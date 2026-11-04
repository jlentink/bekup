-- External API access (D-220): per-job API tokens, run requests and the
-- `api` schema the bekup_external role may use.
--
--   api_tokens      one row per token. id is the token's public id (it is
--                   part of the token string); secret_hash is SHA-256 over
--                   "bekup-api-token-v1\0" || id || secret, computed by the
--                   Go side (the token itself is never stored). Expiry is
--                   mandatory (at most 366 days after creation, the UI
--                   offers 1-365 days); revocation is one-way. Client
--                   addresses (D-220 amendment 1): either a list of 1-32
--                   networks (allowed_cidrs) or the explicit choice
--                   allow_any_address, never both and never neither;
--                   only these two columns may change (unrevoked tokens).
--   api_token_jobs  the jobs a token may run; immutable (a new scope needs
--                   a new token).
--   api_access_denials  a valid token presented from an address outside its
--                   allowlist (a theft signal): at most one row per token
--                   per 10 minutes, written by the api functions; the
--                   workers' consumer audits it (api_token.ip_denied) and
--                   alerts, then sets processed_at. Pruned after 30 days.
--   run_requests    one row per POST /api/v1/jobs/{id}/runs that passed
--                   the checks of api.request_run: pending until the worker's consumer decides it, then
--                   accepted (with the River job and the queued run) or
--                   rejected with a reason. An idempotency key is unique per
--                   token; a request older than 24 h releases its key.
--                   "Last used" of a token is max(created_at) of its
--                   requests. Rows are pruned after 30 days.
--
-- bekup_app: SELECT/INSERT on api_tokens and api_token_jobs, UPDATE only of
-- the revocation columns, never DELETE; SELECT, DELETE (pruning) and UPDATE
-- of the decision columns of run_requests, never INSERT (requests are only
-- created by api.request_run); UPDATE of allowed_cidrs/allow_any_address;
-- SELECT, DELETE (pruning) and UPDATE of processed_at on
-- api_access_denials, never INSERT.
--
-- Client addresses (normalized the same way everywhere by
-- public.api_client_ip): a client address must be a host address (/32,
-- /128); an IPv4-mapped IPv6 address (::ffff:a.b.c.d) is unmapped to the
-- IPv4 address before it is matched or stored, so it matches IPv4
-- entries only. allowed_cidrs may not hold IPv4-mapped networks or a /0
-- (any address is allow_any_address). A NULL or non-host client address
-- matches nothing. A refused address answers exactly like a wrong secret.
--
-- The scope rule is public.api_token_job_allowed (checked by every api
-- function and again by the consumer): the token is unrevoked and
-- unexpired, its creator is an active (not disabled, not deleted) admin, or
-- an active user (role user, D-067) who still owns the job, and the job is
-- not deleted.
--
-- Schema api (owner bekup_owner): SECURITY DEFINER functions, the only
-- objects bekup_external may use. Each takes the token id and the secret
-- hash and authenticates itself, so a compromised external-api process can
-- neither read token hashes nor act for a token whose secret it does not
-- have. bekup_external gets nothing in public (not even USAGE); the grants
-- are applied by public.bekup_sync_external_grants(), here and after every
-- `migrate up` (the role may be created after the migrations).

-- +goose Up
-- +goose StatementBegin

-- ---------------------------------------------------------------------------
-- Client address rules (D-220 amendment 1). Immutable helpers: the CHECK
-- of api_tokens.allowed_cidrs and the address check of the api functions.
-- ---------------------------------------------------------------------------

-- api_cidrs_valid: an allowlist holds at most 32 distinct networks, no
-- NULL, no IPv4 network wider than /8 and no IPv6 network wider than /32
-- (that wide is allow_any_address in all but name, which is alerted and
-- badged; apiaccess.ParseAddresses has the same rule) and no IPv4-mapped
-- IPv6 network (client addresses are unmapped before matching, so such an
-- entry would never match).
CREATE FUNCTION public.api_cidrs_valid(p_cidrs cidr[]) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT p_cidrs IS NOT NULL
       AND pg_catalog.cardinality(p_cidrs) <= 32
       AND (pg_catalog.cardinality(p_cidrs) = 0 OR pg_catalog.array_ndims(p_cidrs) = 1)
       AND NOT EXISTS (
           SELECT 1 FROM pg_catalog.unnest(p_cidrs) AS c (net)
           WHERE c.net IS NULL
              OR pg_catalog.masklen(c.net) < CASE pg_catalog.family(c.net) WHEN 4 THEN 8 ELSE 32 END
              OR (pg_catalog.family(c.net) = 6 AND c.net::inet <<= '::ffff:0.0.0.0/96'::inet))
       AND (SELECT pg_catalog.count(DISTINCT c.net) FROM pg_catalog.unnest(p_cidrs) AS c (net))
           = pg_catalog.cardinality(p_cidrs)
$$;

-- api_client_ip: the normalized client address: NULL unless a host
-- address (/32 or /128); an IPv4-mapped IPv6 address (::ffff:a.b.c.d)
-- becomes the IPv4 address a.b.c.d.
CREATE FUNCTION public.api_client_ip(p_ip inet) RETURNS inet
    LANGUAGE sql IMMUTABLE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT CASE
        WHEN p_ip IS NULL
             OR pg_catalog.masklen(p_ip) <> CASE pg_catalog.family(p_ip) WHEN 4 THEN 32 ELSE 128 END THEN NULL
        WHEN pg_catalog.family(p_ip) = 6 AND p_ip <<= '::ffff:0.0.0.0/96'::inet
            THEN '0.0.0.0'::inet + (p_ip - '::ffff:0.0.0.0'::inet)
        ELSE p_ip
    END
$$;

-- api_client_ip_allowed: whether a token with these address settings may
-- be used from p_ip (normalized first; NULL or a network matches no list).
CREATE FUNCTION public.api_client_ip_allowed(p_cidrs cidr[], p_any boolean, p_ip inet) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT CASE
        WHEN p_any THEN true
        ELSE coalesce(public.api_client_ip(p_ip) <<= ANY (p_cidrs::inet[]), false)
    END
$$;

CREATE TABLE public.api_tokens (
    id                uuid PRIMARY KEY,
    name              text NOT NULL,
    secret_hash       bytea NOT NULL,
    created_by        uuid NOT NULL REFERENCES public.users (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    expires_at        timestamptz NOT NULL,
    revoked_at        timestamptz,
    revoked_by        uuid REFERENCES public.users (id),
    allowed_cidrs     cidr[] NOT NULL DEFAULT '{}',
    allow_any_address boolean NOT NULL,
    CONSTRAINT api_tokens_secret_hash_key UNIQUE (secret_hash),
    CONSTRAINT api_tokens_secret_hash_length CHECK (octet_length(secret_hash) = 32),
    CONSTRAINT api_tokens_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT api_tokens_expiry CHECK (expires_at > created_at AND expires_at <= created_at + interval '366 days'),
    CONSTRAINT api_tokens_revoked_by CHECK (revoked_by IS NULL OR revoked_at IS NOT NULL),
    CONSTRAINT api_tokens_allowed_cidrs CHECK (public.api_cidrs_valid(allowed_cidrs)),
    -- Any address is an explicit choice, and a list is never silently
    -- ignored: exactly one of the two.
    CONSTRAINT api_tokens_addresses CHECK (allow_any_address = (cardinality(allowed_cidrs) = 0))
);

CREATE INDEX api_tokens_created_by_idx ON public.api_tokens (created_by);

-- Only the revocation (once) and, while unrevoked, the client addresses
-- may change.
CREATE FUNCTION public.api_tokens_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.name IS DISTINCT FROM OLD.name
       OR NEW.secret_hash IS DISTINCT FROM OLD.secret_hash OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'api_tokens: token % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND (NEW.revoked_at IS DISTINCT FROM OLD.revoked_at
                                       OR NEW.revoked_by IS DISTINCT FROM OLD.revoked_by
                                       OR NEW.allowed_cidrs IS DISTINCT FROM OLD.allowed_cidrs
                                       OR NEW.allow_any_address IS DISTINCT FROM OLD.allow_any_address) THEN
        RAISE EXCEPTION 'api_tokens: token % is already revoked', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER api_tokens_guard BEFORE UPDATE ON public.api_tokens
    FOR EACH ROW EXECUTE FUNCTION public.api_tokens_guard();

CREATE TABLE public.api_token_jobs (
    token_id uuid NOT NULL REFERENCES public.api_tokens (id),
    job_id   uuid NOT NULL REFERENCES public.jobs (id),
    PRIMARY KEY (token_id, job_id)
);

CREATE INDEX api_token_jobs_job_idx ON public.api_token_jobs (job_id);

CREATE TYPE public.api_request_outcome AS ENUM ('pending', 'accepted', 'rejected');

CREATE TABLE public.run_requests (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_id        uuid NOT NULL REFERENCES public.api_tokens (id),
    job_id          uuid NOT NULL REFERENCES public.jobs (id),
    ip              inet,
    idempotency_key text,
    outcome         public.api_request_outcome NOT NULL DEFAULT 'pending',
    reason          text,
    river_job_id    bigint,
    run_id          uuid REFERENCES public.runs (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_at      timestamptz,
    CONSTRAINT run_requests_ip_host CHECK (masklen(ip) = CASE family(ip) WHEN 4 THEN 32 ELSE 128 END),
    CONSTRAINT run_requests_idempotency_key_format CHECK (idempotency_key ~ '^[!-~]{1,255}$'),
    CONSTRAINT run_requests_reason CHECK (reason IN ('run_active', 'job_disabled', 'job_gone', 'token_invalid', 'expired')),
    CONSTRAINT run_requests_outcome_fields CHECK (
        (outcome = 'pending' AND reason IS NULL AND river_job_id IS NULL AND run_id IS NULL AND decided_at IS NULL)
        OR (outcome = 'accepted' AND reason IS NULL AND river_job_id IS NOT NULL AND run_id IS NOT NULL AND decided_at IS NOT NULL)
        OR (outcome = 'rejected' AND reason IS NOT NULL AND river_job_id IS NULL AND run_id IS NULL AND decided_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX run_requests_idempotency_key ON public.run_requests (token_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX run_requests_token_idx ON public.run_requests (token_id, created_at DESC, id DESC);
CREATE INDEX run_requests_pending_idx ON public.run_requests (created_at, id) WHERE outcome = 'pending';
CREATE INDEX run_requests_created_idx ON public.run_requests (created_at);
-- One request per River job; the run history finds the token of an API run
-- through it.
CREATE UNIQUE INDEX run_requests_river_job_key ON public.run_requests (river_job_id)
    WHERE river_job_id IS NOT NULL;

-- A request is decided once; who asked for what never changes. The
-- idempotency key may only be released (api.request_run, after 24 h).
CREATE FUNCTION public.run_requests_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.token_id IS DISTINCT FROM OLD.token_id
       OR NEW.job_id IS DISTINCT FROM OLD.job_id OR NEW.ip IS DISTINCT FROM OLD.ip
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key AND NEW.idempotency_key IS NOT NULL) THEN
        RAISE EXCEPTION 'run_requests: the identity of request % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.outcome <> 'pending' AND (NEW.outcome IS DISTINCT FROM OLD.outcome OR NEW.reason IS DISTINCT FROM OLD.reason
                                     OR NEW.river_job_id IS DISTINCT FROM OLD.river_job_id
                                     OR NEW.run_id IS DISTINCT FROM OLD.run_id
                                     OR NEW.decided_at IS DISTINCT FROM OLD.decided_at) THEN
        RAISE EXCEPTION 'run_requests: request % is already decided', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER run_requests_guard BEFORE UPDATE ON public.run_requests
    FOR EACH ROW EXECUTE FUNCTION public.run_requests_guard();

-- A valid token presented from an address outside its allowlist (written
-- only by public.api_token_admit, at most one row per token per 10
-- minutes). client_ip is the normalized address, NULL when the external
-- API passed none (or no host address).
CREATE TABLE public.api_access_denials (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_id     uuid NOT NULL REFERENCES public.api_tokens (id),
    client_ip    inet,
    denied_at    timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    CONSTRAINT api_access_denials_ip_host CHECK (masklen(client_ip) = CASE family(client_ip) WHEN 4 THEN 32 ELSE 128 END),
    CONSTRAINT api_access_denials_ip_unmapped CHECK (NOT (client_ip <<= '::ffff:0.0.0.0/96'::inet))
);

CREATE INDEX api_access_denials_token_idx ON public.api_access_denials (token_id, denied_at DESC, id DESC);
CREATE INDEX api_access_denials_pending_idx ON public.api_access_denials (denied_at, id) WHERE processed_at IS NULL;
CREATE INDEX api_access_denials_denied_idx ON public.api_access_denials (denied_at);

-- A denial is processed once; what was denied never changes.
CREATE FUNCTION public.api_access_denials_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.token_id IS DISTINCT FROM OLD.token_id
       OR NEW.client_ip IS DISTINCT FROM OLD.client_ip OR NEW.denied_at IS DISTINCT FROM OLD.denied_at
       OR (OLD.processed_at IS NOT NULL AND NEW.processed_at IS DISTINCT FROM OLD.processed_at) THEN
        RAISE EXCEPTION 'api_access_denials: denial % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER api_access_denials_guard BEFORE UPDATE ON public.api_access_denials
    FOR EACH ROW EXECUTE FUNCTION public.api_access_denials_guard();

-- ---------------------------------------------------------------------------
-- The token rules. Invoker rights: bekup_app reads the same tables anyway;
-- inside the api functions they run as bekup_owner.
-- ---------------------------------------------------------------------------

-- api_token_valid: unrevoked, unexpired, and the creator is an active admin
-- or user (a maintainer never holds a token: demotion to maintainer, a
-- disabled or a deleted creator invalidate the token at once).
CREATE FUNCTION public.api_token_valid(p_token_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM public.api_tokens AS t
        JOIN public.users AS u ON u.id = t.created_by
        WHERE t.id = p_token_id
          AND t.revoked_at IS NULL
          AND t.expires_at > pg_catalog.now()
          AND NOT u.disabled
          AND u.deleted_at IS NULL
          AND u.role IN ('admin', 'user')
    )
$$;

-- api_token_job_allowed: the single scope rule (D-220).
CREATE FUNCTION public.api_token_job_allowed(p_token_id uuid, p_job_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM public.api_tokens AS t
        JOIN public.api_token_jobs AS tj ON tj.token_id = t.id AND tj.job_id = p_job_id
        JOIN public.jobs AS j ON j.id = tj.job_id AND j.deleted_at IS NULL
        JOIN public.users AS u ON u.id = t.created_by
        WHERE t.id = p_token_id
          AND public.api_token_valid(t.id)
          AND (u.role = 'admin'
               OR (u.role = 'user' AND EXISTS (
                   SELECT 1 FROM public.job_owners AS o WHERE o.job_id = j.id AND o.user_id = u.id)))
    )
$$;

-- api_token_admit: the authentication of every api function: the token
-- id and secret hash match, the token is valid (api_token_valid) and the
-- client address is allowed (api_client_ip_allowed). Any failure is the
-- same false. A valid token from a refused address is a theft signal: it
-- records a denial unless the token has one from the last 10 minutes
-- (checked again under the token's row lock, so concurrent calls write
-- one row) and announces it with pg_notify('bekup_api_requests',
-- 'denial:<id>') (a wake-up only, like the requests). READ COMMITTED
-- only: the re-check after the lock must see the previous holder's row.
-- Runs as bekup_owner inside the SECURITY DEFINER api functions; nobody
-- else may execute it.
CREATE FUNCTION public.api_token_admit(p_token_id uuid, p_secret_hash bytea, p_ip inet) RETURNS boolean
    LANGUAGE plpgsql VOLATILE
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    v_cidrs cidr[];
    v_any   boolean;
    v_id    uuid;
BEGIN
    IF pg_catalog.current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'the api functions require READ COMMITTED isolation'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    IF p_token_id IS NULL OR p_secret_hash IS NULL OR pg_catalog.octet_length(p_secret_hash) <> 32 THEN
        RETURN false;
    END IF;
    SELECT t.allowed_cidrs, t.allow_any_address INTO v_cidrs, v_any
    FROM public.api_tokens AS t
    WHERE t.id = p_token_id AND t.secret_hash = p_secret_hash;
    IF NOT FOUND OR NOT public.api_token_valid(p_token_id) THEN
        RETURN false;
    END IF;
    IF public.api_client_ip_allowed(v_cidrs, v_any, p_ip) THEN
        RETURN true;
    END IF;

    IF EXISTS (SELECT 1 FROM public.api_access_denials AS d
               WHERE d.token_id = p_token_id AND d.denied_at > pg_catalog.now() - interval '10 minutes') THEN
        RETURN false;
    END IF;
    PERFORM 1 FROM public.api_tokens AS t WHERE t.id = p_token_id FOR NO KEY UPDATE OF t;
    IF NOT EXISTS (SELECT 1 FROM public.api_access_denials AS d
                   WHERE d.token_id = p_token_id AND d.denied_at > pg_catalog.now() - interval '10 minutes') THEN
        INSERT INTO public.api_access_denials AS d (token_id, client_ip)
        VALUES (p_token_id, public.api_client_ip(p_ip))
        RETURNING d.id INTO v_id;
        PERFORM pg_catalog.pg_notify('bekup_api_requests', 'denial:' || v_id::text);
    END IF;
    RETURN false;
END
$$;

-- ---------------------------------------------------------------------------
-- Schema api: the external API's only door into the database.
-- ---------------------------------------------------------------------------
CREATE SCHEMA api AUTHORIZATION bekup_owner;
REVOKE ALL ON SCHEMA api FROM PUBLIC;

-- Result row types of the api functions. They are empty views (WHERE
-- false) instead of composite types because sqlc reads the columns of a
-- set-returning function only from a table or view row type. Nobody has a
-- privilege on them.
CREATE VIEW api.authenticate_result AS
    SELECT NULL::uuid AS token_id, NULL::text AS name, NULL::timestamptz AS expires_at
    WHERE false;
CREATE VIEW api.token_jobs_result AS
    SELECT NULL::uuid AS job_id, NULL::text AS job_name
    WHERE false;
CREATE VIEW api.request_run_result AS
    SELECT NULL::uuid AS request_id, NULL::timestamptz AS created_at, NULL::text AS refusal,
           NULL::boolean AS replayed, NULL::timestamptz AS retry_at
    WHERE false;
CREATE VIEW api.request_status_result AS
    SELECT NULL::uuid AS request_id, NULL::uuid AS job_id, NULL::text AS outcome, NULL::text AS reason,
           NULL::timestamptz AS created_at, NULL::timestamptz AS decided_at, NULL::bigint AS river_job_id,
           NULL::text AS river_state, NULL::uuid AS run_id, NULL::text AS run_status,
           NULL::integer AS run_attempt, NULL::timestamptz AS run_queued_at,
           NULL::timestamptz AS run_started_at, NULL::timestamptz AS run_finished_at
    WHERE false;

-- Every function below takes the client address (p_client_ip, p_ip for
-- request_run) and admits the call with public.api_token_admit: unknown
-- id, wrong secret, revoked, expired, creator no longer allowed or a
-- client address outside the token's allowlist all answer the same way
-- (no row, or the refusal 'unauthenticated'). VOLATILE and READ COMMITTED
-- only: a refused address may record a denial.

-- authenticate: the token's facts, or no row.
CREATE FUNCTION api.authenticate(p_token_id uuid, p_secret_hash bytea, p_client_ip inet)
    RETURNS SETOF api.authenticate_result
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF public.api_token_admit(p_token_id, p_secret_hash, p_client_ip) THEN
        RETURN QUERY
            SELECT t.id, t.name, t.expires_at
            FROM public.api_tokens AS t
            WHERE t.id = p_token_id;
    END IF;
END
$$;

-- token_jobs: the jobs currently in the token's scope (GET /api/v1/self).
CREATE FUNCTION api.token_jobs(p_token_id uuid, p_secret_hash bytea, p_client_ip inet)
    RETURNS SETOF api.token_jobs_result
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF public.api_token_admit(p_token_id, p_secret_hash, p_client_ip) THEN
        RETURN QUERY
            SELECT j.id, j.name
            FROM public.api_token_jobs AS tj
            JOIN public.jobs AS j ON j.id = tj.job_id
            WHERE tj.token_id = p_token_id
              AND public.api_token_job_allowed(p_token_id, j.id)
            ORDER BY pg_catalog.lower(j.name), j.id;
    END IF;
END
$$;

-- request_run files a run request (POST /api/v1/jobs/{id}/runs). Checks in
-- this order, under the token's row lock (serializes the requests of one
-- token, so the caps and the idempotency check cannot race):
--   unauthenticated       api_token_admit refused (unknown id, wrong
--                         secret, revoked, expired, creator no longer
--                         allowed, client address not allowed; checked
--                         again under the row lock)
--   invalid_request       no job id, malformed idempotency key or address
--   not_found             job unknown, deleted or outside the scope
--   idempotency_mismatch  the key was used for another job within 24 h
--   (replay)              the key was used for this job within 24 h: the
--                         original request is returned (replayed = true)
--   too_many_pending      3 undecided requests of this token
--   rate_limited          30 requests of this token in the last hour
--                         (retry_at: when the oldest leaves the window)
--   job_disabled          the job is disabled
--   run_active            a backup_run of the job is queued, running or
--                         waiting for a retry (River's unique states)
-- Otherwise the request is inserted (pending) and announced with
-- pg_notify('bekup_api_requests', <request id>); the NOTIFY is only a
-- wake-up, the consumer claims pending rows itself. A refusal writes
-- nothing but a possible denial (api_token_admit). The stored address is
-- the normalized one (public.api_client_ip). READ COMMITTED only: the
-- counts must see the requests the previous lock holder committed.
CREATE FUNCTION api.request_run(p_token_id uuid, p_secret_hash bytea, p_job_id uuid,
                                p_idempotency_key text, p_ip inet)
    RETURNS SETOF api.request_run_result
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    v_prev    public.run_requests%ROWTYPE;
    v_count   bigint;
    v_oldest  timestamptz;
    v_enabled boolean;
    v_id      uuid;
    v_created timestamptz;
    v_cidrs   cidr[];
    v_any     boolean;
    v_ip      inet;
BEGIN
    IF pg_catalog.current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'api.request_run requires READ COMMITTED isolation'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    IF NOT public.api_token_admit(p_token_id, p_secret_hash, p_ip) THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'unauthenticated'::text, false, NULL::timestamptz;
        RETURN;
    END IF;
    -- Under the lock again: a revocation or an address change that
    -- committed meanwhile applies to this request.
    SELECT t.allowed_cidrs, t.allow_any_address INTO v_cidrs, v_any
    FROM public.api_tokens AS t
    WHERE t.id = p_token_id AND t.secret_hash = p_secret_hash
    FOR NO KEY UPDATE OF t;
    IF NOT FOUND OR NOT public.api_token_valid(p_token_id)
       OR NOT public.api_client_ip_allowed(v_cidrs, v_any, p_ip) THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'unauthenticated'::text, false, NULL::timestamptz;
        RETURN;
    END IF;
    v_ip := public.api_client_ip(p_ip);
    IF p_job_id IS NULL
       OR (p_idempotency_key IS NOT NULL AND p_idempotency_key !~ '^[!-~]{1,255}$')
       OR (p_ip IS NOT NULL AND v_ip IS NULL) THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'invalid_request'::text, false, NULL::timestamptz;
        RETURN;
    END IF;
    IF NOT public.api_token_job_allowed(p_token_id, p_job_id) THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'not_found'::text, false, NULL::timestamptz;
        RETURN;
    END IF;

    IF p_idempotency_key IS NOT NULL THEN
        SELECT r.* INTO v_prev FROM public.run_requests AS r
        WHERE r.token_id = p_token_id AND r.idempotency_key = p_idempotency_key;
        IF FOUND THEN
            IF v_prev.created_at > pg_catalog.now() - interval '24 hours' THEN
                IF v_prev.job_id <> p_job_id THEN
                    RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'idempotency_mismatch'::text, false, NULL::timestamptz;
                    RETURN;
                END IF;
                RETURN QUERY SELECT v_prev.id, v_prev.created_at, NULL::text, true, NULL::timestamptz;
                RETURN;
            END IF;
            -- Older than the replay window: the key is free again.
            UPDATE public.run_requests AS r SET idempotency_key = NULL WHERE r.id = v_prev.id;
        END IF;
    END IF;

    SELECT pg_catalog.count(*) INTO v_count FROM public.run_requests AS r
    WHERE r.token_id = p_token_id AND r.outcome = 'pending';
    IF v_count >= 3 THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'too_many_pending'::text, false, NULL::timestamptz;
        RETURN;
    END IF;
    SELECT pg_catalog.count(*), pg_catalog.min(r.created_at) INTO v_count, v_oldest FROM public.run_requests AS r
    WHERE r.token_id = p_token_id AND r.created_at > pg_catalog.now() - interval '1 hour';
    IF v_count >= 30 THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'rate_limited'::text, false, v_oldest + interval '1 hour';
        RETURN;
    END IF;

    SELECT j.enabled INTO v_enabled FROM public.jobs AS j WHERE j.id = p_job_id AND j.deleted_at IS NULL;
    IF NOT FOUND THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'not_found'::text, false, NULL::timestamptz;
        RETURN;
    END IF;
    IF NOT v_enabled THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'job_disabled'::text, false, NULL::timestamptz;
        RETURN;
    END IF;
    -- River's unique key of backup_run is the job id in the states of
    -- internal/queue.UniqueStates.
    IF EXISTS (
        SELECT 1 FROM public.river_job AS rj
        WHERE rj.kind = 'backup_run'
          AND rj.args @> pg_catalog.jsonb_build_object('job_id', p_job_id)
          AND rj.state::text IN ('available', 'pending', 'retryable', 'running', 'scheduled')
    ) THEN
        RETURN QUERY SELECT NULL::uuid, NULL::timestamptz, 'run_active'::text, false, NULL::timestamptz;
        RETURN;
    END IF;

    INSERT INTO public.run_requests AS r (token_id, job_id, ip, idempotency_key)
    VALUES (p_token_id, p_job_id, v_ip, p_idempotency_key)
    RETURNING r.id, r.created_at INTO v_id, v_created;
    PERFORM pg_catalog.pg_notify('bekup_api_requests', v_id::text);
    RETURN QUERY SELECT v_id, v_created, NULL::text, false, NULL::timestamptz;
END
$$;

-- request_status: the raw facts of one request of this token (GET
-- /api/v1/requests/{id}); no row for another token's request, a job outside
-- the scope or a refused token. The state aggregation (pending, queued,
-- running, retrying, success, ...) is done by the Go side from the
-- outcome, the latest run of the River job (by attempt) and the River
-- job's state (NULL once River deleted the finished job).
CREATE FUNCTION api.request_status(p_token_id uuid, p_secret_hash bytea, p_request_id uuid, p_client_ip inet)
    RETURNS SETOF api.request_status_result
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NOT public.api_token_admit(p_token_id, p_secret_hash, p_client_ip) THEN
        RETURN;
    END IF;
    RETURN QUERY
        SELECT r.id, r.job_id, r.outcome::text, r.reason, r.created_at,
               r.decided_at, r.river_job_id, rj.state::text,
               lr.id, lr.status::text, lr.attempt, lr.queued_at,
               lr.started_at, lr.finished_at
        FROM public.run_requests AS r
        LEFT JOIN public.river_job AS rj ON rj.id = r.river_job_id
        LEFT JOIN LATERAL (
            SELECT x.id, x.status, x.attempt, x.queued_at, x.started_at, x.finished_at
            FROM public.runs AS x
            WHERE x.river_job_id = r.river_job_id AND x.job_id = r.job_id
            ORDER BY x.attempt DESC, x.created_at DESC, x.id DESC
            LIMIT 1
        ) AS lr ON true
        WHERE r.token_id = p_token_id
          AND r.id = p_request_id
          AND public.api_token_job_allowed(p_token_id, r.job_id);
END
$$;

-- schema_version: the external-api's startup and /readyz check, so that
-- bekup_external needs no access to schema public.
CREATE FUNCTION api.schema_version() RETURNS bigint
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT public.bekup_schema_version()
$$;

-- bekup_sync_external_grants: the complete privileges of bekup_external,
-- re-applied by `migrate up` (internal/db Migrator.Up; the role may be
-- created after the migrations). Without the role it does nothing and
-- returns false. It first takes back everything bekup_owner ever granted
-- the role in public and api, then grants exactly CONNECT, USAGE on api and
-- EXECUTE on the api functions. The privilege audit (db.CheckExternalRole)
-- refuses anything else, e.g. grants of another grantor. Run as
-- bekup_owner (invoker rights).
CREATE FUNCTION public.bekup_sync_external_grants() RETURNS boolean
    LANGUAGE plpgsql VOLATILE
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF current_user <> 'bekup_owner' THEN
        RAISE EXCEPTION 'bekup_sync_external_grants must run as bekup_owner (running as %)', current_user
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles AS r WHERE r.rolname = 'bekup_external') THEN
        RETURN false;
    END IF;
    REVOKE ALL ON ALL TABLES IN SCHEMA public, api FROM bekup_external;
    REVOKE ALL ON ALL SEQUENCES IN SCHEMA public, api FROM bekup_external;
    REVOKE ALL ON ALL ROUTINES IN SCHEMA public, api FROM bekup_external;
    REVOKE ALL ON SCHEMA public FROM bekup_external;
    REVOKE CREATE ON SCHEMA api FROM bekup_external;
    EXECUTE pg_catalog.format('REVOKE CREATE, TEMPORARY ON DATABASE %I FROM bekup_external', pg_catalog.current_database());
    -- CONNECT may come from the init script or bootstrap (another
    -- grantor); grant it only when missing.
    IF NOT pg_catalog.has_database_privilege('bekup_external', pg_catalog.current_database(), 'CONNECT') THEN
        EXECUTE pg_catalog.format('GRANT CONNECT ON DATABASE %I TO bekup_external', pg_catalog.current_database());
    END IF;
    GRANT USAGE ON SCHEMA api TO bekup_external;
    GRANT EXECUTE ON FUNCTION
        api.authenticate(uuid, bytea, inet),
        api.token_jobs(uuid, bytea, inet),
        api.request_run(uuid, bytea, uuid, text, inet),
        api.request_status(uuid, bytea, uuid, inet),
        api.schema_version()
        TO bekup_external;
    RETURN true;
END
$$;

REVOKE ALL ON TABLE public.api_tokens, public.api_token_jobs, public.run_requests, public.api_access_denials FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.api_tokens FROM bekup_app;
GRANT UPDATE (revoked_at, revoked_by, allowed_cidrs, allow_any_address) ON TABLE public.api_tokens TO bekup_app;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.api_token_jobs FROM bekup_app;
REVOKE INSERT, UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.run_requests FROM bekup_app;
GRANT UPDATE (outcome, reason, river_job_id, run_id, decided_at) ON TABLE public.run_requests TO bekup_app;
REVOKE INSERT, UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.api_access_denials FROM bekup_app;
GRANT UPDATE (processed_at) ON TABLE public.api_access_denials TO bekup_app;

REVOKE ALL ON FUNCTION public.api_tokens_guard() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.run_requests_guard() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.api_access_denials_guard() FROM PUBLIC, bekup_app;
-- Denials are written only inside the api functions (as bekup_owner).
REVOKE ALL ON FUNCTION public.api_token_admit(uuid, bytea, inet) FROM PUBLIC, bekup_app;
-- The address rules: bekup_app needs api_cidrs_valid for the CHECK of its
-- token writes; the other two are pure.
REVOKE ALL ON FUNCTION public.api_cidrs_valid(cidr[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.api_cidrs_valid(cidr[]) TO bekup_app;
REVOKE ALL ON FUNCTION public.api_client_ip(inet) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.api_client_ip(inet) TO bekup_app;
REVOKE ALL ON FUNCTION public.api_client_ip_allowed(cidr[], boolean, inet) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.api_client_ip_allowed(cidr[], boolean, inet) TO bekup_app;
REVOKE ALL ON FUNCTION public.api_token_valid(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.api_token_valid(uuid) TO bekup_app;
REVOKE ALL ON FUNCTION public.api_token_job_allowed(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.api_token_job_allowed(uuid, uuid) TO bekup_app;
REVOKE ALL ON FUNCTION public.bekup_sync_external_grants() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION api.authenticate(uuid, bytea, inet) FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION api.token_jobs(uuid, bytea, inet) FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION api.request_run(uuid, bytea, uuid, text, inet) FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION api.request_status(uuid, bytea, uuid, inet) FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION api.schema_version() FROM PUBLIC, bekup_app;
REVOKE ALL ON TABLE api.authenticate_result, api.token_jobs_result, api.request_run_result, api.request_status_result
    FROM PUBLIC, bekup_app;

SELECT public.bekup_sync_external_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- bekup_external keeps CONNECT: the init script and bootstrap grant it
-- with the role (recorded with the database owner as grantor, so it cannot
-- be told apart from the sync's grant), and without the api schema it
-- allows nothing.
DROP FUNCTION public.bekup_sync_external_grants();
DROP FUNCTION api.schema_version();
DROP FUNCTION api.request_status(uuid, bytea, uuid, inet);
DROP FUNCTION api.request_run(uuid, bytea, uuid, text, inet);
DROP FUNCTION api.token_jobs(uuid, bytea, inet);
DROP FUNCTION api.authenticate(uuid, bytea, inet);
DROP VIEW api.request_status_result;
DROP VIEW api.request_run_result;
DROP VIEW api.token_jobs_result;
DROP VIEW api.authenticate_result;
DROP SCHEMA api;
DROP FUNCTION public.api_token_admit(uuid, bytea, inet);
DROP FUNCTION public.api_token_job_allowed(uuid, uuid);
DROP FUNCTION public.api_token_valid(uuid);
DROP TABLE public.api_access_denials;
DROP FUNCTION public.api_access_denials_guard();
DROP TABLE public.run_requests;
DROP FUNCTION public.run_requests_guard();
DROP TYPE public.api_request_outcome;
DROP TABLE public.api_token_jobs;
DROP TABLE public.api_tokens;
DROP FUNCTION public.api_tokens_guard();
DROP FUNCTION public.api_client_ip_allowed(cidr[], boolean, inet);
DROP FUNCTION public.api_client_ip(inet);
DROP FUNCTION public.api_cidrs_valid(cidr[]);
-- +goose StatementEnd
