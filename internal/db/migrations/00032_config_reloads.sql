-- Runtime config reload (D-200, task 6.13):
--
-- config_reload_requests  one row per reload request: trigger 'ui' (an
--                         admin's reloadConfig, broadcast to every serve
--                         and worker process with a NOTIFY that carries
--                         only this row's id) or 'signal' (SIGHUP to one
--                         process, never broadcast). A process acts on a
--                         NOTIFY only when a matching, recent 'ui' row
--                         exists, so a forged NOTIFY does nothing.
--
--     requested_by  the admin of a 'ui' request (NULL for 'signal').
--     instance_id   the process that took the request (the serve
--                   instance of the API call, or the signalled process).
--
-- config_reload_results   one row per process and request with the
--                         outcome, plus one 'loaded' row per process start
--                         (request_id NULL; it doubles as the registry of
--                         processes). Key NAMES only, never values or the
--                         config file path.
--
--     status         loaded | applied | unchanged | restart_required |
--                    rejected | failed.
--     error_kind     rejected: file | syntax | invalid; failed: apply.
--     applied_keys   live keys this reload applied.
--     restart_keys   changed keys that need a restart (still pending).
--     error_keys     keys named by a rejected file's errors.
--     weakened_keys  applied keys whose change weakens security (alerted).
--
-- bekup_app: SELECT, INSERT and DELETE (pruning after 30 days); no UPDATE.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.config_reload_requests (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    requested_at timestamptz NOT NULL DEFAULT now(),
    requested_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    trigger      text NOT NULL,
    instance_id  uuid NOT NULL,
    CONSTRAINT config_reload_requests_trigger CHECK (trigger IN ('ui', 'signal')),
    CONSTRAINT config_reload_requests_signal_actor CHECK (trigger = 'ui' OR requested_by IS NULL)
);

CREATE INDEX config_reload_requests_requested_at_idx ON public.config_reload_requests (requested_at);

CREATE TABLE public.config_reload_results (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id         uuid REFERENCES public.config_reload_requests (id) ON DELETE CASCADE,
    instance_id        uuid NOT NULL,
    process_kind       text NOT NULL,
    hostname           text NOT NULL,
    version            text NOT NULL,
    process_started_at timestamptz NOT NULL,
    finished_at        timestamptz NOT NULL DEFAULT now(),
    status             text NOT NULL,
    error_kind         text,
    applied_keys       text[] NOT NULL DEFAULT '{}',
    restart_keys       text[] NOT NULL DEFAULT '{}',
    error_keys         text[] NOT NULL DEFAULT '{}',
    weakened_keys      text[] NOT NULL DEFAULT '{}',
    CONSTRAINT config_reload_results_once UNIQUE (request_id, instance_id),
    CONSTRAINT config_reload_results_kind CHECK (process_kind IN ('serve', 'worker')),
    CONSTRAINT config_reload_results_hostname CHECK (hostname ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$'),
    CONSTRAINT config_reload_results_version CHECK (version ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$'),
    CONSTRAINT config_reload_results_status CHECK (
        status IN ('loaded', 'applied', 'unchanged', 'restart_required', 'rejected', 'failed')),
    CONSTRAINT config_reload_results_loaded CHECK ((status = 'loaded') = (request_id IS NULL)),
    CONSTRAINT config_reload_results_error_kind CHECK (
        (status = 'rejected' AND error_kind IN ('file', 'syntax', 'invalid'))
        OR (status = 'failed' AND error_kind = 'apply')
        OR (status NOT IN ('rejected', 'failed') AND error_kind IS NULL)),
    CONSTRAINT config_reload_results_cardinality CHECK (
        cardinality(applied_keys) <= 256 AND cardinality(restart_keys) <= 256
        AND cardinality(error_keys) <= 256 AND cardinality(weakened_keys) <= 256),
    CONSTRAINT config_reload_results_no_nulls CHECK (
        array_position(applied_keys, NULL) IS NULL AND array_position(restart_keys, NULL) IS NULL
        AND array_position(error_keys, NULL) IS NULL AND array_position(weakened_keys, NULL) IS NULL),
    CONSTRAINT config_reload_results_key_names CHECK (
        array_to_string(applied_keys, ',') ~ '^([a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}(,|$))*$'
        AND array_to_string(restart_keys, ',') ~ '^([a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}(,|$))*$'
        AND array_to_string(error_keys, ',') ~ '^([a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}(,|$))*$'
        AND array_to_string(weakened_keys, ',') ~ '^([a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}(,|$))*$')
);

CREATE INDEX config_reload_results_instance_idx ON public.config_reload_results (instance_id, finished_at DESC);
CREATE INDEX config_reload_results_finished_at_idx ON public.config_reload_results (finished_at);

REVOKE ALL ON TABLE public.config_reload_requests, public.config_reload_results FROM PUBLIC;
REVOKE TRUNCATE, REFERENCES, TRIGGER, UPDATE ON TABLE public.config_reload_requests, public.config_reload_results FROM bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.config_reload_results;
DROP TABLE public.config_reload_requests;
-- +goose StatementEnd
