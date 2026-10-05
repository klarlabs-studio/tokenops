// Command tokenops-menubar puts TokenOps in the menu bar: an icon whose
// ring fills to the busiest plan window, and a panel with every plan's
// windows, pace, cost and the coach's findings when you click it. It reads
// the local daemon's API (ADR 0010) and nothing else.
//
//	CGO_ENABLED=1 go run -tags vitra_native .
//
// The panel is an ordinary window to Vitra's security model: it may read
// the glance, change the coach preset, and close itself. The API token
// stays in this process; the panel never sees it.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"time"

	"go.klarlabs.de/vitra"
	"go.klarlabs.de/vitra/app"
	"go.klarlabs.de/vitra/audit"
	"go.klarlabs.de/vitra/domain"
	"go.klarlabs.de/vitra/platform"
	"go.klarlabs.de/vitra/platform/darwin"
	"go.klarlabs.de/vitra/platform/linux"
	"go.klarlabs.de/vitra/platform/windows"
)

const (
	appID = "de.klarlabs.tokenops.menubar"
	// panelWindow is the tray panel a click on the icon opens.
	panelWindow domain.WindowID = "panel"
	// viewEvent carries each new view to the panel.
	viewEvent domain.EventName = "glance.update"
	// refreshEvery is how often the tray is refreshed.
	refreshEvery = time.Minute
)

// Tray menu action IDs.
const (
	actionRefresh = "refresh"
	actionLogin   = "login"
	actionQuit    = "quit"
)

//go:embed frontend
var frontendFS embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tokenops-menubar:", err)
		os.Exit(1)
	}
}

func run() error {
	m, err := newMenubar(newHost(), newDaemon(), &audit.MemorySink{})
	if err != nil {
		return err
	}
	return m.Run(context.Background())
}

// view is what the panel shows.
type view struct {
	Glance json.RawMessage `json:"glance,omitempty"`
	Coach  json.RawMessage `json:"coach,omitempty"`
	// Findings is GET /api/findings; absent from a daemon without it.
	Findings json.RawMessage `json:"findings,omitempty"`
	// Costs is each provider's usage today and over the last 30 days.
	Costs map[string]costs `json:"costs,omitempty"`
	// Error says why there is nothing to show, in the operator's terms.
	Error   string    `json:"error,omitempty"`
	Updated time.Time `json:"updated"`
	err     error
}

// costs is one provider's usage over two windows.
type costs struct {
	Today  spend `json:"today"`
	Last30 spend `json:"last_30_days"`
}

// presetInput is the coach.preset command's argument.
type presetInput struct {
	Preset string `json:"preset"`
}

// menubar is the app: the tray, its panel, and the daemon they read.
type menubar struct {
	app    *app.App
	rt     *vitra.Runtime
	daemon *daemon

	mu   sync.Mutex
	last view
}

// newMenubar wires the runtime, the panel's grant and commands, and the
// tray.
func newMenubar(host app.DesktopHost, d *daemon, sink audit.Sink) (*menubar, error) {
	rt, err := vitra.New(vitra.Config{AppID: appID})
	if err != nil {
		return nil, err
	}
	rt.SetAudit(sink)
	assets, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		return nil, err
	}
	a, err := app.New(app.Options{
		AppID: appID, Title: "TokenOps", Assets: assets, Host: host, Runtime: rt,
		Presentation: app.PresentationAccessory,
		Window:       app.WindowOptions{ID: panelWindow, Width: 360, Height: 620, Kind: app.WindowKindPanel},
	})
	if err != nil {
		return nil, err
	}
	m := &menubar{app: a, rt: rt, daemon: d}
	if err := m.register(); err != nil {
		return nil, err
	}
	a.OnAction(m.onAction)
	return m, nil
}

// register grants the panel what it needs and registers its commands.
func (m *menubar) register() error {
	grant, err := domain.NewCapabilityGrant(
		"panel", "read the glance, change the coach preset, close the panel",
		[]domain.WindowID{panelWindow},
		[]domain.Origin{domain.OriginPackagedLocal},
		[]domain.PermissionSpec{{Name: "glance.read"}, {Name: "coach.change"}, {Name: "panel.close"}},
	)
	if err != nil {
		return err
	}
	return errors.Join(
		m.rt.RegisterGrant(grant),
		vitra.Register(m.rt, vitra.Command[struct{}, view]{
			Name:        "glance.follow",
			Description: "Send view updates to the calling window; returns the current view",
			Permission:  "glance.read",
			Handler: func(ctx context.Context, inv domain.Invocation, _ struct{}) (view, error) {
				id := domain.SubscriptionID("glance-" + string(inv.Caller.Window))
				if _, err := m.rt.SubscribeEvent(id, viewEvent, inv.Caller.Window); err != nil {
					return view{}, err
				}
				return m.refresh(ctx), nil
			},
		}),
		vitra.Register(m.rt, vitra.Command[presetInput, view]{
			Name:        "coach.preset",
			Description: "Apply a coach preset through the daemon, which audits it",
			Permission:  "coach.change",
			Handler: func(ctx context.Context, _ domain.Invocation, in presetInput) (view, error) {
				if _, err := m.daemon.setPreset(ctx, in.Preset); err != nil {
					return view{}, err
				}
				return m.refresh(ctx), nil
			},
		}),
		vitra.Register(m.rt, vitra.Command[struct{}, vitra.Void]{
			Name:        "panel.close",
			Description: "Hide the tray panel",
			Permission:  "panel.close",
			Handler: func(context.Context, domain.Invocation, struct{}) (vitra.Void, error) {
				return vitra.Void{}, m.app.HideTrayPanel()
			},
		}),
	)
}

// Run shows the tray and blocks until the app quits. The icon appears at
// once, marked as reading; the first read can take as long as a busy
// daemon does, and an app that shows nothing until then looks broken.
func (m *menubar) Run(ctx context.Context) error {
	m.setTray(status{Title: "—", Tooltip: "TokenOps: reading the daemon…"})
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go m.poll(ctx)
	return m.app.Run(ctx)
}

// poll reads the daemon now and every refreshEvery after.
func (m *menubar) poll(ctx context.Context) {
	m.refresh(ctx)
	tick := time.NewTicker(refreshEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.refresh(ctx)
		}
	}
}

// refresh reads the daemon, updates the tray, and pushes the view to the
// panel. A daemon that is not running is shown, not returned as an error:
// the menu bar is where an operator would notice it.
func (m *menubar) refresh(ctx context.Context) view {
	v := m.read(ctx)
	m.mu.Lock()
	if v.Error != "" && m.last.Glance != nil && !errors.Is(v.err, errNoDaemon) {
		// A slow or failed read keeps the last good reading, marked.
		stale := m.last
		stale.Error = v.Error + " — showing the reading from " + m.last.Updated.Format("15:04")
		v = stale
	} else {
		m.last = v
	}
	m.mu.Unlock()
	st := status{Title: "—", Tooltip: "TokenOps: " + v.Error}
	if v.Glance != nil {
		st = statusOf(v.Glance)
	}
	m.setTray(st)
	_ = m.app.Emit(ctx, viewEvent, v)
	return v
}

// setTray shows the icon alone, its ring filled to the busiest window. A
// click opens the panel with every plan's details under the icon; a right
// click opens the menu.
func (m *menubar) setTray(st status) {
	if err := m.app.SetTray(app.TraySpec{
		Tooltip: st.Tooltip,
		Icon:    trayIcon(st.Pct), Template: true,
		Panel: panelWindow,
		Items: m.menu(),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "tokenops-menubar:", err)
	}
}

// read asks the daemon for the glance, then the coach, its findings and
// each plan's cost at once.
func (m *menubar) read(ctx context.Context) view {
	v := view{Updated: time.Now()}
	g, err := m.daemon.glance(ctx)
	if err != nil {
		v.Error, v.err = explain(err), err
		return v
	}
	v.Glance = g
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if c, err := m.daemon.coach(ctx); err == nil {
			v.Coach = c
		}
	}()
	go func() {
		defer wg.Done()
		if f, err := m.daemon.findings(ctx); err == nil {
			v.Findings = f
		}
	}()
	v.Costs = m.costs(ctx, g)
	wg.Wait()
	return v
}

// costs reads each plan's provider's usage today and over 30 days, all at
// once. A provider whose figures cannot be read is left out.
func (m *menubar) costs(ctx context.Context, g json.RawMessage) map[string]costs {
	var gv glanceView
	if json.Unmarshal(g, &gv) != nil {
		return nil
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = map[string]costs{}
	)
	for _, r := range gv.PlanHeadroom.Reports {
		wg.Add(1)
		go func(provider string) {
			defer wg.Done()
			today, err1 := m.daemon.spendSince(ctx, provider, "24h")
			last30, err2 := m.daemon.spendSince(ctx, provider, "720h")
			if err1 != nil || err2 != nil {
				return
			}
			mu.Lock()
			out[provider] = costs{Today: today, Last30: last30}
			mu.Unlock()
		}(r.Provider)
	}
	wg.Wait()
	return out
}

// explain words an error for the operator.
func explain(err error) string {
	switch {
	case errors.Is(err, errNoDaemon):
		return "the TokenOps daemon is not running — run `tokenops daemon install`"
	case errors.Is(err, errSlow):
		return "the TokenOps daemon is slow to answer"
	}
	return err.Error()
}

// menu is the tray menu.
func (m *menubar) menu() []platform.MenuItem {
	login, err := m.app.LoginItemEnabled()
	return []platform.MenuItem{
		{ID: actionRefresh, Label: "Refresh"},
		{Separator: true},
		{ID: actionLogin, Label: "Launch at Login", Checked: login, Disabled: err != nil},
		{Separator: true},
		{ID: actionQuit, Label: "Quit"},
	}
}

// onAction handles the tray menu.
func (m *menubar) onAction(id string) {
	ctx := context.Background()
	switch id {
	case actionRefresh:
		m.refresh(ctx)
	case actionLogin:
		on, err := m.app.LoginItemEnabled()
		if err == nil {
			err = m.app.SetLoginItem(!on)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "tokenops-menubar: launch at login:", err)
		}
		m.refresh(ctx)
	case actionQuit:
		m.app.Quit()
	}
}

func newHost() app.DesktopHost {
	switch runtime.GOOS {
	case "darwin":
		h := darwin.New()
		h.SetProgramName("tokenops-menubar")
		return h
	case "windows":
		h := windows.New()
		h.SetProgramName("tokenops-menubar")
		return h
	default:
		h := linux.New()
		h.SetProgramName("tokenops-menubar")
		return h
	}
}
