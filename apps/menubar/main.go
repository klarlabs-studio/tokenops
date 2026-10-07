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
	actionAlerts  = "alerts"
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
	Error string `json:"error,omitempty"`
	// Note says what the last refresh did.
	Note    string    `json:"note,omitempty"`
	Updated time.Time `json:"updated"`
	err     error
}

// costs is one provider's usage over two windows, its last 30 days day by
// day, and the model it used most.
type costs struct {
	Today  spend `json:"today"`
	Last30 spend `json:"last_30_days"`
	Daily  []day `json:"daily,omitempty"`
	// TopModel is the model with the most tokens over the 30 days.
	TopModel string `json:"top_model,omitempty"`
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

	// notify shows a desktop notification; nil where the host has none.
	notify   func(title, body string) error
	alerts   alerter
	settings settings
	// prefs is where settings live; empty when there is no config dir.
	prefs string
	// notifyFailed logs why notifications fail once, not every minute.
	notifyFailed sync.Once
	// note says what the last refresh did, shown until noteUntil.
	note      string
	noteUntil time.Time
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
		Window:       app.WindowOptions{ID: panelWindow, Width: 360, Height: 760, Kind: app.WindowKindPanel},
	})
	if err != nil {
		return nil, err
	}
	m := &menubar{app: a, rt: rt, daemon: d}
	if n, ok := host.(platform.Notifier); ok {
		m.notify = n.ShowNotification
	}
	if path, err := settingsPath(); err == nil {
		m.prefs, m.settings = path, loadSettings(path)
	}
	if err := m.register(); err != nil {
		return nil, err
	}
	a.OnAction(m.onAction)
	return m, nil
}

// register grants the panel what it needs and registers its commands.
func (m *menubar) register() error {
	grant, err := domain.NewCapabilityGrant(
		"panel", "read the glance, ask the daemon to poll now, change the coach preset, close the panel",
		[]domain.WindowID{panelWindow},
		[]domain.Origin{domain.OriginPackagedLocal},
		[]domain.PermissionSpec{{Name: "glance.read"}, {Name: "glance.refresh"}, {Name: "coach.change"}, {Name: "panel.close"}},
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
		vitra.Register(m.rt, vitra.Command[struct{}, view]{
			Name:        "sources.refresh",
			Description: "Ask the daemon's usage readers to poll now, then read again",
			Permission:  "glance.refresh",
			Handler: func(ctx context.Context, _ domain.Invocation, _ struct{}) (view, error) {
				return m.refreshNow(ctx), nil
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
	m.setTray(status{Title: "—", Outer: -1, Inner: -1, Tooltip: "TokenOps: reading the daemon…"})
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
	var notes []note
	if v.Error == "" && v.Glance != nil {
		notes = m.alerts.observe(v.Glance)
	}
	on := m.settings.alertsOn()
	if time.Now().Before(m.noteUntil) {
		v.Note = m.note
	}
	m.mu.Unlock()
	if on {
		m.show(notes)
	}
	st := status{Title: "—", Outer: -1, Inner: -1, Tooltip: "TokenOps: " + v.Error}
	if v.Glance != nil {
		st = statusOf(v.Glance)
	}
	m.setTray(st)
	_ = m.app.Emit(ctx, viewEvent, v)
	return v
}

// refreshAgain is when the panel reads again after asking for a refresh:
// most readers answer within seconds, the slowest within half a minute.
var refreshAgain = []time.Duration{4 * time.Second, 15 * time.Second, 35 * time.Second}

// refreshNow asks the daemon's readers to poll, shows what it has at once
// with a note saying so, and reads again as the new readings arrive.
// Re-reading alone returned the same readings: the daemon polls each
// vendor on its own interval, up to 15 minutes.
func (m *menubar) refreshNow(ctx context.Context) view {
	r, err := m.daemon.refreshSources(ctx)
	if err == nil {
		// The note stays until the last follow-up read, so the panel says
		// what is happening while the readings come in.
		m.mu.Lock()
		m.note, m.noteUntil = r.note(time.Now()), time.Now().Add(refreshAgain[len(refreshAgain)-1]+time.Second)
		m.mu.Unlock()
	}
	v := m.refresh(ctx)
	if err == nil && r.Requested {
		// Not tied to ctx: a command's context ends when it returns, and
		// the readings arrive after that.
		bg := context.WithoutCancel(ctx)
		go func() {
			start := time.Now()
			for _, d := range refreshAgain {
				time.Sleep(time.Until(start.Add(d)))
				m.refresh(bg)
			}
		}()
	}
	return v
}

// show delivers notes as desktop notifications. macOS shows them only
// for the packaged app; a failure is logged once.
func (m *menubar) show(notes []note) {
	if m.notify == nil {
		return
	}
	for _, n := range notes {
		if err := m.notify(n.Title, n.Body); err != nil {
			m.notifyFailed.Do(func() { fmt.Fprintln(os.Stderr, "tokenops-menubar: notifications:", err) })
			return
		}
	}
}

// setTray shows the icon alone: the busiest plan's week and session as two
// rings, each filled to the share left. A click opens the panel with
// every plan's details under the icon; a right click opens the menu.
func (m *menubar) setTray(st status) {
	if err := m.app.SetTray(app.TraySpec{
		Tooltip: st.Tooltip,
		Icon:    trayIcon(st.Outer, st.Inner), Template: true,
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
			c := costs{Today: today, Last30: last30}
			// The chart and the top model are extras: a daemon without the
			// series still shows the figures.
			c.Daily, _ = m.daemon.daily(ctx, provider)
			c.TopModel, _ = m.daemon.topModel(ctx, provider)
			mu.Lock()
			out[provider] = c
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
		{ID: actionAlerts, Label: "Alerts", Checked: m.alertsOn(), Disabled: m.notify == nil},
		{Separator: true},
		{ID: actionQuit, Label: "Quit"},
	}
}

// onAction handles the tray menu.
func (m *menubar) onAction(id string) {
	ctx := context.Background()
	switch id {
	case actionRefresh:
		m.refreshNow(ctx)
	case actionLogin:
		on, err := m.app.LoginItemEnabled()
		if err == nil {
			err = m.app.SetLoginItem(!on)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "tokenops-menubar: launch at login:", err)
		}
		m.refresh(ctx)
	case actionAlerts:
		m.toggleAlerts()
		m.refresh(ctx)
	case actionQuit:
		m.app.Quit()
	}
}

func (m *menubar) alertsOn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.alertsOn()
}

// toggleAlerts turns alerts on or off and remembers it across launches.
func (m *menubar) toggleAlerts() {
	m.mu.Lock()
	on := !m.settings.alertsOn()
	m.settings.Alerts = &on
	s := m.settings
	m.mu.Unlock()
	if m.prefs == "" {
		return
	}
	if err := saveSettings(m.prefs, s); err != nil {
		fmt.Fprintln(os.Stderr, "tokenops-menubar: alerts setting:", err)
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
