package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// openBrowser opens an http(s) URL in the default browser. Tests replace it.
var openBrowser = func(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u) //nolint:gosec // an http(s) URL checked by sameServer, passed as one argument with no shell
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u) //nolint:gosec // as above
	default:
		cmd = exec.Command("xdg-open", u) //nolint:gosec // as above
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Start()
}

// deviceLinkSleep waits between polls of a device link. Tests replace it.
var deviceLinkSleep func(context.Context, time.Duration) error

// maxLinkWait bounds how long a command waits for approval, whatever
// expiry the server names.
const maxLinkWait = 20 * time.Minute

// sameServer accepts a URL the server handed out to open in the browser
// only when it is on the server's own origin: a server cannot make this
// machine open another site, a file or another scheme.
func sameServer(base, raw string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != b.Scheme || u.Host != b.Host || u.User != nil {
		return "", fmt.Errorf("the server sent a link to another site (%q); not opened", raw)
	}
	return u.String(), nil
}

// linkInBrowser runs the device-link handshake: it asks the server for a
// code, shows it, opens the page to type it into, and waits until the
// person approves this machine there. The code is never put in the URL, so
// only someone who can see this terminal can approve it.
func linkInBrowser(cmd *cobra.Command, base string, req teamwire.DeviceLinkRequest, noBrowser bool) (teamwire.DeviceLinkResult, error) {
	out := cmd.OutOrStdout()
	client := teamclient.New(base, "")
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	link, err := client.StartDeviceLink(ctx, req)
	cancel()
	if err != nil {
		return teamwire.DeviceLinkResult{}, err
	}
	page, err := sameServer(base, link.VerificationURL)
	if err != nil {
		return teamwire.DeviceLinkResult{}, err
	}
	fmt.Fprintf(out, "Your code: %s\n\n", link.UserCode)
	fmt.Fprintf(out, "Open %s, sign in, and type this code there.\n", page)
	if req.DeviceName != "" {
		fmt.Fprintf(out, "Approve it only if the page names this machine, %q.\n", req.DeviceName)
	}
	if !noBrowser {
		if err := openBrowser(page); err != nil {
			fmt.Fprintln(out, "(could not open a browser; open the address yourself)")
		}
	}
	fmt.Fprintf(out, "Waiting for approval (the code works until %s; Ctrl-C to stop)...\n", link.ExpiresAt.Local().Format("15:04"))
	wait := time.Until(link.ExpiresAt) + 30*time.Second
	if wait <= 0 || wait > maxLinkWait {
		wait = maxLinkWait
	}
	ctx, cancel = context.WithTimeout(cmd.Context(), wait)
	defer cancel()
	res, err := client.WaitDeviceLink(ctx, link, deviceLinkSleep)
	if errors.Is(err, context.DeadlineExceeded) {
		return res, errors.New("the code was not approved in time; run the command again")
	}
	return res, err
}

// saveLinkResult keeps what an approved link handed out: the enrolment and
// the administrator's credential, each in its own 0600 file.
func saveLinkResult(statePath, base string, res teamwire.DeviceLinkResult) error {
	if e := res.Enrollment; e != nil {
		st := teamclient.State{URL: base, OrgName: e.OrgName, TeamName: e.TeamName, MemberID: e.MemberID,
			DeviceID: e.DeviceID, DeviceToken: e.DeviceToken, JoinedAt: time.Now().UTC()}
		if err := teamclient.Save(statePath, st); err != nil {
			return fmt.Errorf("enrolled, but could not keep the enrolment at %s: %w", statePath, err)
		}
	}
	if a := res.Admin; a != nil {
		path := teamclient.AdminStatePath(statePath)
		as := teamclient.AdminState{URL: base, OrgName: a.OrgName, Role: a.Role, Token: a.Token, ExpiresAt: a.ExpiresAt}
		if err := teamclient.SaveAdmin(path, as); err != nil {
			return fmt.Errorf("signed in, but could not keep the administrator credential at %s: %w", path, err)
		}
	}
	return nil
}

// writePlan explains the organisation's billing state and when figures
// appear, so a new trial with nothing to show yet never looks broken.
func writePlan(out io.Writer, p *teamwire.Plan, now time.Time) {
	if p == nil {
		return
	}
	day := func(t *time.Time) string { return t.Local().Format("Mon 2 Jan 2006") }
	switch p.Status {
	case teamwire.PlanTrialing:
		if p.TrialEndsAt != nil {
			left := int(p.TrialEndsAt.Sub(now).Hours()/24 + 0.999)
			fmt.Fprintf(out, "Free trial until %s (%d days left).\n", day(p.TrialEndsAt), max(left, 0))
		} else {
			fmt.Fprintln(out, "Free trial.")
		}
	case teamwire.PlanActive:
		fmt.Fprintln(out, "Subscription active.")
	case teamwire.PlanPastDue:
		fmt.Fprintln(out, "A payment failed and is being retried; uploads continue meanwhile.")
	}
	if p.UploadsPaused {
		fmt.Fprintln(out, "Uploads are PAUSED: the organisation has no active trial or subscription.")
		if p.PurgeAt != nil {
			fmt.Fprintf(out, "The figures already sent stay viewable, read-only, until %s, then they are deleted.\n", day(p.PurgeAt))
		}
		fmt.Fprintln(out, "An owner or admin can subscribe in the web view or with `tokenops team admin billing --checkout`.")
	}
	if p.FirstWeekAt != nil {
		fmt.Fprintf(out, "Your first week of team totals appears on %s: each week is released three days after it ends.\n", day(p.FirstWeekAt))
	}
}

func newTeamCreateCmd(rf *rootFlags) *cobra.Command {
	var server, name, device string
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "create --server <url>",
		Short: "Start a team: sign up with GitHub in the browser, create an organisation with a free trial, and join this machine",
		Long: `create opens the team server's sign-up page in your browser. Sign in with
GitHub (the server reads your verified primary e-mail address, nothing
else from your account); if you have no organisation yet, name one and
you become its owner, with a 14-day free trial. Then type the code this command shows into the page:
that approves this machine, which joins the organisation and receives an
administrator's credential for ` + "`tokenops team admin`" + `.

The code is shown here and never put in a link, so only someone who can
see this terminal can approve this machine.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := teamStatePath(cfg)
			if err != nil {
				return err
			}
			if st, err := teamclient.Load(path); err == nil {
				return fmt.Errorf("already joined %s (team %s); run `tokenops team leave` first", st.URL, st.TeamName)
			}
			if server == "" {
				return errors.New("--server is required: the team server's address, e.g. https://team.example.eu")
			}
			base, err := teamclient.CheckURL(server)
			if err != nil {
				return err
			}
			if name == "" {
				name = defaultDisplayName()
			}
			if device == "" {
				device = defaultDeviceName()
			}
			res, err := linkInBrowser(cmd, base, teamwire.DeviceLinkRequest{Enroll: true, Admin: true, Create: true,
				DisplayName: name, DeviceName: device}, noBrowser)
			if err != nil {
				return err
			}
			if res.Enrollment == nil {
				return errors.New("the server approved the link but did not enrol this machine")
			}
			if err := saveLinkResult(path, base, res); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			e := res.Enrollment
			fmt.Fprintf(out, "\nThis machine joined %s", e.OrgName)
			if e.TeamName != "" {
				fmt.Fprintf(out, ", team %s", e.TeamName)
			}
			fmt.Fprintf(out, " (device %q).\n", device)
			if res.Admin != nil {
				fmt.Fprintf(out, "You administer it as %s from this machine until %s (`tokenops team admin`).\n",
					res.Admin.Role, res.Admin.ExpiresAt.Local().Format("2006-01-02"))
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if me, err := teamclient.New(base, e.DeviceToken).Me(ctx); err == nil {
				fmt.Fprintln(out)
				writePlan(out, me.Plan, time.Now())
			}
			fmt.Fprintln(out, "\nNext: invite your team with `tokenops team admin invite --team <team>`.")
			fmt.Fprintln(out)
			writeSharePolicy(out, cfg)
			if !cfg.Team.UploadsEnabled() {
				fmt.Fprintln(out, "\nteam.enabled is false: nothing is uploaded until you turn it on.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "the team server's address (required)")
	cmd.Flags().StringVar(&name, "name", "", "your name in the organisation (default: your account name)")
	cmd.Flags().StringVar(&device, "device", "", "this machine's name on your page (default: the host name)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the address instead of opening a browser")
	return cmd
}
