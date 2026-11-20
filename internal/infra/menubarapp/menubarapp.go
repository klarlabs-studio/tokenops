// Package menubarapp keeps the installed menu bar app in step with the one
// shipped beside the tokenops binary.
//
// The app ships with the Homebrew install on macOS. `tokenops menubar`
// copies it to ~/Applications, a path that stays put across upgrades, and
// the running daemon refreshes that copy when it starts on a new binary.
// The copy and the relaunch are injected through Ops so the decision can be
// tested without touching the real app or its process.
package menubarapp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AppName is the bundle's name.
const AppName = "TokenOps.app"

// ProcessPattern matches the running app's command line for pgrep -f and
// pkill -f (an extended regex): the bundle's own binary only. The bundle
// also holds the presence helper (Contents/MacOS/tokenops-presence), which
// a refresh must never kill: it may be holding the person's Touch ID dialog
// for an approval.
const ProcessPattern = AppName + "/Contents/MacOS/TokenOps( |$)"

// Result says what a refresh or an install did.
type Result struct {
	// Refreshed reports that the installed copy was replaced.
	Refreshed bool
	// Relaunched reports that the app was opened: it was running, or the
	// person keeps it in the menu bar.
	Relaunched bool
}

// ErrNotShipped is an install with no app beside the binary and none
// installed.
var ErrNotShipped = errors.New("the menu bar app is not beside this binary: it ships with the Homebrew install (brew install --cask klarlabs-studio/tap/tokenops); from a source checkout, run `make menubar`")

// Ops are the side effects a refresh needs. System provides the real ones.
type Ops struct {
	Running func() bool
	// Wanted reports that the person keeps the app in the menu bar (see
	// Wanted). Nil is no.
	Wanted  func() bool
	Quit    func()
	Replace func(src, dst string) error
	Open    func(dst string) error
}

// System returns the operations that act on the real app: pgrep, pkill,
// ditto, and open. A quit waits for the app to exit and an open waits for
// it to run, so a reopen right after a quit is not lost.
func System() Ops {
	running := func() bool { return exec.Command("pgrep", "-f", ProcessPattern).Run() == nil }
	return Ops{
		Running: running,
		Wanted:  func() bool { return Wanted(IntentPath()) },
		Quit: func() {
			stop(func(force bool) {
				args := []string{"-f", ProcessPattern}
				if force {
					args = append([]string{"-KILL"}, args...)
				}
				_ = exec.Command("pkill", args...).Run()
			}, running, time.Sleep)
		},
		Replace: copyApp,
		Open: func(dst string) error {
			return launch(func() error { return exec.Command("open", dst).Run() }, running, time.Sleep)
		},
	}
}

// launchChecks and launchEvery bound how long an open waits for the app
// to run, per attempt: five seconds.
const (
	launchChecks = 20
	launchEvery  = 250 * time.Millisecond
)

// errNotStarted is an open that never showed the app running.
var errNotStarted = errors.New("the app did not start")

// launch opens the app and waits until it runs. `open` returns once
// Launch Services took the request, which it may hand to an instance that
// is still exiting; so an app not seen running is opened once more before
// the launch fails.
func launch(open func() error, running func() bool, sleep func(time.Duration)) error {
	for attempt := 0; attempt < 2; attempt++ {
		if err := open(); err != nil {
			return err
		}
		for range launchChecks {
			if running() {
				return nil
			}
			sleep(launchEvery)
		}
	}
	return errNotStarted
}

// stop asks the app to exit and waits until it has; one still running
// after launchChecks is stopped hard. kill(false) asks, kill(true) forces.
func stop(kill func(force bool), running func() bool, sleep func(time.Duration)) {
	kill(false)
	for range launchChecks {
		if !running() {
			return
		}
		sleep(launchEvery)
	}
	kill(true)
	for range launchChecks {
		if !running() {
			return
		}
		sleep(launchEvery)
	}
}

// intent is what the app records of the person's choice in its
// preferences file (apps/menubar, settings).
type intent struct {
	// Open is true while the app runs and false once the person quit it
	// from its menu.
	Open *bool `json:"open"`
	// LaunchAtLogin mirrors the app's Launch at Login item.
	LaunchAtLogin bool `json:"launch_at_login"`
}

// IntentPath is the app's preferences file; empty when there is no
// config directory.
func IntentPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "TokenOps", "menubar.json")
}

// Wanted reports whether the app's preferences at path say the person
// keeps it in the menu bar: it was open and they did not quit it, or
// Launch at Login is on. A missing or unreadable file, or one from an app
// that records neither, is no.
func Wanted(path string) bool {
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var in intent
	if json.Unmarshal(b, &in) != nil {
		return false
	}
	return (in.Open != nil && *in.Open) || in.LaunchAtLogin
}

// ShippedBeside is the app beside the binary at exe, following symlinks:
// Homebrew puts a link on PATH into the versioned Caskroom directory that
// holds the app. Empty when there is none.
func ShippedBeside(exe string) string {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	app := filepath.Join(filepath.Dir(exe), AppName)
	if st, err := os.Stat(app); err == nil && st.IsDir() {
		return app
	}
	return ""
}

// Refresh replaces the installed app at dst with the shipped one at src
// when they differ. It never installs an app that is not already there.
// It reopens dst when it was running, or when the person keeps it in the
// menu bar: a refresh runs when the new daemon starts, and by then the app
// may not be running for reasons that are not the person's choice (it
// exited, or macOS did not start it after a restart). Nothing shipped, or
// nothing installed, is a no-op, not an error.
func Refresh(src, dst string, ops Ops) (Result, error) {
	if src == "" {
		return Result{}, nil
	}
	if _, err := os.Stat(dst); err != nil {
		return Result{}, nil
	}
	differs, err := Differs(src, dst)
	if err != nil {
		return Result{}, err
	}
	if !differs {
		return Result{}, nil
	}
	running := ops.Running()
	reopen := running || (ops.Wanted != nil && ops.Wanted())
	if running {
		ops.Quit()
	}
	if err := ops.Replace(src, dst); err != nil {
		err = fmt.Errorf("install %s: %w", dst, err)
		// The copy goes to a temporary bundle first, so the old app is
		// usually still in place: reopen it rather than leave the menu
		// bar empty.
		if !reopen {
			return Result{}, err
		}
		if _, statErr := os.Stat(dst); statErr != nil {
			return Result{}, err
		}
		if openErr := ops.Open(dst); openErr != nil {
			return Result{}, errors.Join(err, fmt.Errorf("reopen %s: %w", dst, openErr))
		}
		return Result{Relaunched: true}, err
	}
	res := Result{Refreshed: true}
	if !reopen {
		return res, nil
	}
	if err := ops.Open(dst); err != nil {
		return res, fmt.Errorf("open %s: %w", dst, err)
	}
	res.Relaunched = true
	return res, nil
}

// Install is `tokenops menubar`: it puts the shipped app at dst when dst
// is missing or differs, and ends with the app running. An app that is
// already the shipped one is not quit; one already running is left as it
// is. With nothing shipped (a source build) the installed copy is opened;
// with nothing shipped and nothing installed it returns ErrNotShipped.
func Install(src, dst string, ops Ops) (Result, error) {
	_, statErr := os.Stat(dst)
	installed := statErr == nil
	if src == "" && !installed {
		return Result{}, ErrNotShipped
	}
	var res Result
	if src != "" {
		differs := true
		if installed {
			d, err := Differs(src, dst)
			if err != nil {
				return Result{}, err
			}
			differs = d
		}
		if differs {
			if ops.Running() {
				ops.Quit()
			}
			if err := ops.Replace(src, dst); err != nil {
				return Result{}, fmt.Errorf("install %s: %w", dst, err)
			}
			res.Refreshed = true
		}
	}
	if !res.Refreshed && ops.Running() {
		return res, nil
	}
	if err := ops.Open(dst); err != nil {
		return res, fmt.Errorf("open %s: %w", dst, err)
	}
	res.Relaunched = true
	return res, nil
}

// Differs reports whether the installed app is not the shipped one. The
// bundle version decides when both carry one. The executable's bytes decide
// too: a same-version rebuild is still a different program. An installed
// copy with no executable is broken, so it differs.
func Differs(shipped, installed string) (bool, error) {
	if bundleVersion(shipped) != bundleVersion(installed) {
		return true, nil
	}
	want, err := fileDigest(executable(shipped))
	if err != nil {
		return false, fmt.Errorf("shipped app: %w", err)
	}
	have, err := fileDigest(executable(installed))
	if err != nil {
		return true, nil
	}
	return want != have, nil
}

func executable(app string) string {
	return filepath.Join(app, "Contents", "MacOS", "TokenOps")
}

// bundleVersion is the short version and build number from the app's
// Info.plist, or "" when there is no readable plist.
func bundleVersion(app string) string {
	f, err := os.Open(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	keys := plistStrings(f)
	return keys["CFBundleShortVersionString"] + "/" + keys["CFBundleVersion"]
}

// plistStrings reads the top-level string values of an XML property list.
func plistStrings(r io.Reader) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(r)
	var key string
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var text string
		switch start.Name.Local {
		case "key":
			if dec.DecodeElement(&key, &start) != nil {
				return out
			}
		case "string":
			if dec.DecodeElement(&text, &start) != nil {
				return out
			}
			if key != "" {
				out[key] = strings.TrimSpace(text)
				key = ""
			}
		}
	}
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyApp replaces dst with a copy of the bundle src. ditto keeps the
// bundle's modes, symlinks and signature intact.
func copyApp(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	_ = os.RemoveAll(tmp)
	if out, err := exec.Command("ditto", src, tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", tmp).Run()
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
