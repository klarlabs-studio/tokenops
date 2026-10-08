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
$EDITOR .env                                   # TEAM_DOMAIN, ACME_EMAIL, POSTGRES_PASSWORD, TEAM_VERSION=X.Y.Z
docker compose pull
docker compose up -d
docker compose ps                              # caddy, team, db, backup: running
curl -fsS https://team.example.eu/healthz      # ok
```

The checkout supplies the Compose file, Caddyfile and backup script for
that release; the server itself is the published image
`ghcr.io/klarlabs-studio/tokenops-team:X.Y.Z` (linux/amd64 and
linux/arm64), which runs the `tokenops-team` binary from the release's
archives byte for byte. `TEAM_VERSION` is the release without its leading
`v`. The same binary is attached to every release as
`tokenops-team_X.Y.Z_linux_<arch>.tar.gz`, listed in its `checksums.txt`,
for running without Docker.

Caddy obtains the certificate on first request; ports 80 and 443 must be
reachable and the DNS record in place.

**Building from source instead** (a fork, a patch, a commit between
releases): add the build override, which builds `deploy/team/Dockerfile`
from this checkout and tags it locally as `tokenops-team:$TEAM_VERSION`:

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

Use the same `-f … -f …` pair for every later `docker compose` command, or
`export COMPOSE_FILE=docker-compose.yml:docker-compose.build.yml` once.

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
cd ~/tokenops && git fetch --tags && git checkout vX.Y.Z    # Compose file, Caddyfile, backup script
cd deploy/team
docker compose exec backup sh /usr/local/bin/backup.sh once   # dump first
sed -i 's/^TEAM_VERSION=.*/TEAM_VERSION=X.Y.Z/' .env         # no leading v
docker compose pull team
docker compose up -d                   # recreates what changed
docker compose logs --tail=50 team     # "migrations applied" when the schema moved
docker compose exec team tokenops-team --version
```

Read the release's CHANGELOG entry first: it names new settings (for
example `TEAMSERVER_*` variables or a `.env` line) and anything to do
before or after.

`serve` applies pending migrations at start under an advisory lock;
`docker compose exec team tokenops-team migrate` does it by hand. To roll
back, check out the previous tag, set `TEAM_VERSION` back and run
`docker compose up -d`; if a migration ran, restore the dump taken before
the upgrade (§4) first, because an older server does not undo a newer
schema.

Built from source? `git checkout vX.Y.Z`, set `TEAM_VERSION`, then
`docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.

Keep the host patched: `apt-get upgrade` monthly, and `docker compose pull
caddy db backup && docker compose up -d` for the other images.

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
