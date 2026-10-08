-- The team plane's schema (ADR 0012). Every credential is stored as its
-- SHA-256; no column holds a prompt, a path, a transcript or a commit
-- message, because the upload that fills metric_buckets has no field that
-- could carry one (pkg/teamwire).

CREATE TABLE orgs (
    id             uuid PRIMARY KEY,
    name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    min_group_size int  NOT NULL DEFAULT 3 CHECK (min_group_size BETWEEN 1 AND 50),
    retention_days int  NOT NULL DEFAULT 400 CHECK (retention_days BETWEEN 30 AND 3650),
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE teams (
    id         uuid PRIMARY KEY,
    org_id     uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE members (
    id           uuid PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 100),
    role         text NOT NULL CHECK (role IN ('member', 'lead', 'admin', 'owner')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    removed_at   timestamptz
);
CREATE INDEX members_org ON members (org_id);

CREATE TABLE team_members (
    team_id   uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    member_id uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    PRIMARY KEY (team_id, member_id)
);
CREATE INDEX team_members_member ON team_members (member_id);

CREATE TABLE invites (
    id         uuid PRIMARY KEY,
    org_id     uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    team_id    uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('member', 'lead', 'admin', 'owner')),
    token_hash bytea NOT NULL UNIQUE,
    created_by uuid REFERENCES members (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    used_by    uuid REFERENCES members (id) ON DELETE SET NULL
);

CREATE TABLE devices (
    id           uuid PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    member_id    uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX devices_member ON devices (member_id);

-- Browser sessions, single-use login links and admin API tokens.
CREATE TABLE tokens (
    token_hash bytea PRIMARY KEY,
    member_id  uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('session', 'login', 'admin')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    used_at    timestamptz,
    revoked_at timestamptz
);
CREATE INDEX tokens_expiry ON tokens (expires_at);

-- A grant lets one member see the individual figures of a team's members
-- (team_id) or of everyone (team_id NULL). Members see every grant that
-- covers them.
CREATE TABLE grants (
    id         uuid PRIMARY KEY,
    org_id     uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    grantee_id uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    team_id    uuid REFERENCES teams (id) ON DELETE CASCADE,
    reason     text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    granted_by uuid REFERENCES members (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    revoked_by uuid REFERENCES members (id) ON DELETE SET NULL
);
CREATE INDEX grants_org ON grants (org_id);

-- The derived figures: one row per device, UTC day, repository label and
-- kind of work. The CHECKs repeat pkg/teamwire's validation, so even a
-- writer that bypassed it could not store free text here.
CREATE TABLE metric_buckets (
    device_id          uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    member_id          uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    org_id             uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    day                date NOT NULL,
    repo               text NOT NULL CHECK (repo ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9][A-Za-z0-9._-]{0,63})?$'),
    kind               text NOT NULL CHECK (kind IN ('unknown', 'lookup', 'research', 'edit', 'deep')),
    sessions           bigint NOT NULL DEFAULT 0 CHECK (sessions >= 0),
    instructions       bigint NOT NULL DEFAULT 0 CHECK (instructions >= 0),
    turns              bigint NOT NULL DEFAULT 0 CHECK (turns >= 0),
    tool_calls         bigint NOT NULL DEFAULT 0 CHECK (tool_calls >= 0),
    active_seconds     double precision NOT NULL DEFAULT 0 CHECK (active_seconds >= 0),
    first_try          bigint NOT NULL DEFAULT 0 CHECK (first_try >= 0),
    reworked           bigint NOT NULL DEFAULT 0 CHECK (reworked >= 0),
    interrupted        bigint NOT NULL DEFAULT 0 CHECK (interrupted >= 0),
    escalated          bigint NOT NULL DEFAULT 0 CHECK (escalated >= 0),
    rejected           bigint NOT NULL DEFAULT 0 CHECK (rejected >= 0),
    tokens             bigint NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    cost_usd           double precision NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
    api_equivalent_usd double precision NOT NULL DEFAULT 0 CHECK (api_equivalent_usd >= 0),
    unpriced_turns     bigint NOT NULL DEFAULT 0 CHECK (unpriced_turns >= 0),
    PRIMARY KEY (device_id, day, repo, kind)
);
CREATE INDEX metric_buckets_org_day ON metric_buckets (org_id, day);
CREATE INDEX metric_buckets_member_day ON metric_buckets (member_id, day);

-- When each device's figures for a day were computed: an upload older than
-- what is held for a day does not replace it.
CREATE TABLE device_days (
    device_id   uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    day         date NOT NULL,
    computed_at timestamptz NOT NULL,
    PRIMARY KEY (device_id, day)
);

-- Upload batches seen, so a replayed batch is acknowledged, not applied.
CREATE TABLE ingest_batches (
    device_id   uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    batch_id    uuid NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, batch_id)
);

-- Who did what, and who viewed whom. Names are copied in so the record
-- outlives a removed member; no foreign keys for the same reason.
CREATE TABLE audit_log (
    id           bigserial PRIMARY KEY,
    org_id       uuid NOT NULL,
    at           timestamptz NOT NULL DEFAULT now(),
    actor_id     uuid,
    actor_name   text NOT NULL DEFAULT '',
    action       text NOT NULL,
    subject_id   uuid,
    subject_name text NOT NULL DEFAULT '',
    detail       text NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_org_at ON audit_log (org_id, at);
CREATE INDEX audit_log_subject_at ON audit_log (subject_id, at);
