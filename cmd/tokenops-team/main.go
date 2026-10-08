// Command tokenops-team is the TokenOps team plane (ADR 0012): the hosted
// service joined machines upload derived figures to, and its operator's
// console. It runs behind Caddy on one VPS with Postgres; see
// deploy/team/README.md.
//
// Configuration is by environment:
//
//	TEAMSERVER_DATABASE_URL          postgres://user:pass@host:5432/db (required)
//	TEAMSERVER_PUBLIC_URL            https://team.example.eu (required for serve)
//	TEAMSERVER_LISTEN                listen address, default :8080
//	TEAMSERVER_TRUSTED_PROXIES       CIDRs whose X-Forwarded-For is believed,
//	                                 default loopback and private ranges
//	TEAMSERVER_AUDIT_RETENTION_DAYS  default 730
//
// Single sign-on is configured per organisation with `tokenops-team sso
// set`; its client secret stays in an environment variable or a file the
// server reads (see deploy/team/README.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
	"go.klarlabs.de/tokenops/internal/version"
)

func main() {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "tokenops-team:", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func open(ctx context.Context) (*pgstore.Store, error) {
	dsn := env("TEAMSERVER_DATABASE_URL", "")
	if dsn == "" {
		return nil, errors.New("TEAMSERVER_DATABASE_URL is not set")
	}
	return pgstore.Open(ctx, dsn)
}

// withStore runs fn against a migrated store.
func withStore(fn func(ctx context.Context, s *pgstore.Store) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := open(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if _, err := s.Migrate(ctx); err != nil {
		return err
	}
	return fn(ctx, s)
}

func publicURL() string { return strings.TrimRight(env("TEAMSERVER_PUBLIC_URL", ""), "/") }

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "tokenops-team",
		Short:         "The TokenOps team plane: derived figures from joined machines, aggregated by team, repository and kind of work",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(serveCmd(), migrateCmd(), createOrgCmd(), createTeamCmd(), inviteCmd(), grantCmd(),
		revokeGrantCmd(), loginLinkCmd(), adminTokenCmd(), setRoleCmd(), removeMemberCmd(), membersCmd(),
		privacyCmd(), purgeCmd(), ssoCmd(), setEmailCmd())
	return cmd
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Migrate the database and serve the API and web view",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			proxies, err := parsePrefixes(env("TEAMSERVER_TRUSTED_PROXIES", "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"))
			if err != nil {
				return err
			}
			auditDays, err := strconv.Atoi(env("TEAMSERVER_AUDIT_RETENTION_DAYS", strconv.Itoa(team.DefaultAuditRetentionDays)))
			if err != nil {
				return fmt.Errorf("TEAMSERVER_AUDIT_RETENTION_DAYS: %w", err)
			}
			openCtx, cancel := context.WithTimeout(ctx, time.Minute)
			store, err := open(openCtx)
			if err == nil {
				var applied []int
				applied, err = store.Migrate(openCtx)
				if len(applied) > 0 {
					logger.Info("migrations applied", "versions", applied)
				}
			}
			cancel()
			if err != nil {
				return err
			}
			defer store.Close()
			srv, err := teamserver.New(store, teamserver.Config{PublicURL: publicURL(), TrustedProxies: proxies,
				AuditRetentionDays: auditDays, Logger: logger})
			if err != nil {
				return err
			}
			go srv.Maintain(ctx, teamserver.MaintainEvery)
			hs := &http.Server{
				Addr: env("TEAMSERVER_LISTEN", ":8080"), Handler: srv.Handler(),
				ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
				WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10,
			}
			errc := make(chan error, 1)
			go func() { errc <- hs.ListenAndServe() }()
			logger.Info("tokenops-team serving", "listen", hs.Addr, "public_url", publicURL(), "version", version.Version)
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			logger.Info("shutting down")
			shutCtx, cancelShut := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancelShut()
			return hs.Shutdown(shutCtx)
		},
	}
}

func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("TEAMSERVER_TRUSTED_PROXIES: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use: "migrate", Short: "Apply pending database migrations", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			s, err := open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			applied, err := s.Migrate(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("applied %d migration(s) %v\n", len(applied), applied)
			return nil
		},
	}
}

func createOrgCmd() *cobra.Command {
	var name, owner string
	cmd := &cobra.Command{
		Use: "create-org", Short: "Create an organisation and its owner; prints the owner's API token and a sign-in link", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				org, p, admin, login, err := s.CreateOrg(ctx, name, owner)
				if err != nil {
					return err
				}
				fmt.Printf("Organisation %q created (%s), owner %s.\n\n", org.Name, org.ID, p.DisplayName)
				fmt.Printf("Owner API token (shown once; the server keeps only its hash):\n  %s\n\n", admin)
				fmt.Printf("Sign in within 10 minutes:\n  %s/login?t=%s\n", publicURL(), login)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "organisation name")
	cmd.Flags().StringVar(&owner, "owner", "", "the owner's display name")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("owner")
	return cmd
}

// orgFlag adds --org and resolves it.
func orgFlag(cmd *cobra.Command, org *string) {
	cmd.Flags().StringVar(org, "org", "", "organisation name")
	_ = cmd.MarkFlagRequired("org")
}

func createTeamCmd() *cobra.Command {
	var org, name string
	cmd := &cobra.Command{
		Use: "create-team", Short: "Create a team", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				t, err := s.CreateTeam(ctx, pgstore.Console(o.ID), name)
				if err != nil {
					return err
				}
				fmt.Printf("Team %q created (%s).\n", t.Name, t.ID)
				return nil
			})
		},
	}
	orgFlag(cmd, &org)
	cmd.Flags().StringVar(&name, "name", "", "team name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func inviteCmd() *cobra.Command {
	var org, teamName, role string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use: "invite", Short: "Mint a single-use invite to a team", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			r, err := team.ParseRole(role)
			if err != nil {
				return err
			}
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				tok, expires, err := s.CreateInvite(ctx, pgstore.Console(o.ID), teamName, r, ttl)
				if err != nil {
					return err
				}
				fmt.Printf("Single-use invite to %s as %s, valid until %s. The member runs:\n\n  tokenops team join %s %s\n",
					teamName, r, expires.UTC().Format(time.RFC3339), publicURL(), tok)
				return nil
			})
		},
	}
	orgFlag(cmd, &org)
	cmd.Flags().StringVar(&teamName, "team", "", "team name")
	cmd.Flags().StringVar(&role, "role", "member", "member, lead, admin or owner")
	cmd.Flags().DurationVar(&ttl, "ttl", team.InviteTTL, "how long the invite stays valid (at most 30 days)")
	_ = cmd.MarkFlagRequired("team")
	return cmd
}

func grantCmd() *cobra.Command {
	var org, to, teamName, reason string
	cmd := &cobra.Command{
		Use:   "grant",
		Short: "Let a lead, admin or owner see individual figures; the members covered are told",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				m, err := s.FindMember(ctx, o.ID, to)
				if err != nil {
					return fmt.Errorf("member %q: %w", to, err)
				}
				id, err := s.CreateGrant(ctx, pgstore.Console(o.ID), m.ID, teamName, reason)
				if err != nil {
					return err
				}
				scope := "everyone"
				if teamName != "" {
					scope = "team " + teamName
				}
				fmt.Printf("Grant %s: %s may see the individual figures of %s. Each member covered sees it, and every view, on their page.\n", id, m.DisplayName, scope)
				return nil
			})
		},
	}
	orgFlag(cmd, &org)
	cmd.Flags().StringVar(&to, "to", "", "grantee: member ID or display name")
	cmd.Flags().StringVar(&teamName, "team", "", "limit to one team (default: everyone)")
	cmd.Flags().StringVar(&reason, "reason", "", "why; the members covered read it")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func revokeGrantCmd() *cobra.Command {
	var org, id string
	cmd := &cobra.Command{
		Use: "revoke-grant", Short: "End a grant", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				if err := s.RevokeGrant(ctx, pgstore.Console(o.ID), id); err != nil {
					return err
				}
				fmt.Println("Grant revoked.")
				return nil
			})
		},
	}
	orgFlag(cmd, &org)
	cmd.Flags().StringVar(&id, "id", "", "grant ID")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

// memberCmd builds a command acting on one member.
func memberCmd(use, short string, extra func(*cobra.Command), run func(ctx context.Context, s *pgstore.Store, o pgstore.Org, m pgstore.MemberInfo) error) *cobra.Command {
	var org, member string
	cmd := &cobra.Command{
		Use: use, Short: short, Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				m, err := s.FindMember(ctx, o.ID, member)
				if err != nil {
					return fmt.Errorf("member %q: %w", member, err)
				}
				return run(ctx, s, o, m)
			})
		},
	}
	orgFlag(cmd, &org)
	cmd.Flags().StringVar(&member, "member", "", "member ID or display name")
	_ = cmd.MarkFlagRequired("member")
	if extra != nil {
		extra(cmd)
	}
	return cmd
}

func loginLinkCmd() *cobra.Command {
	return memberCmd("login-link", "Print a single-use sign-in link for a member", nil,
		func(ctx context.Context, s *pgstore.Store, _ pgstore.Org, m pgstore.MemberInfo) error {
			tok, expires, err := s.MintLogin(ctx, m.ID)
			if err != nil {
				return err
			}
			fmt.Printf("%s/login?t=%s\n(valid once, until %s)\n", publicURL(), tok, expires.UTC().Format(time.RFC3339))
			return nil
		})
}

func adminTokenCmd() *cobra.Command {
	return memberCmd("admin-token", "Mint an API token for an owner or admin", nil,
		func(ctx context.Context, s *pgstore.Store, _ pgstore.Org, m pgstore.MemberInfo) error {
			p, err := s.Member(ctx, m.ID)
			if err != nil {
				return err
			}
			tok, err := s.MintAdminToken(ctx, p)
			if err != nil {
				return err
			}
			fmt.Printf("API token for %s (shown once):\n  %s\n", p.DisplayName, tok)
			return nil
		})
}

func setRoleCmd() *cobra.Command {
	var role string
	return memberCmd("set-role", "Change a member's role", func(c *cobra.Command) {
		c.Flags().StringVar(&role, "role", "", "member, lead, admin or owner")
		_ = c.MarkFlagRequired("role")
	}, func(ctx context.Context, s *pgstore.Store, o pgstore.Org, m pgstore.MemberInfo) error {
		r, err := team.ParseRole(role)
		if err != nil {
			return err
		}
		if err := s.SetRole(ctx, pgstore.Console(o.ID), m.ID, r); err != nil {
			return err
		}
		fmt.Printf("%s is now %s.\n", m.DisplayName, r)
		return nil
	})
}

func removeMemberCmd() *cobra.Command {
	return memberCmd("remove-member", "Remove a member and erase their figures, devices and grants", nil,
		func(ctx context.Context, s *pgstore.Store, o pgstore.Org, m pgstore.MemberInfo) error {
			if err := s.RemoveMember(ctx, pgstore.Console(o.ID), m.ID); err != nil {
				return err
			}
			fmt.Printf("%s removed; their figures are erased.\n", m.DisplayName)
			return nil
		})
}

func membersCmd() *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use: "members", Short: "List teams, members and grants", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				fmt.Fprintf(w, "%s: groups under %d people withheld, figures kept %d days\n\n", o.Name, o.MinGroupSize, o.RetentionDays)
				teams, err := s.Teams(ctx, o.ID)
				if err != nil {
					return err
				}
				fmt.Fprintln(w, "TEAM\tMEMBERS\tID")
				for _, t := range teams {
					fmt.Fprintf(w, "%s\t%d\t%s\n", t.Name, t.Members, t.ID)
				}
				members, err := s.Members(ctx, o.ID)
				if err != nil {
					return err
				}
				fmt.Fprintln(w, "\nMEMBER\tROLE\tTEAMS\tSSO E-MAIL\tID")
				for _, m := range members {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.DisplayName, m.Role, strings.Join(m.TeamNames, ","), m.Email, m.ID)
				}
				grants, err := s.Grants(ctx, o.ID)
				if err != nil {
					return err
				}
				fmt.Fprintln(w, "\nGRANT\tGRANTEE\tSCOPE\tSTATUS\tREASON")
				for _, g := range grants {
					scope, status := "everyone", "active"
					if g.TeamName != "" {
						scope = "team " + g.TeamName
					}
					if g.RevokedAt != nil {
						status = "revoked"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", g.ID, g.GranteeName, scope, status, g.Reason)
				}
				return w.Flush()
			})
		},
	}
	orgFlag(cmd, &org)
	return cmd
}

func privacyCmd() *cobra.Command {
	var org string
	var minGroup, retention int
	cmd := &cobra.Command{
		Use: "privacy", Short: "Set the minimum group size and how long figures are kept", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if minGroup < 1 || minGroup > 50 || retention < team.MinRetentionDays || retention > team.MaxRetentionDays {
				return fmt.Errorf("--min-group 1..50, --retention-days %d..%d", team.MinRetentionDays, team.MaxRetentionDays)
			}
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				if err := s.SetPrivacy(ctx, pgstore.Console(o.ID), minGroup, retention); err != nil {
					return err
				}
				fmt.Printf("%s: groups under %d people withheld; figures kept %d days.\n", o.Name, minGroup, retention)
				return nil
			})
		},
	}
	orgFlag(cmd, &org)
	cmd.Flags().IntVar(&minGroup, "min-group", team.DefaultMinGroupSize, "smallest group an aggregate is shown for")
	cmd.Flags().IntVar(&retention, "retention-days", team.DefaultRetentionDays, "days figures are kept")
	return cmd
}

func purgeCmd() *cobra.Command {
	return &cobra.Command{
		Use: "purge", Short: "Apply retention now (serve does it every six hours)", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			days, _ := strconv.Atoi(env("TEAMSERVER_AUDIT_RETENTION_DAYS", strconv.Itoa(team.DefaultAuditRetentionDays)))
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				r, err := s.Purge(ctx, days)
				if err != nil {
					return err
				}
				fmt.Printf("deleted: %d figure rows, %d audit entries, %d credentials, %d invites, %d upload receipts\n",
					r.Buckets, r.Audit, r.Tokens, r.Invites, r.Batches)
				return nil
			})
		},
	}
}

func ssoCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "sso", Short: "Single sign-on (OpenID Connect) for an organisation's web view"}
	var org, issuer, clientID, secretEnv, secretFile, domains string
	var allowUnverified, skipCheck bool
	set := &cobra.Command{
		Use:   "set",
		Short: "Turn single sign-on on, or change it; checks the issuer and the secret first",
		Long: `Turn single sign-on on for an organisation, or change it.

Register a web application ("confidential client") at the identity
provider with the redirect URI <TEAMSERVER_PUBLIC_URL>/sso/callback and the
scopes openid, email and profile. The client secret is never stored: name
the environment variable (--client-secret-env) or file (--client-secret-file)
the server reads it from, and give the server that variable or file.

A verified e-mail address in an allowed domain signs in the existing member
it is set on (set-email); nobody is ever created.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			c := team.SSO{Issuer: issuer, ClientID: clientID, AllowUnverifiedEmail: allowUnverified}
			switch {
			case secretEnv != "" && secretFile == "":
				c.SecretRef = "env:" + secretEnv
			case secretFile != "" && secretEnv == "":
				c.SecretRef = "file:" + secretFile
			default:
				return errors.New("give exactly one of --client-secret-env and --client-secret-file")
			}
			for _, d := range strings.Split(domains, ",") {
				if d = strings.TrimSpace(d); d != "" {
					c.Domains = append(c.Domains, d)
				}
			}
			if err := c.Validate(); err != nil {
				return err
			}
			if !skipCheck {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				err := teamserver.CheckSSO(ctx, c, nil)
				cancel()
				if err != nil {
					return fmt.Errorf("%w (run where the server runs, with its secret, or pass --skip-check)", err)
				}
			}
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, org)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", org, err)
				}
				if err := s.SetSSO(ctx, pgstore.Console(o.ID), c); err != nil {
					return err
				}
				fmt.Printf("Single sign-on for %s: %s, client %s, domains %s.\n", o.Name, c.Issuer, c.ClientID, strings.Join(c.Domains, ", "))
				fmt.Printf("Redirect URI to register: %s/sso/callback\nSign-in page: %s/sso?org=%s\n", publicURL(), publicURL(), url.QueryEscape(o.Name))
				fmt.Println("Members sign in once their e-mail is set: tokenops-team set-email --org … --member … --email …")
				return nil
			})
		},
	}
	orgFlag(set, &org)
	set.Flags().StringVar(&issuer, "issuer", "", "issuer URL, e.g. https://accounts.google.com, https://login.microsoftonline.com/<tenant>/v2.0, https://keycloak.example.eu/realms/acme")
	set.Flags().StringVar(&clientID, "client-id", "", "the client ID registered at the issuer")
	set.Flags().StringVar(&secretEnv, "client-secret-env", "", "environment variable holding the client secret")
	set.Flags().StringVar(&secretFile, "client-secret-file", "", "file holding the client secret (absolute path)")
	set.Flags().StringVar(&domains, "domains", "", "comma-separated e-mail domains that may sign in, e.g. example.com,example.de")
	set.Flags().BoolVar(&allowUnverified, "allow-unverified-email", false,
		"accept ID tokens without email_verified=true: only for an issuer that owns its users' addresses, such as one Microsoft Entra tenant")
	set.Flags().BoolVar(&skipCheck, "skip-check", false, "store without fetching the issuer's discovery document or reading the secret")
	for _, f := range []string{"issuer", "client-id", "domains"} {
		_ = set.MarkFlagRequired(f)
	}

	var showOrg string
	show := &cobra.Command{
		Use: "show", Short: "Show an organisation's single sign-on", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, showOrg)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", showOrg, err)
				}
				c, err := s.SSOConfig(ctx, o.ID)
				if errors.Is(err, pgstore.ErrNotFound) {
					fmt.Printf("%s: single sign-on is off.\n", o.Name)
					return nil
				}
				if err != nil {
					return err
				}
				fmt.Printf("%s: issuer %s, client %s, secret from %s, domains %s, unverified e-mail %s.\n", o.Name, c.Issuer,
					c.ClientID, c.SecretRef, strings.Join(c.Domains, ", "), map[bool]string{true: "accepted", false: "refused"}[c.AllowUnverifiedEmail])
				return nil
			})
		},
	}
	orgFlag(show, &showOrg)

	var offOrg string
	disable := &cobra.Command{
		Use: "disable", Short: "Turn single sign-on off; sign-in links keep working", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withStore(func(ctx context.Context, s *pgstore.Store) error {
				o, err := s.OrgByName(ctx, offOrg)
				if err != nil {
					return fmt.Errorf("organisation %q: %w", offOrg, err)
				}
				if err := s.DisableSSO(ctx, pgstore.Console(o.ID)); err != nil {
					return err
				}
				fmt.Printf("%s: single sign-on is off.\n", o.Name)
				return nil
			})
		},
	}
	orgFlag(disable, &offOrg)
	cmd.AddCommand(set, show, disable)
	return cmd
}

func setEmailCmd() *cobra.Command {
	var email string
	return memberCmd("set-email", "Set the e-mail address single sign-on signs a member in by (empty clears it)", func(c *cobra.Command) {
		c.Flags().StringVar(&email, "email", "", "the member's address at the identity provider")
		_ = c.MarkFlagRequired("email")
	}, func(ctx context.Context, s *pgstore.Store, o pgstore.Org, m pgstore.MemberInfo) error {
		if err := s.SetEmail(ctx, pgstore.Console(o.ID), m.ID, email); err != nil {
			return err
		}
		if email == "" {
			fmt.Printf("%s has no single sign-on address.\n", m.DisplayName)
			return nil
		}
		fmt.Printf("%s signs in with single sign-on as %s. Their page shows it.\n", m.DisplayName, strings.ToLower(strings.TrimSpace(email)))
		return nil
	})
}
