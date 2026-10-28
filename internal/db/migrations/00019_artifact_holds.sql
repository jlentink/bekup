-- Artifact holds (M3 review, task 3.11): a lease that keeps retention from
-- deleting an artifact while it is being read. A download takes one when its
-- ticket is redeemed and releases it when the stream ends; restores (4.x)
-- take kind 'restore'. A hold expires on its own (expires_at) as a safety
-- net when its holder dies; the holder renews it while it works.
--
--   artifact_id  the held artifact (a present one: the hold is taken under
--                a share lock of the artifact row, which retention locks
--                FOR NO KEY UPDATE while it deletes the object, so a hold
--                and a deletion never interleave).
--   holder_id    the download ticket or restore that holds it.
--   expires_at   at most 1 hour ahead; renewals move it forward.
--
-- Retention never deletes an artifact with an unexpired hold; the sweep
-- skips it and retries on the next sweep.
--
-- bekup_app may insert, read and delete holds and may only update
-- expires_at.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.artifact_holds (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id uuid NOT NULL REFERENCES public.artifacts (id),
    kind        text NOT NULL,
    holder_id   uuid NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    CONSTRAINT artifact_holds_kind CHECK (kind IN ('download', 'restore')),
    CONSTRAINT artifact_holds_expiry CHECK (expires_at > created_at AND expires_at <= now() + interval '1 hour')
);

CREATE UNIQUE INDEX artifact_holds_holder_idx ON public.artifact_holds (artifact_id, kind, holder_id);
CREATE INDEX artifact_holds_artifact_idx ON public.artifact_holds (artifact_id, expires_at);
CREATE INDEX artifact_holds_expires_idx ON public.artifact_holds (expires_at);

REVOKE ALL ON TABLE public.artifact_holds FROM PUBLIC;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.artifact_holds FROM bekup_app;
GRANT UPDATE (expires_at) ON TABLE public.artifact_holds TO bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.artifact_holds;
-- +goose StatementEnd
