-- Released weeks (ADR 0012 §3). Every aggregate is read from here: a week
-- is computed once, three days after it ends, with small groups and their
-- complements withheld across all dimensions and periods together, and
-- never recomputed, so asking again (another window, another dimension,
-- after a late upload or a removal) shows nothing new. Withheld cells keep
-- their group's name and no figures.

CREATE TABLE released_weeks (
    org_id         uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    week_start     date NOT NULL CHECK (extract(isodow FROM week_start) = 1),
    min_group_size int  NOT NULL,
    released_at    timestamptz NOT NULL DEFAULT now(),
    withheld       int  NOT NULL DEFAULT 0,
    fallback       text NOT NULL DEFAULT '' CHECK (fallback IN ('', 'days', 'all')),
    PRIMARY KEY (org_id, week_start)
);

CREATE TABLE released_cells (
    org_id             uuid NOT NULL,
    week_start         date NOT NULL,
    dim                text NOT NULL CHECK (dim IN ('team', 'repo', 'kind')),
    period             text NOT NULL CHECK (period IN ('day', 'week')),
    period_start       date NOT NULL,
    grp                text NOT NULL,
    people             int  NOT NULL CHECK (people >= 0),
    suppressed         boolean NOT NULL,
    sessions           bigint NOT NULL DEFAULT 0,
    instructions       bigint NOT NULL DEFAULT 0,
    turns              bigint NOT NULL DEFAULT 0,
    tool_calls         bigint NOT NULL DEFAULT 0,
    active_seconds     double precision NOT NULL DEFAULT 0,
    first_try          bigint NOT NULL DEFAULT 0,
    reworked           bigint NOT NULL DEFAULT 0,
    interrupted        bigint NOT NULL DEFAULT 0,
    escalated          bigint NOT NULL DEFAULT 0,
    rejected           bigint NOT NULL DEFAULT 0,
    tokens             bigint NOT NULL DEFAULT 0,
    cost_usd           double precision NOT NULL DEFAULT 0,
    api_equivalent_usd double precision NOT NULL DEFAULT 0,
    unpriced_turns     bigint NOT NULL DEFAULT 0,
    -- A withheld cell holds no figures.
    CHECK (NOT suppressed OR (people = 0 AND instructions = 0 AND tokens = 0 AND sessions = 0 AND cost_usd = 0)),
    PRIMARY KEY (org_id, dim, period, period_start, grp),
    FOREIGN KEY (org_id, week_start) REFERENCES released_weeks (org_id, week_start) ON DELETE CASCADE
);
