-- OpenID Connect sign-in for the web view (ADR 0012 §4). An organisation
-- names its issuer, client and allowed e-mail domains; the client secret
-- stays outside the database (an environment variable or a file on the
-- server), so neither the database nor a dump holds it. A verified e-mail
-- address signs in the member it is set on, and nobody else: SSO never
-- creates a member.

ALTER TABLE members ADD COLUMN email text
    CHECK (email IS NULL OR (length(email) BETWEEN 3 AND 254 AND email = lower(email) AND position('@' IN email) > 1));
CREATE UNIQUE INDEX members_org_email ON members (org_id, email) WHERE email IS NOT NULL AND removed_at IS NULL;

CREATE TABLE org_sso (
    org_id                 uuid PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    issuer                 text NOT NULL CHECK (length(issuer) BETWEEN 8 AND 500),
    client_id              text NOT NULL CHECK (length(client_id) BETWEEN 1 AND 255),
    client_secret_ref      text NOT NULL CHECK (client_secret_ref ~ '^(env:[A-Za-z_][A-Za-z0-9_]*|file:/.+)$'),
    allowed_domains        text[] NOT NULL CHECK (cardinality(allowed_domains) BETWEEN 1 AND 50),
    allow_unverified_email boolean NOT NULL DEFAULT false,
    updated_at             timestamptz NOT NULL DEFAULT now()
);

-- A sign-in in flight: the state sent to the issuer (as its hash), the
-- nonce its ID token must carry, and the PKCE verifier for the code
-- exchange. Used once, for at most ten minutes.
CREATE TABLE sso_logins (
    state_hash bytea PRIMARY KEY,
    org_id     uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    nonce      text NOT NULL,
    verifier   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sso_logins_expiry ON sso_logins (expires_at);
