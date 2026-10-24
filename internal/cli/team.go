package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/capability/teamshare"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// newTeamCmd joins this machine to a team plane and shows, sends and
// withdraws what it shares (ADR 0012).
func newTeamCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Share derived figures with a team: create, join, preview, status, admin, leave",
		Long: `team connects this machine to a TokenOps team plane. Nothing is sent
until you join. Once joined, the daemon uploads every hour, per UTC day,
repository and kind of work: instruction, turn and outcome counts, time
waiting on the agent, tokens and cost; to a server that accepts them, also
plan use and lockouts, avoidable usage by waste class, realized savings and
the plan advice's verdict. Never prompts, file contents,
transcripts, paths, commit messages or model outputs — ` + "`tokenops team preview`" + `
prints exactly what an upload holds.

Your team sees totals by team, repository and kind of work, with groups
under three people withheld. Your individual figures are visible only to
people an owner explicitly granted, and you see who, why, and every view
(` + "`tokenops team status`" + `).

Starting a team for your organisation: ` + "`tokenops team create --server <url>`" + `
signs you up in the browser with a 14-day free trial; ` + "`tokenops team admin`" + `
then invites people and manages teams, roles, grants and billing.`,
	}
	cmd.AddCommand(newTeamCreateCmd(rf), newTeamAdminCmd(rf), newTeamJoinCmd(rf), newTeamStatusCmd(rf), newTeamPreviewCmd(rf), newTeamSyncCmd(rf),
		newTeamWebCmd(rf), newTeamLeaveCmd(rf))
	return cmd
}

func teamStatePath(cfg config.Config) (string, error) {
	return teamclient.ResolveStatePath(cfg.Team.StatePath)
}

func teamOptions(cfg config.Config) teamshare.Options {
	return teamshare.Options{Days: cfg.Team.EffectiveDays(), RepoNames: cfg.Team.EffectiveRepoNames(), ClientVersion: version.Version}
}

// withTeamDeps opens the event store read-only and hands fn the readers an
// upload is computed from. Without a store the upload still carries the
// transcripts' figures.
func withTeamDeps(ctx context.Context, cfg config.Config, fn func(teamshare.Deps) error) error {
	var agg *spending.EventAggregator
	var finops teamshare.FinOps
	if resolved, err := resolvePlanDBIn("", cfg.Storage.Path); err == nil {
		if store, err := sqlite.OpenReadOnly(ctx, resolved); err == nil {
			defer func() { _ = store.Close() }()
			if eng, err := buildSpendEngine(cfg); err == nil {
				agg = spending.NewAggregator(store, eng)
				finops = teamshare.FinOpsFor(cfg, agg, store, eng)
			}
		}
	}
	d := teamshare.DepsFor(agg)
	d.FinOps = finops
	return fn(d)
}

func defaultDisplayName() string {
	if u, err := user.Current(); err == nil {
		if n := strings.TrimSpace(u.Name); n != "" {
			return n
		}
		return u.Username
	}
	return "member"
}

func defaultDeviceName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "this machine"
	}
	h, _, _ = strings.Cut(h, ".")
	if len(h) > 64 {
		h = h[:64]
	}
	return h
}

func newTeamJoinCmd(rf *rootFlags) *cobra.Command {
	var name, device string
	cmd := &cobra.Command{
		Use:   "join <server-url> <invite>",
		Short: "Join a team with an invite; uploads start with the daemon's next hourly run",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			base, err := teamclient.CheckURL(args[0])
			if err != nil {
				return err
			}
			if name == "" {
				name = defaultDisplayName()
			}
			if device == "" {
				device = defaultDeviceName()
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			resp, err := teamclient.New(base, "").Enroll(ctx, teamwire.EnrollRequest{Invite: args[1], DisplayName: name, DeviceName: device})
			if err != nil {
				return err
			}
			st := teamclient.State{URL: base, OrgName: resp.OrgName, TeamName: resp.TeamName, MemberID: resp.MemberID,
				DeviceID: resp.DeviceID, DeviceToken: resp.DeviceToken, JoinedAt: time.Now().UTC()}
			if err := teamclient.Save(path, st); err != nil {
				return fmt.Errorf("joined, but could not keep the enrolment at %s: %w", path, err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Joined %s, team %s, as %s (device %q).\n\n", resp.OrgName, resp.TeamName, name, device)
			writeSharePolicy(out, cfg)
			if !cfg.Team.UploadsEnabled() {
				fmt.Fprintln(out, "\nteam.enabled is false: nothing is uploaded until you turn it on.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "how you appear to people granted to see individual figures (default: your account name)")
	cmd.Flags().StringVar(&device, "device", "", "this machine's name on your page (default: the host name)")
	return cmd
}

func writeSharePolicy(out io.Writer, cfg config.Config) {
	fmt.Fprintf(out, "Every %s the daemon sends the last %d days, per UTC day, repository (%s) and kind of work:\n",
		cfg.Team.EffectiveInterval(), cfg.Team.EffectiveDays(), cfg.Team.EffectiveRepoNames())
	fmt.Fprintln(out, "  sessions, instructions, turns, tool calls, time waiting on the agent,")
	fmt.Fprintln(out, "  first-try / reworked / interrupted / escalated / rejected counts, tokens, cost and value;")
	fmt.Fprintln(out, "  and, to a server that accepts them, plan use and lockouts per plan, avoidable usage per")
	fmt.Fprintln(out, "  waste class, realized savings per day and the plan advice's verdict.")
	fmt.Fprintln(out, "Never prompts, file contents, transcripts, paths, commit messages or model outputs.")
	fmt.Fprintln(out, "\n  tokenops team preview   exactly what the next upload holds")
	fmt.Fprintln(out, "  tokenops team sync      upload now")
	fmt.Fprintln(out, "  tokenops team status    who can see your individual figures, and who looked")
}

func newTeamStatusCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Which team you joined, what it holds about you, who may see your figures and who looked",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := teamStatePath(cfg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			st, err := teamclient.Load(path)
			if errors.Is(err, teamclient.ErrNotJoined) {
				if jsonOut {
					return writeControlJSON(cmd, map[string]any{"joined": false})
				}
				fmt.Fprintln(out, "Not joined to a team: nothing is shared. Join with `tokenops team join <url> <invite>`.")
				return nil
			}
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			me, meErr := teamclient.New(st.URL, st.DeviceToken).Me(ctx)
			if jsonOut {
				v := map[string]any{"joined": true, "url": st.URL, "org": st.OrgName, "team": st.TeamName,
					"uploads_enabled": cfg.Team.UploadsEnabled(), "last_upload": st.LastUpload, "last_result": st.LastResult}
				if meErr == nil {
					v["server"] = me
				} else {
					v["server_error"] = meErr.Error()
				}
				return writeControlJSON(cmd, v)
			}
			fmt.Fprintf(out, "Joined %s at %s, team %s.\n", st.OrgName, st.URL, st.TeamName)
			switch {
			case !cfg.Team.UploadsEnabled():
				fmt.Fprintln(out, "Uploads are paused (team.enabled: false).")
			case st.LastUpload != nil:
				fmt.Fprintf(out, "Last upload %s: %s.\n", st.LastUpload.Local().Format("2006-01-02 15:04"), st.LastResult)
			default:
				fmt.Fprintf(out, "No upload yet; the daemon uploads every %s.\n", cfg.Team.EffectiveInterval())
			}
			fmt.Fprintln(out, orgBoundariesLine(bcap.LoadOrg(boundaryledger.DefaultDir(), path)))
			if meErr != nil {
				return fmt.Errorf("could not read your page on the server: %w", meErr)
			}
			// The uploader trusts this answer for a day; refresh it now.
			st.NoteSchema(me.MaxSchema, time.Now())
			if _, err := teamclient.Load(path); err == nil {
				_ = teamclient.Save(path, st)
			}
			if me.Plan != nil {
				fmt.Fprintln(out)
				writePlan(out, me.Plan, time.Now())
			}
			writeMe(out, me)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeMe(out io.Writer, me teamwire.Me) {
	fmt.Fprintf(out, "\nThe server holds %d rows over %d days about you (%s, %s), kept %d days.\n",
		me.Buckets, me.Days, me.DisplayName, me.Role, me.RetentionDays)
	fmt.Fprintf(out, "Team, repository and kind-of-work totals withhold groups under %d people.\n", me.MinGroupSize)
	if me.MaxSchema >= teamwire.SchemaFinOps {
		fmt.Fprintln(out, "The server accepts upload schema 2: plan use, waste, realized savings and plan advice are sent with the work figures.")
	} else {
		fmt.Fprintln(out, "The server reads upload schema 1 only: plan use, waste, realized savings and plan advice stay on this machine.")
	}
	if me.SSOEmail != "" {
		fmt.Fprintf(out, "Single sign-on signs you in to the web view as %s.\n", me.SSOEmail)
	}
	fmt.Fprintln(out, "\nWho may see your individual figures:")
	if len(me.Viewers) == 0 {
		fmt.Fprintln(out, "  nobody but you")
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, v := range me.Viewers {
		fmt.Fprintf(w, "  %s (%s)\t%s\tsince %s\t%s\n", v.Name, v.Role, v.Scope, v.GrantedAt.Format("2006-01-02"), v.Reason)
	}
	_ = w.Flush()
	fmt.Fprintln(out, "\nWho looked:")
	if len(me.Views) == 0 {
		fmt.Fprintln(out, "  nobody")
	}
	for _, v := range me.Views {
		fmt.Fprintf(out, "  %s  %s\n", v.At.Local().Format("2006-01-02 15:04"), v.Viewer)
	}
}

func newTeamPreviewCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Exactly what the next upload holds, computed now; nothing is sent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			return withTeamDeps(ctx, cfg, func(d teamshare.Deps) error {
				now := time.Now()
				path, _ := teamStatePath(cfg)
				st, stErr := teamclient.Load(path)
				joined := stErr == nil
				// Not joined, the server's format is unknown: show everything
				// a machine can send. Joined, ask the server (cached a day).
				schema := teamwire.SchemaVersion
				if joined {
					checked := st.SchemaCheckedAt
					schema = teamshare.ServerSchema(ctx, teamclient.New(st.URL, st.DeviceToken), &st, now)
					if st.SchemaCheckedAt != checked {
						_ = teamclient.Save(path, st)
					}
				}
				opts := teamOptions(cfg)
				opts.Schema = teamwire.SchemaVersion
				full, warnings := teamshare.Build(ctx, d, opts, now)
				sent := full.ForServer(schema)
				if jsonOut {
					return writeControlJSON(cmd, sent)
				}
				out := cmd.OutOrStdout()
				if joined {
					fmt.Fprintf(out, "Sent every %s to %s (team %s). Nothing else leaves this machine.\n\n", cfg.Team.EffectiveInterval(), st.URL, st.TeamName)
				} else {
					fmt.Fprintln(out, "Not joined: this is what would be sent after `tokenops team join`. Nothing is sent now.")
					fmt.Fprintln(out)
				}
				writeUpload(out, sent)
				writeFinOps(out, full, finOpsNote(joined, sent.Schema))
				for _, w := range warnings {
					fmt.Fprintln(out, "warning:", w)
				}
				fmt.Fprintln(out, "\n--json prints the upload byte for byte as it is sent.")
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the upload exactly as sent")
	return cmd
}

func writeUpload(out io.Writer, u teamwire.Upload) {
	fmt.Fprintf(out, "Upload: %d days (%s to %s), %d rows.\n\n", len(u.Days), u.Days[0], u.Days[len(u.Days)-1], len(u.Buckets))
	if len(u.Buckets) == 0 {
		fmt.Fprintln(out, "No agent work in these days.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "DAY\tREPO\tKIND\tSESS\tINSTR\tTURNS\tFIRST TRY\tREWORK\tMIN\tTOKENS\tCOST\tVALUE\t")
	for _, b := range u.Buckets {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%.0f\t%d\t$%.2f\t$%.2f\t\n", b.Day, b.Repo, b.Kind, b.Sessions,
			b.Instructions, b.Turns, b.FirstTry, b.Reworked, b.ActiveSeconds/60, b.Tokens, b.CostUSD, b.APIEquivalentUSD)
	}
	_ = w.Flush()
}

// orgBoundariesLine says which of the organisation's rules this machine
// holds, or why it holds none it can use.
func orgBoundariesLine(set bcap.OrgSet) string {
	switch {
	case set.Problem != "":
		return "Organisation boundaries: " + set.Problem + "."
	case set.Version == 0 && len(set.Rules) == 0:
		return "Organisation boundaries: none yet (the daemon fetches them every five minutes)."
	case len(set.Rules) == 0:
		return fmt.Sprintf("Organisation boundaries: version %d, no rules.", set.Version)
	}
	return fmt.Sprintf("Organisation boundaries: version %d, %d rules (`tokenops boundaries status` shows them).", set.Version, len(set.Rules))
}

func newTeamSyncCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Upload now instead of waiting for the daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := teamStatePath(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			return withTeamDeps(ctx, cfg, func(d teamshare.Deps) error {
				opts := teamOptions(cfg)
				opts.Allow = func() error {
					return bcap.CheckShare(bcap.AllRules(cfg), bcap.DestinationTeam, bcap.Env{Approval: cfg.ApprovalMode()})
				}
				res, err := teamshare.Sync(ctx, d, opts, path, time.Now())
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				switch {
				case res.Response.Duplicate:
					fmt.Fprintln(out, "Already sent.")
				default:
					fmt.Fprintf(out, "Sent %d rows covering %d days.\n", res.Response.Accepted, len(res.Upload.Days))
				}
				if res.Response.Stale {
					fmt.Fprintln(out, "Some days already held a newer computation and were left as they were.")
				}
				for _, w := range res.Warnings {
					fmt.Fprintln(out, "warning:", w)
				}
				return nil
			})
		},
	}
}

func newTeamWebCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "web",
		Short: "A single-use link that signs you in to the team's web view",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := teamStatePath(cfg)
			if err != nil {
				return err
			}
			st, err := teamclient.Load(path)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			link, err := teamclient.New(st.URL, st.DeviceToken).LoginLink(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n(works once, until %s)\n", link.URL, link.ExpiresAt.Local().Format("15:04"))
			return nil
		},
	}
}

func newTeamLeaveCmd(rf *rootFlags) *cobra.Command {
	var keep, force bool
	cmd := &cobra.Command{
		Use:   "leave",
		Short: "Stop sharing: revoke this machine and erase its figures on the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := teamStatePath(cfg)
			if err != nil {
				return err
			}
			st, err := teamclient.Load(path)
			if errors.Is(err, teamclient.ErrNotJoined) {
				fmt.Fprintln(cmd.OutOrStdout(), "Not joined to a team.")
				return nil
			}
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if err := teamclient.New(st.URL, st.DeviceToken).Leave(ctx, keep); err != nil && !force {
				return fmt.Errorf("%w\nthe server was not told; retry, or pass --force to forget the team on this machine only", err)
			}
			if err := teamclient.Remove(path); err != nil {
				return err
			}
			// The organisation's rules end with membership.
			_ = boundaryledger.DefaultDir().RemoveOrg()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Left %s. This machine sends nothing more.\n", st.TeamName)
			if keep {
				fmt.Fprintln(out, "Its figures stay in the team's history until the retention period ends.")
			} else {
				fmt.Fprintln(out, "Its figures were erased on the server.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep-history", false, "keep this machine's past figures in the team's totals")
	cmd.Flags().BoolVar(&force, "force", false, "forget the team here even when the server cannot be reached")
	return cmd
}
