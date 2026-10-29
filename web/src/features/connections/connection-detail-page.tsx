import { useMutation, useQuery } from "@apollo/client/react";
import {
  ArrowLeft,
  CalendarClock,
  FileKey2,
  Fingerprint,
  KeyRound,
  Pencil,
  ShieldHalf,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import { CopyValue } from "@/components/copy-value";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { HostKeyDialog, PinnedHostKey } from "@/components/ssh/host-key-dialog";
import { KeypairDialog } from "@/components/ssh/keypair-dialog";
import { SecurityBadge } from "@/components/status/security-badge";
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useSessionUser } from "@/features/auth/use-session-user";
import { useStepUp } from "@/features/auth/use-step-up";
import type { ConnectionDetailQuery as DetailResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";

import { ConnectionChecks } from "./checks";
import { DeleteDialog } from "./delete-dialog";
import { ENGINE_LABEL, pinStatusMeta, TLS_MODE } from "./labels";
import {
  ConfirmSshHostKeyMutation,
  ConnectionDetailQuery,
  ConnectionsListQuery,
  DeleteConnectionMutation,
  FetchSshHostKeyMutation,
  GenerateConnectionSshKeypairMutation,
} from "./operations";
import { PinDialog } from "./pin-dialog";
import { TlsModeDialog } from "./tls-mode-dialog";
import { DetailSkeleton, Fact, Facts, LedgerSection } from "./ui";

type Connection = NonNullable<DetailResult["connection"]>;
type Pin = NonNullable<NonNullable<Connection["settings"]>["tlsPin"]>;

const AUTH_LABEL = { NONE: "None yet", PASSWORD: "Password", PRIVATE_KEY: "Private key" } as const;

function PinFacts({ pin }: { pin: Pin }) {
  return (
    <Facts>
      <Fact term="Anchor">{pin.isCa ? "Private CA" : "Self-signed certificate"}</Fact>
      <Fact term="Subject" mono>
        {pin.subject}
      </Fact>
      <Fact term="Issuer" mono>
        {pin.issuer}
      </Fact>
      <Fact term="Leaf SPKI" mono>
        {pin.leafSpkiSha256 ?? "any leaf of this CA (rotation allowed)"}
      </Fact>
      <Fact term="Expires">{formatDateTime(pin.notAfter)}</Fact>
      <Fact term="Pinned">{formatDateTime(pin.pinnedAt)}</Fact>
    </Facts>
  );
}

function ExpiryAlert({ c }: { c: Connection }) {
  if (c.tlsMode !== "PINNED") return null;
  const s = c.pinStatus;
  if (s === "VALID" || s === "NOT_REQUIRED") return null;
  const text =
    s === "MISSING"
      ? "No certificate is pinned yet, so the connection cannot be used. Fetch the server's certificate and confirm it."
      : s === "EXPIRED"
        ? "The pinned certificate has expired: backups fail. Pin the server's new certificate."
        : `The pinned certificate expires ${formatDateTime(c.pinExpiresAt)}. Stage the next certificate as a backup pin before the server switches.`;
  return (
    <Alert variant={s === "EXPIRES_WITHIN_30_DAYS" ? "default" : "destructive"}>
      <CalendarClock aria-hidden="true" />
      <AlertTitle>{pinStatusMeta(s).label}</AlertTitle>
      <AlertDescription>{text}</AlertDescription>
    </Alert>
  );
}

function TransportSection({ c, isAdmin }: { c: Connection; isAdmin: boolean }) {
  const [dialog, setDialog] = useState<"pin" | "stage" | "mode">();
  const pin = pinStatusMeta(c.pinStatus, c.pinExpiresAt);
  const s = c.settings;
  return (
    <LedgerSection
      title="Transport security"
      lead={TLS_MODE[c.tlsMode].description}
      aside={
        <>
          <Tag tone={c.tlsMode === "PINNED" ? "success" : "insecure"}>
            {TLS_MODE[c.tlsMode].label}
          </Tag>
          {c.tlsMode === "PINNED" ? (
            <Tag tone={pin.tone} {...(pin.detail ? { title: pin.detail } : {})}>
              {pin.label}
            </Tag>
          ) : null}
        </>
      }
      actions={
        isAdmin ? (
          <>
            {c.tlsMode === "PINNED" ? (
              <Button
                type="button"
                size="sm"
                onClick={() => {
                  setDialog("pin");
                }}
              >
                <FileKey2 aria-hidden="true" />{" "}
                {s?.tlsPin ? "Re-pin certificate" : "Pin certificate"}
              </Button>
            ) : null}
            {c.tlsMode === "PINNED" && s?.tlsPin ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => {
                  setDialog("stage");
                }}
              >
                <CalendarClock aria-hidden="true" /> Stage backup pin
              </Button>
            ) : null}
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => {
                setDialog("mode");
              }}
            >
              <ShieldHalf aria-hidden="true" /> Change TLS mode
            </Button>
          </>
        ) : null
      }
    >
      <ExpiryAlert c={c} />
      {c.insecureTransport ? (
        <div className="flex flex-col items-start gap-2">
          <SecurityBadge flag="insecure-transport" />
          <p className="text-sm text-muted-foreground">
            An admin opted out of pinned TLS. This was audited and stays visible until TLS is pinned
            again.
          </p>
        </div>
      ) : null}
      {s?.tlsPin ? (
        <div className="flex flex-col gap-2">
          <p className="eyebrow">Current pin</p>
          <PinFacts pin={s.tlsPin} />
        </div>
      ) : null}
      {s?.backupPin ? (
        <div className="flex flex-col gap-2 border-t border-rule pt-4">
          <p className="eyebrow">Staged backup pin</p>
          <PinFacts pin={s.backupPin} />
        </div>
      ) : null}
      {s?.clientCertificatePem ? (
        <div className="flex flex-col gap-2 border-t border-rule pt-4">
          <p className="eyebrow">Client certificate (public part)</p>
          <CopyValue value={s.clientCertificatePem} label="Copy the client certificate" multiline />
        </div>
      ) : null}
      {isAdmin ? (
        <>
          <PinDialog
            id={c.id}
            host={c.host}
            mode={dialog === "stage" ? "stage" : "pin"}
            open={dialog === "pin" || dialog === "stage"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
          />
          <TlsModeDialog
            key={c.tlsMode}
            id={c.id}
            name={c.name}
            current={c.tlsMode}
            open={dialog === "mode"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
          />
        </>
      ) : null}
    </LedgerSection>
  );
}

function SshSection({ c, isAdmin }: { c: Connection; isAdmin: boolean }) {
  const { withStepUp } = useStepUp();
  const refetch = { refetchQueries: [ConnectionDetailQuery], awaitRefetchQueries: true };
  const [fetchKey] = useMutation(FetchSshHostKeyMutation);
  const [confirmKey] = useMutation(ConfirmSshHostKeyMutation, refetch);
  const [generate] = useMutation(GenerateConnectionSshKeypairMutation, refetch);
  const [dialog, setDialog] = useState<"hostkey" | "keypair">();
  const ssh = c.settings?.ssh;

  if (!c.sshEnabled) {
    return (
      <LedgerSection title="SSH tunnel" lead="The database is reached directly from the runner.">
        <p className="text-sm text-muted-foreground">
          {isAdmin ? "Edit the connection to route it through an SSH bastion." : "No tunnel."}
        </p>
      </LedgerSection>
    );
  }

  return (
    <LedgerSection
      title="SSH tunnel"
      lead="The runner reaches the database through this bastion. Its host key is pinned, never trusted automatically."
      aside={
        ssh ? (
          ssh.hostKey ? (
            <Tag tone="success">Host key pinned</Tag>
          ) : (
            <Tag tone="danger">Host key not confirmed</Tag>
          )
        ) : (
          <Tag tone="info">Enabled</Tag>
        )
      }
      actions={
        isAdmin ? (
          <>
            <Button
              type="button"
              size="sm"
              variant={ssh?.hostKey ? "outline" : "default"}
              onClick={() => {
                setDialog("hostkey");
              }}
            >
              <Fingerprint aria-hidden="true" />
              {ssh?.hostKey ? "Re-check host key" : "Fetch and confirm host key"}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => {
                setDialog("keypair");
              }}
            >
              <KeyRound aria-hidden="true" /> Generate keypair
            </Button>
          </>
        ) : null
      }
    >
      {ssh ? (
        <>
          <Facts>
            <Fact term="Bastion" mono>
              {ssh.user}@{ssh.host}:{ssh.port}
            </Fact>
            <Fact term="Signs in with">
              {AUTH_LABEL[ssh.authMethod]}
              {ssh.authMethod === "PRIVATE_KEY" && ssh.hasPassphrase ? " (with passphrase)" : ""}
            </Fact>
            <Fact term="Host key">
              {ssh.hostKey ? (
                <PinnedHostKey hostKey={ssh.hostKey} />
              ) : (
                <span className="text-status-failed-fg">Not confirmed: the tunnel cannot open</span>
              )}
            </Fact>
          </Facts>
          {ssh.publicKey ? (
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">Public key to install on the bastion</p>
              <CopyValue value={ssh.publicKey} label="Copy the SSH public key" />
            </div>
          ) : null}
        </>
      ) : null}
      {isAdmin ? (
        <>
          <HostKeyDialog
            open={dialog === "hostkey"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
            subject="the SSH bastion"
            pinned={ssh?.hostKey}
            onFetch={async () => {
              const r = await fetchKey({ variables: { id: c.id } });
              if (!r.data) throw new Error("no host key");
              return r.data.fetchSshHostKey;
            }}
            onConfirm={async (key, host, port) => {
              await withStepUp(() =>
                confirmKey({
                  variables: {
                    id: c.id,
                    authorizedKey: key.authorizedKey,
                    expectedHost: host,
                    expectedPort: port,
                  },
                }),
              );
              toast.success("Host key pinned.");
            }}
          />
          <KeypairDialog
            open={dialog === "keypair"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
            subject="the SSH bastion"
            replaces={ssh ? ssh.authMethod !== "NONE" : false}
            onGenerate={async () => {
              const r = await withStepUp(() => generate({ variables: { id: c.id } }));
              if (!r.data) throw new Error("no keypair");
              return r.data.generateConnectionSshKeypair;
            }}
          />
        </>
      ) : null}
    </LedgerSection>
  );
}

/** /connections/:id */
export function ConnectionDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { role } = useSessionUser();
  const isAdmin = role === "ADMIN";
  const { withStepUp } = useStepUp();
  const { data, loading, error, refetch } = useQuery(ConnectionDetailQuery, {
    variables: { id },
    fetchPolicy: "cache-and-network",
  });
  const [remove] = useMutation(DeleteConnectionMutation, {
    refetchQueries: [ConnectionsListQuery],
  });
  const [deleting, setDeleting] = useState(false);

  const back = (
    <Button asChild variant="ghost" size="sm" className="self-start">
      <Link to="/connections">
        <ArrowLeft aria-hidden="true" /> Connections
      </Link>
    </Button>
  );

  if (!data && loading) return <DetailSkeleton label="Loading connection" />;
  if (error && !data) {
    return (
      <div className="flex flex-col gap-6">
        {back}
        <Alert variant="destructive">
          <AlertDescription>
            {userMessage(error)}
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Try again
            </Button>
          </AlertDescription>
        </Alert>
      </div>
    );
  }
  const c = data?.connection;
  if (!c) {
    return (
      <div className="flex flex-col gap-6">
        {back}
        <EmptyState icon={Trash2} title="Connection not found">
          It was deleted or never existed.
        </EmptyState>
      </div>
    );
  }
  const s = c.settings;

  return (
    <div className="flex flex-col gap-6">
      {back}
      <PageHeader
        eyebrow={`${ENGINE_LABEL[c.engine]} ${c.versionKey}`}
        title={c.name}
        description={
          <span className="font-mono text-xs">
            {c.host}:{c.port}
          </span>
        }
        actions={
          isAdmin ? (
            <Button asChild variant="outline">
              <Link to={`/connections/${c.id}/edit`}>
                <Pencil aria-hidden="true" /> Edit
              </Link>
            </Button>
          ) : null
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        {c.ready ? (
          <Tag tone="success">Ready</Tag>
        ) : (
          <Tag tone="danger">Not ready{c.notReadyReason ? `: ${c.notReadyReason}` : ""}</Tag>
        )}
        {c.insecureTransport ? <SecurityBadge flag="insecure-transport" /> : null}
        {c.legacyVersion ? <Tag tone="warning">Legacy version</Tag> : null}
        {!isAdmin ? <Tag tone="neutral">Read only</Tag> : null}
      </div>

      <div className="flex flex-col">
        <LedgerSection
          title="Checks"
          lead="Run on the runner with the stored credentials. serve never connects to a database itself."
        >
          <ConnectionChecks id={c.id} lastTest={c.lastTest} />
        </LedgerSection>

        <LedgerSection title="Configuration">
          <Facts>
            <Fact term="Engine">{ENGINE_LABEL[c.engine]}</Fact>
            <Fact term="Version key" mono>
              {c.versionKey}
              {c.legacyVersion ? " (legacy)" : ""}
            </Fact>
            {s ? (
              <Fact term="Client image" mono>
                {s.imageKey}
              </Fact>
            ) : null}
            <Fact term="Endpoint" mono>
              {c.host}:{c.port}
            </Fact>
            {s ? (
              <>
                <Fact term="User" mono>
                  {s.username}
                </Fact>
                <Fact term="Password">
                  {s.hasPassword ? (
                    <Tag tone="success">Stored</Tag>
                  ) : (
                    <Tag tone="neutral">None</Tag>
                  )}
                </Fact>
              </>
            ) : null}
            <Fact term="Concurrent runs">
              <span className="font-mono">{c.effectiveMaxConcurrentRuns}</span>
              <span className="text-muted-foreground">
                {c.maxConcurrentRuns == null
                  ? " (global default)"
                  : c.maxConcurrentRuns !== c.effectiveMaxConcurrentRuns
                    ? ` (set to ${c.maxConcurrentRuns}, capped by the global limit)`
                    : " (set on this connection)"}
              </span>
            </Fact>
            <Fact term="Updated">{formatDateTime(c.updatedAt)}</Fact>
          </Facts>
          {!s ? (
            <p className="text-xs text-muted-foreground">
              Credentials and the full configuration are visible to admins only.
            </p>
          ) : null}
        </LedgerSection>

        <TransportSection c={c} isAdmin={isAdmin} />
        <SshSection c={c} isAdmin={isAdmin} />

        {isAdmin ? (
          <LedgerSection
            title="Delete"
            lead="Soft delete: backups and the audit history stay. Jobs that use the connection block it."
          >
            <Button
              type="button"
              variant="outline"
              className="self-start text-destructive"
              onClick={() => {
                setDeleting(true);
              }}
            >
              <Trash2 aria-hidden="true" /> Delete connection
            </Button>
            <DeleteDialog
              open={deleting}
              onOpenChange={setDeleting}
              kind="connection"
              name={c.name}
              onDelete={async () => {
                await withStepUp(() => remove({ variables: { id: c.id } }));
                toast.success(`Connection "${c.name}" deleted.`);
                void navigate("/connections");
              }}
            />
          </LedgerSection>
        ) : null}
      </div>
    </div>
  );
}
