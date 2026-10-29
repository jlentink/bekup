import { useMutation, useQuery } from "@apollo/client/react";
import {
  Download,
  FileKey2,
  KeyRound,
  Loader2,
  LockOpen,
  ShieldAlert,
  TriangleAlert,
} from "lucide-react";
import { useId, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Section } from "@/features/account/section";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import { Pager } from "@/features/jobs/pager";
import { useCursorPager } from "@/features/jobs/use-cursor-pager";
import type { AdminJobsQuery } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { downloadText } from "@/lib/download";
import { messages } from "@/lib/messages";
import { ExportJobPassphrasesMutation } from "@/lib/operations";

import { JobsQuery } from "../operations";
import { docs } from "../docs";
import { Command, DocLink } from "../ui";

type Job = AdminJobsQuery["jobs"]["edges"][number]["node"];

const T = {
  title: "Recovery",
  lead: "Escrow what you need to verify and decrypt backups without bekup: the instance public keys and every job's passphrases. Keep the copies offline, away from this host and the backups.",
  guide: "Disaster recovery guide",
  keysTitle: "Instance public keys",
  keysLead:
    "Needed to verify manifest signatures offline. Not secret, but integrity-relevant: record the printed fingerprints separately (for example on a printed escrow sheet).",
  keysRun: "Run on the bekup host (needs the database, not the keyring):",
  keysAgain:
    "Export again whenever the command shows a key you have not escrowed. Retired keys stay valid for the manifests they signed: keep every key.",
  passTitle: "Job passphrases",
  passLead:
    "One export per passphrase-encrypted job, with every passphrase version. Re-export after each passphrase change.",
  warnTitle: "An export is a decryption key",
  warn: "Anyone holding the file can decrypt all of the job's passphrase-encrypted backups. Every export is audited and raises a security alert to all admins.",
  colJob: "Job",
  colKey: "Key",
  colAction: "Export",
  passphrase: (v: number) => `Passphrase v${String(v)}`,
  since: (when: string) => `since ${when}`,
  recipient: "age recipient",
  recipientHint: "Escrow the age identity yourself; bekup never stored it.",
  unencrypted: "Unencrypted",
  notConfigured: "No key",
  export: "Export",
  exportLabel: (name: string) => `Export the passphrases of ${name}`,
  none: "No jobs yet.",
  pager: "Job pages",
  dialogTitle: (name: string) => `Export the passphrases of ${name}?`,
  dialogLead:
    "The file holds every passphrase version of this job in clear text. It downloads straight to this device and is never shown on screen.",
  dialogPoints: [
    "Store it offline (password manager vault, encrypted USB stick in a safe). Never next to the backups.",
    "Delete the downloaded copy from this device after moving it.",
    "All admins get a security alert and the export is in the audit log.",
  ],
  acknowledge: "I understand that this file decrypts the job's backups",
  confirm: "Confirm identity and export",
  exported: (versions: number) =>
    `Passphrases exported (${String(versions)} version${versions === 1 ? "" : "s"}). Move the file offline now.`,
} as const;

/** Recovery escrow (admin, D-142/D-154/D-164): passphrase export and public keys. */
export function RecoveryPage() {
  const pager = useCursorPager("recovery-jobs");
  const { data, error, refetch } = useQuery(JobsQuery, {
    variables: { first: 50, after: pager.after },
    fetchPolicy: "cache-and-network",
  });
  const [exporting, setExporting] = useState<Job | null>(null);

  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-rule pb-4">
        <div className="flex max-w-prose flex-col gap-1">
          <h2 className="font-display text-3xl">{T.title}</h2>
          <p className="text-sm text-muted-foreground">{T.lead}</p>
        </div>
        <DocLink href={docs.recovery}>{T.guide}</DocLink>
      </div>

      <Section icon={FileKey2} title={T.keysTitle} lead={T.keysLead}>
        <p className="text-sm">{T.keysRun}</p>
        <Command>bekup keys export-public --out bekup-instance-keys.txt</Command>
        <p className="text-sm text-muted-foreground">{T.keysAgain}</p>
      </Section>

      <Section icon={KeyRound} title={T.passTitle} lead={T.passLead}>
        <Alert className="border-status-failed/45">
          <ShieldAlert aria-hidden="true" />
          <AlertTitle>{T.warnTitle}</AlertTitle>
          <AlertDescription>{T.warn}</AlertDescription>
        </Alert>
        {error && !data ? (
          <Alert variant="destructive">
            <AlertDescription className="flex flex-wrap items-center gap-3">
              {userMessage(error)}
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  void refetch().catch(() => undefined);
                }}
              >
                {messages.common.retry}
              </Button>
            </AlertDescription>
          </Alert>
        ) : !data ? (
          <div className="flex flex-col gap-2" aria-busy="true">
            <Skeleton className="h-11 w-full" />
            <Skeleton className="h-11 w-full" />
          </div>
        ) : data.jobs.edges.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.none}</p>
        ) : (
          <>
            <div className="overflow-x-auto rounded-lg border">
              <table className="w-full min-w-[36rem] text-sm">
                <thead>
                  <tr className="border-b border-rule text-left">
                    <th scope="col" className="eyebrow px-4 py-2 font-normal">
                      {T.colJob}
                    </th>
                    <th scope="col" className="eyebrow px-3 py-2 font-normal">
                      {T.colKey}
                    </th>
                    <th scope="col" className="eyebrow px-4 py-2 text-right font-normal">
                      {T.colAction}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {data.jobs.edges.map(({ node: j }) => (
                    <tr
                      key={j.id}
                      data-testid="recovery-job"
                      className="border-b border-rule/60 last:border-b-0"
                    >
                      <td className="px-4 py-2.5">
                        <Link to={`/jobs/${j.id}`} className="font-medium hover:underline">
                          {j.name}
                        </Link>
                        <span className="ml-2 font-mono text-2xs text-muted-foreground">
                          {j.slug}
                        </span>
                      </td>
                      <td className="px-3 py-2.5">
                        <KeyCell job={j} />
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        {j.encryption.passphrase ? (
                          <Button
                            variant="outline"
                            size="sm"
                            aria-label={T.exportLabel(j.name)}
                            onClick={() => {
                              setExporting(j);
                            }}
                          >
                            <Download aria-hidden="true" /> {T.export}
                          </Button>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pager
              page={pager.page}
              hasNext={data.jobs.pageInfo.hasNextPage}
              onPrev={pager.prev}
              onNext={() => {
                pager.next(data.jobs.pageInfo.endCursor);
              }}
              label={T.pager}
            />
          </>
        )}
      </Section>
      {exporting ? (
        <ExportDialog
          key={exporting.id}
          job={exporting}
          onClose={() => {
            setExporting(null);
          }}
        />
      ) : null}
    </div>
  );
}

function KeyCell({ job }: { job: Job }) {
  const e = job.encryption;
  if (e.optedOut || e.mode === "NONE") {
    return (
      <Tag tone="insecure">
        <LockOpen aria-hidden="true" className="size-3" /> {T.unencrypted}
      </Tag>
    );
  }
  if (e.mode === "RECIPIENT") {
    return (
      <span className="flex flex-col">
        <span>{T.recipient}</span>
        <span className="text-xs text-muted-foreground">{T.recipientHint}</span>
      </span>
    );
  }
  if (e.passphrase) {
    return (
      <span className="flex flex-col">
        <span className="font-mono text-xs">{T.passphrase(e.passphrase.version)}</span>
        <span className="text-xs text-muted-foreground">
          {T.since(formatDateTime(e.passphrase.createdAt))}
        </span>
      </span>
    );
  }
  return <span className="text-muted-foreground">{T.notConfigured}</span>;
}

function ExportDialog({ job, onClose }: { job: Job; onClose: () => void }) {
  const { withStepUp } = useStepUp();
  const [exportPassphrases] = useMutation(ExportJobPassphrasesMutation, {
    fetchPolicy: "no-cache",
  });
  const [ack, setAck] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const ackId = useId();

  const run = () => {
    setBusy(true);
    setError(undefined);
    withStepUp(() => exportPassphrases({ variables: { jobId: job.id } }))
      .then((r) => {
        const e = r.data?.exportJobPassphrases;
        if (!e) return;
        downloadText(e.fileName, e.content);
        toast.success(T.exported(e.versions.length));
        onClose();
      })
      .catch((err: unknown) => {
        if (!isStepUpCancelled(err)) setError(userMessage(err));
      })
      .finally(() => {
        setBusy(false);
      });
  };

  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o && !busy) onClose();
      }}
    >
      <DialogContent className="gap-5 sm:max-w-lg">
        <DialogHeader>
          <p className="eyebrow flex items-center gap-1.5 text-status-failed-fg">
            <TriangleAlert aria-hidden="true" className="size-3.5" /> Secret material
          </p>
          <DialogTitle className="font-display text-2xl font-normal">
            {T.dialogTitle(job.name)}
          </DialogTitle>
          <DialogDescription>{T.dialogLead}</DialogDescription>
        </DialogHeader>
        <ul className="grid gap-2 border-l-2 border-status-failed/50 pl-4 text-sm">
          {T.dialogPoints.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
        <label
          htmlFor={ackId}
          className="flex items-start gap-2.5 rounded-md border bg-surface p-3 text-sm"
        >
          <input
            id={ackId}
            type="checkbox"
            className="mt-0.5 size-4 accent-primary"
            checked={ack}
            onChange={(e) => {
              setAck(e.target.checked);
            }}
          />
          {T.acknowledge}
        </label>
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={busy}>
              {messages.common.cancel}
            </Button>
          </DialogClose>
          <Button variant="destructive" disabled={!ack || busy} onClick={run}>
            {busy ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <Download aria-hidden="true" />
            )}
            {T.confirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
