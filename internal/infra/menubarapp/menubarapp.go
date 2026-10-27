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
	"encoding/xml"
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

// Result says what a refresh did.
type Result struct {
	// Refreshed reports that the installed copy was replaced.
	Refreshed bool
	// Relaunched reports that the app was reopened, because it was running.
	Relaunched bool
}

// Ops are the side effects a refresh needs. System provides the real ones.
type Ops struct {
	Running func() bool
	Quit    func()
	Replace func(src, dst string) error
	Open    func(dst string) error
}

// System returns the operations that act on the real app: pgrep, pkill,
// ditto, and open.
func System() Ops {
	return Ops{
		Running: func() bool { return exec.Command("pgrep", "-f", ProcessPattern).Run() == nil },
		Quit: func() {
			_ = exec.Command("pkill", "-f", ProcessPattern).Run()
			time.Sleep(500 * time.Millisecond)
		},
		Replace: copyApp,
		Open:    func(dst string) error { return exec.Command("open", dst).Run() },
	}
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
// when they differ. It never installs an app that is not already there, and
// it reopens dst only if it was running. Nothing shipped, or nothing
// installed, is a no-op, not an error.
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
	if running {
		ops.Quit()
	}
	if err := ops.Replace(src, dst); err != nil {
		return Result{}, fmt.Errorf("install %s: %w", dst, err)
	}
	res := Result{Refreshed: true}
	if !running {
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
