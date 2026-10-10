package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// adminTokenEnv supplies an administrator's API token instead of the one
// `team admin login` keeps, e.g. one klarlabs' console minted.
const adminTokenEnv = "TOKENOPS_TEAM_ADMIN_TOKEN"

// adminFlags are the flags every `team admin` command shares.
type adminFlags struct {
	server string
	json   bool
}

// adminCtx is a resolved administrator: whom to call and with what.
type adminCtx struct {
	client *teamclient.Client
	url    string
}

// resolveAdmin finds the administrator's credential: TOKENOPS_TEAM_ADMIN_TOKEN
// with --server (or the joined team's server), else the one `team admin
// login` kept.
func resolveAdmin(rf *rootFlags, af *adminFlags) (adminCtx, error) {
	cfg, err := loadConfig(rf)
	if err != nil {
		return adminCtx{}, err
	}
	path, err := teamStatePath(cfg)
	if err != nil {
		return adminCtx{}, err
	}
	if tok := strings.TrimSpace(os.Getenv(adminTokenEnv)); tok != "" {
		server := af.server
		if server == "" {
			if st, err := teamclient.Load(path); err == nil {
				server = st.URL
			}
		}
		if server == "" {
			return adminCtx{}, fmt.Errorf("%s is set: pass --server with the team server's address", adminTokenEnv)
		}
		base, err := teamclient.CheckURL(server)
		if err != nil {
			return adminCtx{}, err
		}
		return adminCtx{client: teamclient.New(base, tok), url: base}, nil
	}
	as, err := teamclient.LoadAdmin(teamclient.AdminStatePath(path), time.Now())
	if err != nil {
		return adminCtx{}, err
	}
	if af.server != "" {
		base, err := teamclient.CheckURL(af.server)
		if err != nil {
			return adminCtx{}, err
		}
		if base != as.URL {
			return adminCtx{}, fmt.Errorf("the administrator credential here is for %s, not %s; run `tokenops team admin login --server %s`", as.URL, base, base)
		}
	}
	return adminCtx{client: teamclient.New(as.URL, as.Token), url: as.URL}, nil
}

// adminRun wraps a command body with the resolved administrator and a
// bounded context.
func adminRun(rf *rootFlags, af *adminFlags, fn func(ctx context.Context, cmd *cobra.Command, a adminCtx, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		a, err := resolveAdmin(rf, af)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		return fn(ctx, cmd, a, args)
	}
}

func newTeamAdminCmd(rf *rootFlags) *cobra.Command {
	af := &adminFlags{}
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administer your organisation: teams, invites, members, roles, grants, billing, audit",
		Long: `admin manages the organisation you own or administer, through the same API
as the web view. Every change is recorded in the organisation's audit log.

Sign in once with ` + "`tokenops team admin login`" + ` (a code you type in the browser);
the credential is kept in team-admin.json beside the enrolment (mode 0600)
and expires. ` + adminTokenEnv + ` with --server uses a token you were given instead.`,
	}
	cmd.PersistentFlags().StringVar(&af.server, "server", "", "the team server (default: the one this machine joined or signed in to)")
	cmd.AddCommand(newTeamAdminLoginCmd(rf, af), newTeamAdminLogoutCmd(rf),
		newTeamAdminRemoveCmd(rf, af), newTeamAdminRoleCmd(rf, af),
		newTeamAdminGrantCmd(rf, af), newTeamAdminRevokeCmd(rf, af))
	// The commands that read, or answer with what they created, print JSON
	// on request.
	for _, c := range []*cobra.Command{newTeamAdminTeamsCmd(rf, af), newTeamAdminCreateTeamCmd(rf, af),
		newTeamAdminInviteCmd(rf, af), newTeamAdminMembersCmd(rf, af), newTeamAdminGrantsCmd(rf, af),
		newTeamAdminBillingCmd(rf, af), newTeamAdminAuditCmd(rf, af)} {
		c.Flags().BoolVar(&af.json, "json", false, "emit JSON")
		cmd.AddCommand(c)
	}
	return cmd
}

func newTeamAdminLoginCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in as an owner or admin: approve this machine in the browser with a code",
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
			server := af.server
			if server == "" {
				if st, err := teamclient.Load(path); err == nil {
					server = st.URL
				}
			}
			if server == "" {
				return errors.New("pass --server with the team server's address (this machine joined none)")
			}
			base, err := teamclient.CheckURL(server)
			if err != nil {
				return err
			}
			res, err := linkInBrowser(cmd, base, teamwire.DeviceLinkRequest{Admin: true, DeviceName: defaultDeviceName()}, noBrowser)
			if err != nil {
				return err
			}
			if res.Admin == nil {
				return errors.New("approved, but the account is not an owner or admin of an organisation, so no administrator credential was issued")
			}
			if err := saveLinkResult(path, base, teamwire.DeviceLinkResult{Admin: res.Admin}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nSigned in to %s as %s until %s.\n", res.Admin.OrgName, res.Admin.Role,
				res.Admin.ExpiresAt.Local().Format("2006-01-02 15:04"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the address instead of opening a browser")
	return cmd
}

func newTeamAdminLogoutCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke and forget this machine's administrator credential",
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
			adminPath := teamclient.AdminStatePath(path)
			as, err := teamclient.LoadAdmin(adminPath, time.Now())
			if errors.Is(err, teamclient.ErrNoAdmin) {
				_ = teamclient.Remove(adminPath)
				fmt.Fprintln(cmd.OutOrStdout(), "No administrator credential on this machine.")
				return nil
			}
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			revokeErr := teamclient.New(as.URL, as.Token).RevokeSelf(ctx)
			if err := teamclient.Remove(adminPath); err != nil {
				return err
			}
			if revokeErr != nil {
				return fmt.Errorf("forgot the credential here, but the server could not revoke it (it expires %s): %w",
					as.ExpiresAt.Local().Format("2006-01-02"), revokeErr)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Signed out; the credential was revoked.")
			return nil
		},
	}
}

func newTeamAdminTeamsCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "teams",
		Short: "List the organisation's teams",
		Args:  cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			teams, err := a.client.Teams(ctx)
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, teams)
			}
			out := cmd.OutOrStdout()
			if len(teams) == 0 {
				fmt.Fprintln(out, "No teams yet: `tokenops team admin create-team <name>`.")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TEAM\tMEMBERS\tID")
			for _, t := range teams {
				fmt.Fprintf(w, "%s\t%d\t%s\n", t.Name, t.Members, t.ID)
			}
			return w.Flush()
		}),
	}
}

func newTeamAdminCreateTeamCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "create-team <name>",
		Short: "Create a team",
		Args:  cobra.ExactArgs(1),
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, args []string) error {
			t, err := a.client.CreateTeam(ctx, args[0])
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, t)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created team %s. Invite people with `tokenops team admin invite --team %q`.\n", t.Name, t.Name)
			return nil
		}),
	}
}

func newTeamAdminInviteCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var teamName, role, email string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "invite --team <team>",
		Short: "Create a single-use invite with a role, optionally e-mailed; prints the line the new member runs",
		Args:  cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			if teamName == "" {
				return errors.New("--team is required")
			}
			if !teamwire.ValidRole(role) {
				return fmt.Errorf("--role %q: want %s", role, strings.Join(teamwire.Roles, ", "))
			}
			if ttl < 0 || (ttl > 0 && ttl < time.Hour) {
				return errors.New("--ttl: at least 1h")
			}
			inv, err := a.client.Invite(ctx, teamwire.InviteRequest{Team: teamName, Role: role, TTLHours: int(ttl / time.Hour),
				Email: strings.TrimSpace(email)})
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, inv)
			}
			out := cmd.OutOrStdout()
			if inv.Emailed {
				fmt.Fprintf(out, "E-mailed the invite to %s.\n", strings.TrimSpace(email))
			}
			fmt.Fprintf(out, "Single-use invite as %s to team %s, valid until %s. On the new member's machine:\n\n  %s\n",
				role, teamName, inv.ExpiresAt.Local().Format("2006-01-02 15:04"), inv.Join)
			return nil
		}),
	}
	cmd.Flags().StringVar(&teamName, "team", "", "the team to join (name or ID)")
	cmd.Flags().StringVar(&role, "role", teamwire.RoleMember, "member, lead, admin or owner (never above your own)")
	cmd.Flags().StringVar(&email, "email", "", "e-mail the invite to this address (the line below still works)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "how long the invite is valid (default 7 days)")
	return cmd
}

func newTeamAdminMembersCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "members",
		Short: "List members, their roles and teams",
		Args:  cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			members, err := a.client.Members(ctx)
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, members)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tROLE\tTEAMS\tE-MAIL\tID")
			for _, m := range members {
				name := m.Name
				if m.IsYou {
					name += " (you)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, m.Role, strings.Join(m.Teams, ", "), m.Email, m.ID)
			}
			return w.Flush()
		}),
	}
}

// findMember resolves a member by ID, e-mail or exact name (ignoring case).
func findMember(ctx context.Context, c *teamclient.Client, ref string) (teamwire.Member, error) {
	members, err := c.Members(ctx)
	if err != nil {
		return teamwire.Member{}, err
	}
	var hits []teamwire.Member
	for _, m := range members {
		if m.ID == ref {
			return m, nil
		}
		if strings.EqualFold(m.Name, ref) || (m.Email != "" && strings.EqualFold(m.Email, ref)) {
			hits = append(hits, m)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return teamwire.Member{}, fmt.Errorf("no member %q (see `tokenops team admin members`)", ref)
	default:
		return teamwire.Member{}, fmt.Errorf("%d members are called %q; name one by ID (see `tokenops team admin members`)", len(hits), ref)
	}
}

func newTeamAdminRemoveCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <member>",
		Short: "Remove a member, revoke their machines and erase their figures",
		Args:  cobra.ExactArgs(1),
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, args []string) error {
			m, err := findMember(ctx, a.client, args[0])
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("removing %s (%s) erases their figures and cannot be undone; pass --yes to confirm", m.Name, m.ID)
			}
			if err := a.client.RemoveMember(ctx, m.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s; their machines were revoked and their figures erased.\n", m.Name)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the removal")
	return cmd
}

func newTeamAdminRoleCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "role <member> <member|lead|admin|owner>",
		Short: "Change a member's role",
		Args:  cobra.ExactArgs(2),
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, args []string) error {
			if !teamwire.ValidRole(args[1]) {
				return fmt.Errorf("role %q: want %s", args[1], strings.Join(teamwire.Roles, ", "))
			}
			m, err := findMember(ctx, a.client, args[0])
			if err != nil {
				return err
			}
			if err := a.client.SetRole(ctx, m.ID, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s.\n", m.Name, args[1])
			return nil
		}),
	}
}

func newTeamAdminGrantsCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "grants",
		Short: "List who may see individual figures, and why",
		Args:  cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			grants, err := a.client.Grants(ctx)
			if err != nil {
				return err
			}
			if !all {
				active := grants[:0]
				for _, g := range grants {
					if g.RevokedAt == nil {
						active = append(active, g)
					}
				}
				grants = active
			}
			if af.json {
				return writeControlJSON(cmd, grants)
			}
			out := cmd.OutOrStdout()
			if len(grants) == 0 {
				fmt.Fprintln(out, "No grants: nobody sees anyone's individual figures but their own.")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tGRANTEE\tSCOPE\tSINCE\tBY\tREASON")
			for _, g := range grants {
				since := g.GrantedAt.Local().Format("2006-01-02")
				if g.RevokedAt != nil {
					since += " (revoked " + g.RevokedAt.Local().Format("2006-01-02") + ")"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", g.ID, g.Grantee, g.Scope, since, g.GrantedBy, g.Reason)
			}
			return w.Flush()
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "include revoked grants")
	return cmd
}

func newTeamAdminGrantCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var teamName, reason string
	var everyone bool
	cmd := &cobra.Command{
		Use:   "grant <member> (--team <team> | --everyone) --reason <why>",
		Short: "Let a lead, admin or owner see individual figures; the members covered are told, with the reason",
		Args:  cobra.ExactArgs(1),
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, args []string) error {
			if (teamName == "") == !everyone {
				return errors.New("pass exactly one of --team <team> or --everyone")
			}
			if strings.TrimSpace(reason) == "" {
				return errors.New("--reason is required: the members covered read it")
			}
			m, err := findMember(ctx, a.client, args[0])
			if err != nil {
				return err
			}
			created, err := a.client.Grant(ctx, teamwire.GrantRequest{GranteeID: m.ID, Team: teamName, Reason: reason})
			if err != nil {
				return err
			}
			scope := "everyone"
			if teamName != "" {
				scope = "team " + teamName
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Granted %s to see the individual figures of %s (grant %s). Each member covered sees it on their page.\n",
				m.Name, scope, created.ID)
			return nil
		}),
	}
	cmd.Flags().StringVar(&teamName, "team", "", "the team whose members' figures may be seen")
	cmd.Flags().BoolVar(&everyone, "everyone", false, "every member of the organisation")
	cmd.Flags().StringVar(&reason, "reason", "", "why; the members covered read it")
	return cmd
}

func newTeamAdminRevokeCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <grant-id>",
		Short: "Revoke a grant",
		Args:  cobra.ExactArgs(1),
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, args []string) error {
			if err := a.client.RevokeGrant(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Revoked.")
			return nil
		}),
	}
}

func newTeamAdminBillingCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var checkout, portal, noBrowser bool
	cmd := &cobra.Command{
		Use:   "billing",
		Short: "Trial and subscription; --checkout to subscribe, --portal for invoices and payment details",
		Args:  cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			if checkout && portal {
				return errors.New("pass one of --checkout or --portal")
			}
			out := cmd.OutOrStdout()
			if checkout || portal {
				var link teamwire.BillingLink
				var err error
				if checkout {
					link, err = a.client.BillingCheckout(ctx)
				} else {
					link, err = a.client.BillingPortal(ctx)
				}
				if err != nil {
					return err
				}
				target, err := billingLink(a.url, link.URL)
				if err != nil {
					return err
				}
				fmt.Fprintln(out, target)
				if !noBrowser {
					if err := openBrowser(target); err != nil {
						fmt.Fprintln(out, "(could not open a browser; open the address yourself)")
					}
				}
				return nil
			}
			b, err := a.client.Billing(ctx)
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, b)
			}
			writePlan(out, &b.Plan, time.Now())
			fmt.Fprintf(out, "Seats: %d (one per member; the subscription follows members joining and leaving).\n", b.Seats)
			if b.CurrentPeriodEnd != nil {
				fmt.Fprintf(out, "Current period ends %s.\n", b.CurrentPeriodEnd.Local().Format("2006-01-02"))
			}
			if b.CancelAt != nil {
				fmt.Fprintf(out, "The subscription ends on %s.\n", b.CancelAt.Local().Format("2006-01-02"))
			}
			if b.Status == teamwire.PlanActive || b.Status == teamwire.PlanPastDue {
				fmt.Fprintln(out, "Invoices and payment details: `tokenops team admin billing --portal`.")
			} else {
				fmt.Fprintln(out, "Subscribe: `tokenops team admin billing --checkout`.")
			}
			if b.ManageURL != "" {
				fmt.Fprintf(out, "In the web view: %s\n", b.ManageURL)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&checkout, "checkout", false, "open the checkout to subscribe")
	cmd.Flags().BoolVar(&portal, "portal", false, "open the customer portal (invoices, payment details, cancel)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the link instead of opening a browser")
	return cmd
}

// billingLink accepts a billing link only on the team server itself or on
// the payment provider's own https hosts (Paddle's checkout and customer
// portal), so a server cannot make this machine open another site.
func billingLink(base, raw string) (string, error) {
	if u, err := sameServer(base, raw); err == nil {
		return u, nil
	}
	u, err := url.Parse(raw)
	if err == nil && u.Scheme == "https" && u.User == nil &&
		(u.Hostname() == "paddle.com" || strings.HasSuffix(u.Hostname(), ".paddle.com")) {
		return u.String(), nil
	}
	return "", fmt.Errorf("the server sent a billing link to another site (%q); not opened", raw)
}

func newTeamAdminAuditCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "The organisation's audit log, newest first",
		Args:  cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			entries, err := a.client.Audit(ctx)
			if err != nil {
				return err
			}
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}
			if af.json {
				return writeControlJSON(cmd, entries)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "WHEN\tWHO\tACTION\tSUBJECT\tDETAIL")
			for _, e := range entries {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.At.Local().Format("2006-01-02 15:04"), e.Actor, e.Action, e.Subject, e.Detail)
			}
			return w.Flush()
		}),
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "show at most this many entries (0: all the server returns)")
	return cmd
}
