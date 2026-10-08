# TokenOps team plane on a Hetzner VPS

One small VPS in Germany runs the whole team plane (ADR 0012) with Docker
Compose: Caddy terminates TLS, `tokenops-team` serves the API and the web
view, Postgres keeps the figures, and a backup container dumps the
database nightly. No systemd units: Docker's restart policy keeps the
services up across reboots.

What the server receives is derived figures only — counts, durations,
tokens and costs per UTC day, repository label and kind of work. Prompts,
file contents, transcripts, paths, commit messages and model outputs never
leave members' machines; the upload format has no field that could hold
them (`pkg/teamwire`).

## 1. What you create

1. A Hetzner Cloud project and a server in **Falkenstein (fsn1) or
   Nuremberg (nbg1)**: CX22 (2 vCPU, 4 GB) is ample for hundreds of members.
   Image: Ubuntu 24.04. Add your SSH key; enable backups if you want
   Hetzner's snapshots on top of the database dumps below.
2. A Hetzner firewall allowing inbound **22/tcp** (from your address if you
   can), **80/tcp**, **443/tcp** and **443/udp** only.
3. DNS: an **A** record (and **AAAA** if you use IPv6) for the team domain,
   e.g. `team.example.eu`, pointing at the server.
4. A data-processing agreement with Hetzner (Hetzner Console → Settings →
   DPA) — the figures are personal data once a member is named.

## 2. Install

```bash
ssh root@team.example.eu
apt-get update && apt-get -y upgrade
curl -fsSL https://get.docker.com | sh        # Docker Engine + compose plugin
adduser --disabled-password tokenops && usermod -aG docker tokenops
su - tokenops

git clone https://github.com/klarlabs-studio/tokenops.git
cd tokenops && git checkout vX.Y.Z             # the release to run
cd deploy/team
cp .env.example .env && chmod 600 .env
$EDITOR .env                                   # TEAM_DOMAIN, ACME_EMAIL, POSTGRES_PASSWORD, TEAM_VERSION
docker compose up -d --build
docker compose ps                              # caddy, team, db, backup: running
curl -fsS https://team.example.eu/healthz      # ok
```

Caddy obtains the certificate on first request; ports 80 and 443 must be
reachable and the DNS record in place.

## 3. Create the organisation and invite people

The operator console is the same binary, run inside the container:

```bash
alias team='docker compose exec team tokenops-team'
team create-org --name "Acme" --owner "Your Name"   # prints the owner's API token and a sign-in link
team create-team --org Acme --name platform
team invite --org Acme --team platform              # prints: tokenops team join https://… tot_inv_…
```

Send each person their own invite (single-use, seven days by default). On
their machine they run the printed `tokenops team join …` line; their
daemon starts uploading within the hour. They can check what is sent with
`tokenops team preview` at any time.

Individual figures are **not** visible to anyone but the member until an
owner grants it, with a reason the member reads:

```bash
team set-role --org Acme --member "Lars" --role lead
team grant --org Acme --to "Lars" --team platform --reason "1:1 coaching, agreed with the works council"
team members --org Acme                              # teams, members, grants
team revoke-grant --org Acme --id <grant-id>
```

Each member sees, on their page in the web view and in `tokenops team
status`, who may see their figures, why, and every view. Owners and
admins read the audit log at `/audit`.

Privacy settings per organisation:

```bash
team privacy --org Acme --min-group 3 --retention-days 400
```

`--min-group` withholds any team, repository or kind-of-work row fewer
people contributed to (default 3). Figures older than `--retention-days`
are deleted every six hours (default 400); the audit log is kept
`AUDIT_RETENTION_DAYS` (default 730).

To remove someone and erase their figures: `team remove-member --org Acme
--member "Name"`. A member leaving from their own machine
(`tokenops team leave`) erases that machine's figures unless they pass
`--keep-history`.

## 4. Backups

The `backup` service writes `pg_dump` custom-format dumps to
`./backups/tokenops-<UTC timestamp>.dump` every 24 hours and deletes those
older than `BACKUP_KEEP_DAYS`.

```bash
docker compose exec backup sh /usr/local/bin/backup.sh once   # a dump now
ls -lh backups/
```

Keep a copy off the server, e.g. on a Hetzner Storage Box in the same
region (`rsync -a backups/ uXXXX@uXXXX.your-storagebox.de:tokenops/` from
a cron job or by hand). Dumps hold the same derived figures and names as
the database; treat them as personal data.

Restore into a fresh stack:

```bash
docker compose up -d db
docker compose exec -T db pg_restore -U tokenops -d tokenops --clean --if-exists < backups/tokenops-YYYYMMDDTHHMMSSZ.dump
docker compose up -d
```

## 5. Upgrade

```bash
cd ~/tokenops && git fetch --tags && git checkout vX.Y.Z
cd deploy/team
docker compose exec backup sh /usr/local/bin/backup.sh once   # dump first
sed -i 's/^TEAM_VERSION=.*/TEAM_VERSION=vX.Y.Z/' .env
docker compose up -d --build team
docker compose logs --tail=50 team     # "migrations applied" when the schema moved
```

`serve` applies pending migrations at start under an advisory lock;
`docker compose exec team tokenops-team migrate` does it by hand. To roll
back, check out the previous tag and rebuild; if a migration ran,
restore the dump taken before the upgrade.

Keep the host patched: `apt-get upgrade` monthly, and `docker compose pull
caddy db backup && docker compose up -d` for the base images.

## 6. Operating notes

- Logs: `docker compose logs -f team` — JSON, one line per request with
  method, path, status and duration. Query strings, headers and tokens are
  never logged; Caddy's access log is off for the same reason.
- Health: `/healthz` (process up) and `/readyz` (database reachable).
- Rate limits: enrolment and sign-in 10 per address then one every
  6 seconds; uploads 6 per device then one a minute.
- Tokens: device, invite, sign-in, session and admin tokens are 256-bit
  random values stored only as SHA-256. A leaked database holds no usable
  credential. Lost an owner API token? `team admin-token --org Acme
  --member "Your Name"` mints another.
- Sign in as an owner without a joined machine: `team login-link --org
  Acme --member "Your Name"`.
