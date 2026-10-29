import { useApolloClient, useMutation, useQuery } from "@apollo/client/react";
import {
  BadgeCheck,
  CircleCheck,
  CircleX,
  KeyRound,
  Loader2,
  Mail,
  MailWarning,
  Plus,
  Power,
  Send,
  ShieldOff,
  Trash2,
  Webhook,
} from "lucide-react";
import { useState } from "react";

type NotificationTestResult = TestMyWebhookResult["testMyWebhook"];
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Section, StatePill } from "@/features/account/section";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { getFragmentData } from "@/graphql/gen";
import type {
  PersonalWebhookFieldsFragment,
  TestMyWebhookMutation as TestMyWebhookResult,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { AddWebhookDialog } from "./add-webhook-dialog";
import { notificationsMessages as M } from "./messages";
import { personalTestOutcome } from "./model";
import {
  DeleteMyWebhookMutation,
  MyNotificationChannelsQuery,
  MyNotificationPreferencesQuery,
  PersonalWebhookFields,
  ReenableMyWebhookMutation,
  RequestEmailVerificationMutation,
  RotateMyWebhookKeyMutation,
  TestMyWebhookMutation,
} from "./operations";
import { SigningKeyDialog, type IssuedKey } from "./signing-key-dialog";

/* ---- E-mail ---- */

function EmailSection({
  email,
  verified,
  verifiedAt,
}: {
  email: string;
  verified: boolean;
  verifiedAt: string | null;
}) {
  const [request, { loading }] = useMutation(RequestEmailVerificationMutation);
  const [sent, setSent] = useState<{ to: string; until: string } | null>(null);
  const send = async () => {
    try {
      const r = await request();
      const out = r.data?.requestEmailVerification;
      if (out) setSent({ to: out.sentTo, until: formatDateTime(out.expiresAt) });
    } catch (err) {
      toast.error(userMessage(err));
    }
  };
  return (
    <Section
      icon={Mail}
      title={M.email.heading}
      lead={M.email.lead}
      status={
        <StatePill on={verified}>{verified ? M.email.verified : M.email.unverified}</StatePill>
      }
    >
      <div className="flex flex-col gap-3 rounded-md border bg-card px-4 py-3.5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2.5">
            {verified ? (
              <BadgeCheck aria-hidden="true" className="size-4 shrink-0 text-status-success" />
            ) : (
              <MailWarning aria-hidden="true" className="size-4 shrink-0 text-status-partial" />
            )}
            <span className="truncate font-mono text-[0.8125rem]">{email}</span>
          </div>
          {verified ? (
            <span
              className="font-mono text-2xs text-muted-foreground"
              title={formatDateTime(verifiedAt)}
            >
              {M.email.verifiedAt(formatRelative(verifiedAt))}
            </span>
          ) : (
            <Button size="sm" variant="outline" disabled={loading} onClick={() => void send()}>
              {loading ? (
                <Loader2 aria-hidden="true" className="animate-spin" />
              ) : (
                <Send aria-hidden="true" />
              )}
              {sent ? M.email.resend : M.email.send}
            </Button>
          )}
        </div>
        {sent && !verified ? (
          <p role="status" className="text-sm text-muted-foreground">
            {M.email.sent(sent.to, sent.until)}
          </p>
        ) : null}
      </div>
    </Section>
  );
}

/* ---- Webhooks ---- */

const OUTCOME_TEXT = {
  ok: { title: M.webhooks.testOk, help: M.webhooks.testOkHelp },
  not_allowed: { title: M.webhooks.testNotAllowed, help: M.webhooks.testNotAllowedHelp },
  failed: { title: M.webhooks.testFailed, help: M.webhooks.testFailedHelp },
} as const;

/** A personal webhook test result: ok, not_allowed or failed only (D-198). */
function TestOutcome({ result }: { result: NotificationTestResult }) {
  const outcome = personalTestOutcome(result);
  const text = OUTCOME_TEXT[outcome];
  const Icon = outcome === "ok" ? CircleCheck : outcome === "not_allowed" ? ShieldOff : CircleX;
  return (
    <p
      role="status"
      data-testid="webhook-test-result"
      data-outcome={outcome}
      className={cn(
        "flex items-start gap-2 rounded-sm border px-3 py-2 text-xs",
        outcome === "ok" &&
          "border-status-success/40 bg-status-success-soft text-status-success-fg",
        outcome === "not_allowed" &&
          "border-status-partial/40 bg-status-partial-soft text-status-partial-fg",
        outcome === "failed" &&
          "border-status-failed/40 bg-status-failed-soft text-status-failed-fg",
      )}
    >
      <Icon aria-hidden="true" className="mt-px size-3.5 shrink-0" />
      <span>
        <span className="font-medium">{text.title}</span>
        <span className="block">{text.help}</span>
      </span>
    </p>
  );
}

function WebhookRow({
  hook,
  onRotated,
  onChanged,
}: {
  hook: PersonalWebhookFieldsFragment;
  onRotated: (issued: IssuedKey) => void;
  onChanged: () => Promise<unknown>;
}) {
  const { withStepUp } = useStepUp();
  const [test, { loading: testing }] = useMutation(TestMyWebhookMutation);
  const [rotate] = useMutation(RotateMyWebhookKeyMutation, { fetchPolicy: "no-cache" });
  const [remove] = useMutation(DeleteMyWebhookMutation);
  const [reenable, { loading: reenabling }] = useMutation(ReenableMyWebhookMutation);
  const [result, setResult] = useState<NotificationTestResult | null>(null);
  const [confirm, setConfirm] = useState<"rotate" | "delete" | null>(null);
  const s = hook.state;
  const auto = !s.enabled && s.disabledReason !== null && s.disabledReason !== "manual";
  const failures = Math.max(s.consecutiveFailures, s.permanentFailures);

  const runTest = async () => {
    setResult(null);
    try {
      const r = await test({ variables: { id: hook.id } });
      if (r.data) setResult(r.data.testMyWebhook);
    } catch (err) {
      toast.error(userMessage(err));
    }
  };

  const doRotate = async () => {
    try {
      const r = await withStepUp(() => rotate({ variables: { id: hook.id } }));
      const out = r.data?.rotateMyWebhookKey;
      if (!out) throw new Error("empty rotateMyWebhookKey result");
      onRotated({
        label: out.webhook.label,
        displayUrl: out.webhook.displayUrl,
        key: out.signingKey,
        kind: "rotated",
      });
    } catch (err) {
      reportActionError(err);
      throw err;
    }
  };

  const doDelete = async () => {
    try {
      await withStepUp(() => remove({ variables: { id: hook.id } }));
      await onChanged();
      toast.success(M.webhooks.deleted);
    } catch (err) {
      reportActionError(err);
      throw err;
    }
  };

  const doReenable = async () => {
    try {
      await reenable({ variables: { id: hook.id } });
      await onChanged();
      toast.success(M.webhooks.reenabled);
    } catch (err) {
      toast.error(userMessage(err));
    }
  };

  return (
    <li
      data-testid="webhook-row"
      className={cn(
        "flex flex-col gap-3 border-b border-rule px-4 py-4 last:border-b-0",
        auto && "bg-status-failed-soft/40",
      )}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{hook.label}</span>
            <StatePill on={s.enabled}>
              {s.enabled
                ? M.webhooks.enabled
                : auto
                  ? M.webhooks.autoDisabled
                  : M.webhooks.disabled}
            </StatePill>
          </div>
          <span className="truncate font-mono text-xs text-muted-foreground">
            {hook.displayUrl}
          </span>
          <span className="font-mono text-2xs text-muted-foreground/80">
            {M.webhooks.addedAt(formatRelative(hook.createdAt))}
            {failures > 0 ? ` · ${M.webhooks.failures(failures)}` : ""}
          </span>
        </div>
        <div
          role="group"
          aria-label={M.webhooks.actions(hook.label)}
          className="flex flex-wrap items-center gap-1"
        >
          <Button size="sm" variant="outline" disabled={testing} onClick={() => void runTest()}>
            {testing ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <Send aria-hidden="true" />
            )}
            {testing ? M.webhooks.testing : M.webhooks.test}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setConfirm("rotate");
            }}
          >
            <KeyRound aria-hidden="true" /> {M.webhooks.rotate}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="text-destructive hover:text-destructive"
            onClick={() => {
              setConfirm("delete");
            }}
          >
            <Trash2 aria-hidden="true" /> {M.webhooks.delete}
          </Button>
        </div>
      </div>

      {auto ? (
        <div
          role="alert"
          className="flex flex-wrap items-center justify-between gap-3 rounded-sm border border-status-failed/40 bg-status-failed-soft px-3 py-2.5 text-sm text-status-failed-fg"
        >
          <div className="flex flex-col gap-0.5">
            <span className="font-medium">{s.disabledMessage ?? M.webhooks.autoDisabled}</span>
            {s.lastFailureCode && s.lastFailureAt ? (
              <span className="font-mono text-2xs">
                {M.webhooks.lastFailure(s.lastFailureCode, formatRelative(s.lastFailureAt))}
              </span>
            ) : null}
          </div>
          <Button
            size="sm"
            variant="outline"
            disabled={reenabling}
            onClick={() => void doReenable()}
          >
            <Power aria-hidden="true" /> {M.webhooks.reenable}
          </Button>
        </div>
      ) : s.lastFailureCode && s.lastFailureAt && failures > 0 ? (
        <p className="font-mono text-2xs text-status-partial-fg">
          {M.webhooks.lastFailure(s.lastFailureCode, formatRelative(s.lastFailureAt))}
        </p>
      ) : null}

      {result ? <TestOutcome result={result} /> : null}

      <ConfirmDialog
        open={confirm === "rotate"}
        onOpenChange={(o) => {
          if (!o) setConfirm(null);
        }}
        title={M.webhooks.rotateTitle(hook.label)}
        description={M.webhooks.rotateBody}
        confirmLabel={M.webhooks.rotate}
        onConfirm={doRotate}
      />
      <ConfirmDialog
        open={confirm === "delete"}
        onOpenChange={(o) => {
          if (!o) setConfirm(null);
        }}
        title={M.webhooks.deleteTitle(hook.label)}
        description={M.webhooks.deleteBody}
        confirmLabel={M.webhooks.delete}
        destructive
        onConfirm={doDelete}
      />
    </li>
  );
}

/** Personal channels: the e-mail address and up to 5 webhooks (D-191). */
export function ChannelsSection() {
  const client = useApolloClient();
  const { data, loading, error } = useQuery(MyNotificationChannelsQuery, {
    fetchPolicy: "cache-and-network",
  });
  const [adding, setAdding] = useState(false);
  const [issued, setIssued] = useState<IssuedKey | null>(null);
  // The matrix shows whether a webhook is active: reload both.
  const reload = () =>
    client
      .refetchQueries({ include: [MyNotificationChannelsQuery, MyNotificationPreferencesQuery] })
      .catch(() => undefined);

  if (!data) {
    if (error) {
      return (
        <Section icon={Webhook} title={M.webhooks.heading}>
          <p className="text-sm text-destructive">{userMessage(error)}</p>
        </Section>
      );
    }
    return loading ? <Skeleton className="my-8 h-40" /> : null;
  }

  const ch = data.myNotificationChannels;
  const hooks = ch.webhooks.map((w) => getFragmentData(PersonalWebhookFields, w));
  const full = hooks.length >= ch.maxWebhooks;

  return (
    <>
      <EmailSection email={ch.email} verified={ch.emailVerified} verifiedAt={ch.emailVerifiedAt} />
      <Section
        icon={Webhook}
        title={M.webhooks.heading}
        lead={M.webhooks.lead}
        status={
          <span className="font-mono text-2xs text-muted-foreground tabular-nums">
            {M.webhooks.count(hooks.length, ch.maxWebhooks)}
          </span>
        }
      >
        {hooks.length === 0 ? (
          <p className="rounded-md border border-dashed px-4 py-5 text-sm text-muted-foreground">
            {M.webhooks.none}
          </p>
        ) : (
          <ul className="overflow-hidden rounded-md border bg-card">
            {hooks.map((h) => (
              <WebhookRow key={h.id} hook={h} onRotated={setIssued} onChanged={reload} />
            ))}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <Button
            size="sm"
            disabled={full}
            onClick={() => {
              setAdding(true);
            }}
          >
            <Plus aria-hidden="true" /> {M.webhooks.add}
          </Button>
          {full ? (
            <span className="text-xs text-muted-foreground">
              {M.webhooks.limit(ch.maxWebhooks)}
            </span>
          ) : null}
        </div>
      </Section>
      <AddWebhookDialog
        open={adding}
        onOpenChange={setAdding}
        onAdded={(k) => {
          setIssued(k);
          void reload();
        }}
      />
      <SigningKeyDialog
        issued={issued}
        onDone={() => {
          setIssued(null);
        }}
      />
    </>
  );
}
