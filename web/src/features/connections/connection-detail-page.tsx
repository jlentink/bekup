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
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { RiskBadge } from "@/features/accepted-risks/risk-badge";
import { useSessionUser } from "@/features/auth/use-session-user";
import { useStepUp } from "@/features/auth/use-step-up";
import type { ConnectionDetailQuery as DetailResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { defineMessages } from "@/lib/i18n";

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

const T = defineMessages({
  en: {
    auth: { NONE: "None yet", PASSWORD: "Password", PRIVATE_KEY: "Private key" },
    anchor: "Anchor",
    privateCa: "Private CA",
    selfSigned: "Self-signed certificate",
    subject: "Subject",
    issuer: "Issuer",
    leafSpki: "Leaf SPKI",
    anyLeaf: "any leaf of this CA (rotation allowed)",
    expires: "Expires",
    pinned: "Pinned",
    pinMissing:
      "No certificate is pinned yet, so the connection cannot be used. Fetch the server's certificate and confirm it.",
    pinExpired:
      "The pinned certificate has expired: backups fail. Pin the server's new certificate.",
    pinExpiring: (when: string) =>
      `The pinned certificate expires ${when}. Stage the next certificate as a backup pin before the server switches.`,
    transport: "Transport security",
    repin: "Re-pin certificate",
    pin: "Pin certificate",
    stage: "Stage backup pin",
    changeMode: "Change TLS mode",
    optedOut:
      "An admin opted out of pinned TLS. This was audited and stays visible until TLS is pinned again; an accepted risk is no open issue.",
    currentPin: "Current pin",
    stagedPin: "Staged backup pin",
    clientCert: "Client certificate (public part)",
    copyClientCert: "Copy the client certificate",
    sshTunnel: "SSH tunnel",
    direct: "The database is reached directly from the runner.",
    editToTunnel: "Edit the connection to route it through an SSH bastion.",
    noTunnel: "No tunnel.",
    sshLead:
      "The runner reaches the database through this bastion. Its host key is pinned, never trusted automatically.",
    hostKeyPinned: "Host key pinned",
    hostKeyNotConfirmed: "Host key not confirmed",
    enabled: "Enabled",
    recheck: "Re-check host key",
    fetchConfirm: "Fetch and confirm host key",
    generate: "Generate keypair",
    bastion: "Bastion",
    signsIn: "Signs in with",
    withPassphrase: " (with passphrase)",
    hostKey: "Host key",
    notConfirmed: "Not confirmed: the tunnel cannot open",
    publicKey: "Public key to install on the bastion",
    copyPublicKey: "Copy the SSH public key",
    bastionSubject: "the SSH bastion",
    hostKeyPinnedToast: "Host key pinned.",
    connections: "Connections",
    loading: "Loading connection",
    retry: "Try again",
    notFound: "Connection not found",
    notFoundBody: "It was deleted or never existed.",
    edit: "Edit",
    ready: "Ready",
    notReady: (reason: string | null | undefined) => `Not ready${reason ? `: ${reason}` : ""}`,
    legacyVersion: "Legacy version",
    readOnly: "Read only",
    checks: "Checks",
    checksLead:
      "Run on the runner with the stored credentials. serve never connects to a database itself.",
    configuration: "Configuration",
    engine: "Engine",
    versionKey: "Version key",
    legacy: " (legacy)",
    clientImage: "Client image",
    endpoint: "Endpoint",
    user: "User",
    password: "Password",
    stored: "Stored",
    none: "None",
    concurrentRuns: "Concurrent runs",
    globalDefault: " (global default)",
    capped: (n: number) => ` (set to ${n}, capped by the global limit)`,
    setHere: " (set on this connection)",
    updated: "Updated",
    adminsOnly: "Credentials and the full configuration are visible to admins only.",
    delete: "Delete",
    deleteLead:
      "Soft delete: backups and the audit history stay. Jobs that use the connection block it.",
    deleteConnection: "Delete connection",
    deleted: (name: string) => `Connection "${name}" deleted.`,
  },
  nl: {
    auth: { NONE: "Nog niets", PASSWORD: "Wachtwoord", PRIVATE_KEY: "Privésleutel" },
    anchor: "Anker",
    privateCa: "Private CA",
    selfSigned: "Zelfondertekend certificaat",
    subject: "Onderwerp",
    issuer: "Uitgever",
    leafSpki: "Leaf SPKI",
    anyLeaf: "elk leaf-certificaat van deze CA (wisselen toegestaan)",
    expires: "Verloopt",
    pinned: "Vastgepind",
    pinMissing:
      "Er is nog geen certificaat vastgepind, dus de verbinding kan niet worden gebruikt. Haal het certificaat van de server op en bevestig het.",
    pinExpired:
      "Het vastgepinde certificaat is verlopen: back-ups mislukken. Pin het nieuwe certificaat van de server.",
    pinExpiring: (when: string) =>
      `Het vastgepinde certificaat verloopt ${when}. Zet het volgende certificaat klaar als reservepin voordat de server overschakelt.`,
    transport: "Transportbeveiliging",
    repin: "Certificaat opnieuw vastpinnen",
    pin: "Certificaat vastpinnen",
    stage: "Reservepin klaarzetten",
    changeMode: "TLS-modus wijzigen",
    optedOut:
      "Een beheerder heeft afgezien van vastgepinde TLS. Dat is vastgelegd in de auditlog en blijft zichtbaar totdat TLS weer is vastgepind; een geaccepteerd risico is geen open probleem.",
    currentPin: "Huidige pin",
    stagedPin: "Klaargezette reservepin",
    clientCert: "Clientcertificaat (publiek deel)",
    copyClientCert: "Het clientcertificaat kopiëren",
    sshTunnel: "SSH-tunnel",
    direct: "De runner bereikt de database rechtstreeks.",
    editToTunnel: "Bewerk de verbinding om haar via een SSH-bastionhost te leiden.",
    noTunnel: "Geen tunnel.",
    sshLead:
      "De runner bereikt de database via deze bastionhost. De hostsleutel is vastgepind en wordt nooit automatisch vertrouwd.",
    hostKeyPinned: "Hostsleutel vastgepind",
    hostKeyNotConfirmed: "Hostsleutel niet bevestigd",
    enabled: "Ingeschakeld",
    recheck: "Hostsleutel opnieuw controleren",
    fetchConfirm: "Hostsleutel ophalen en bevestigen",
    generate: "Sleutelpaar genereren",
    bastion: "Bastionhost",
    signsIn: "Logt in met",
    withPassphrase: " (met wachtwoordzin)",
    hostKey: "Hostsleutel",
    notConfirmed: "Niet bevestigd: de tunnel kan niet openen",
    publicKey: "Publieke sleutel om op de bastionhost te installeren",
    copyPublicKey: "De publieke SSH-sleutel kopiëren",
    bastionSubject: "de SSH-bastionhost",
    hostKeyPinnedToast: "Hostsleutel vastgepind.",
    connections: "Verbindingen",
    loading: "Verbinding laden",
    retry: "Opnieuw proberen",
    notFound: "Verbinding niet gevonden",
    notFoundBody: "Ze is verwijderd of heeft nooit bestaan.",
    edit: "Bewerken",
    ready: "Gereed",
    notReady: (reason: string | null | undefined) => `Niet gereed${reason ? `: ${reason}` : ""}`,
    legacyVersion: "Verouderde versie",
    readOnly: "Alleen lezen",
    checks: "Controles",
    checksLead:
      "Worden op de runner uitgevoerd met de opgeslagen inloggegevens. serve maakt zelf nooit verbinding met een database.",
    configuration: "Configuratie",
    engine: "Engine",
    versionKey: "Versiesleutel",
    legacy: " (verouderd)",
    clientImage: "Client-image",
    endpoint: "Eindpunt",
    user: "Gebruiker",
    password: "Wachtwoord",
    stored: "Opgeslagen",
    none: "Geen",
    concurrentRuns: "Gelijktijdige runs",
    globalDefault: " (globale standaard)",
    capped: (n: number) => ` (ingesteld op ${n}, begrensd door de globale limiet)`,
    setHere: " (ingesteld op deze verbinding)",
    updated: "Bijgewerkt",
    adminsOnly: "Inloggegevens en de volledige configuratie zijn alleen zichtbaar voor beheerders.",
    delete: "Verwijderen",
    deleteLead:
      "Zacht verwijderen: back-ups en de auditgeschiedenis blijven bewaard. Taken die de verbinding gebruiken, blokkeren het verwijderen.",
    deleteConnection: "Verbinding verwijderen",
    deleted: (name: string) => `Verbinding „${name}” verwijderd.`,
  },
});

const AUTH_LABEL = T.auth;

function PinFacts({ pin }: { pin: Pin }) {
  return (
    <Facts>
      <Fact term={T.anchor}>{pin.isCa ? T.privateCa : T.selfSigned}</Fact>
      <Fact term={T.subject} mono>
        {pin.subject}
      </Fact>
      <Fact term={T.issuer} mono>
        {pin.issuer}
      </Fact>
      <Fact term={T.leafSpki} mono>
        {pin.leafSpkiSha256 ?? T.anyLeaf}
      </Fact>
      <Fact term={T.expires}>{formatDateTime(pin.notAfter)}</Fact>
      <Fact term={T.pinned}>{formatDateTime(pin.pinnedAt)}</Fact>
    </Facts>
  );
}

function ExpiryAlert({ c }: { c: Connection }) {
  if (c.tlsMode !== "PINNED") return null;
  const s = c.pinStatus;
  if (s === "VALID" || s === "NOT_REQUIRED") return null;
  const text =
    s === "MISSING"
      ? T.pinMissing
      : s === "EXPIRED"
        ? T.pinExpired
        : T.pinExpiring(formatDateTime(c.pinExpiresAt));
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
      title={T.transport}
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
                <FileKey2 aria-hidden="true" /> {s?.tlsPin ? T.repin : T.pin}
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
                <CalendarClock aria-hidden="true" /> {T.stage}
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
              <ShieldHalf aria-hidden="true" /> {T.changeMode}
            </Button>
          </>
        ) : null
      }
    >
      <ExpiryAlert c={c} />
      {c.insecureTransport ? (
        <div className="flex flex-col items-start gap-2">
          <RiskBadge flag="insecure-transport" kind="CONNECTION_TLS" subjectId={c.id} />
          <p className="text-sm text-muted-foreground">{T.optedOut}</p>
        </div>
      ) : null}
      {s?.tlsPin ? (
        <div className="flex flex-col gap-2">
          <p className="eyebrow">{T.currentPin}</p>
          <PinFacts pin={s.tlsPin} />
        </div>
      ) : null}
      {s?.backupPin ? (
        <div className="flex flex-col gap-2 border-t border-rule pt-4">
          <p className="eyebrow">{T.stagedPin}</p>
          <PinFacts pin={s.backupPin} />
        </div>
      ) : null}
      {s?.clientCertificatePem ? (
        <div className="flex flex-col gap-2 border-t border-rule pt-4">
          <p className="eyebrow">{T.clientCert}</p>
          <CopyValue value={s.clientCertificatePem} label={T.copyClientCert} multiline />
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
      <LedgerSection title={T.sshTunnel} lead={T.direct}>
        <p className="text-sm text-muted-foreground">{isAdmin ? T.editToTunnel : T.noTunnel}</p>
      </LedgerSection>
    );
  }

  return (
    <LedgerSection
      title={T.sshTunnel}
      lead={T.sshLead}
      aside={
        ssh ? (
          ssh.hostKey ? (
            <Tag tone="success">{T.hostKeyPinned}</Tag>
          ) : (
            <Tag tone="danger">{T.hostKeyNotConfirmed}</Tag>
          )
        ) : (
          <Tag tone="info">{T.enabled}</Tag>
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
              {ssh?.hostKey ? T.recheck : T.fetchConfirm}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => {
                setDialog("keypair");
              }}
            >
              <KeyRound aria-hidden="true" /> {T.generate}
            </Button>
          </>
        ) : null
      }
    >
      {ssh ? (
        <>
          <Facts>
            <Fact term={T.bastion} mono>
              {ssh.user}@{ssh.host}:{ssh.port}
            </Fact>
            <Fact term={T.signsIn}>
              {AUTH_LABEL[ssh.authMethod]}
              {ssh.authMethod === "PRIVATE_KEY" && ssh.hasPassphrase ? T.withPassphrase : ""}
            </Fact>
            <Fact term={T.hostKey}>
              {ssh.hostKey ? (
                <PinnedHostKey hostKey={ssh.hostKey} />
              ) : (
                <span className="text-status-failed-fg">{T.notConfirmed}</span>
              )}
            </Fact>
          </Facts>
          {ssh.publicKey ? (
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">{T.publicKey}</p>
              <CopyValue value={ssh.publicKey} label={T.copyPublicKey} />
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
            subject={T.bastionSubject}
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
              toast.success(T.hostKeyPinnedToast);
            }}
          />
          <KeypairDialog
            open={dialog === "keypair"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
            subject={T.bastionSubject}
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
        <ArrowLeft aria-hidden="true" /> {T.connections}
      </Link>
    </Button>
  );

  if (!data && loading) return <DetailSkeleton label={T.loading} />;
  if (error && !data) {
    return (
      <div className="flex flex-col gap-6">
        {back}
        <Alert variant="destructive">
          <AlertDescription>
            {userMessage(error)}
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              {T.retry}
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
        <EmptyState icon={Trash2} title={T.notFound}>
          {T.notFoundBody}
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
                <Pencil aria-hidden="true" /> {T.edit}
              </Link>
            </Button>
          ) : null
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        {c.ready ? (
          <Tag tone="success">{T.ready}</Tag>
        ) : (
          <Tag tone="danger">{T.notReady(c.notReadyReason)}</Tag>
        )}
        {c.insecureTransport ? (
          <RiskBadge
            flag="insecure-transport"
            kind="CONNECTION_TLS"
            subjectId={c.id}
            showAction={false}
          />
        ) : null}
        {c.legacyVersion ? <Tag tone="warning">{T.legacyVersion}</Tag> : null}
        {!isAdmin ? <Tag tone="neutral">{T.readOnly}</Tag> : null}
      </div>

      <div className="flex flex-col">
        <LedgerSection title={T.checks} lead={T.checksLead}>
          <ConnectionChecks id={c.id} lastTest={c.lastTest} />
        </LedgerSection>

        <LedgerSection title={T.configuration}>
          <Facts>
            <Fact term={T.engine}>{ENGINE_LABEL[c.engine]}</Fact>
            <Fact term={T.versionKey} mono>
              {c.versionKey}
              {c.legacyVersion ? T.legacy : ""}
            </Fact>
            {s ? (
              <Fact term={T.clientImage} mono>
                {s.imageKey}
              </Fact>
            ) : null}
            <Fact term={T.endpoint} mono>
              {c.host}:{c.port}
            </Fact>
            {s ? (
              <>
                <Fact term={T.user} mono>
                  {s.username}
                </Fact>
                <Fact term={T.password}>
                  {s.hasPassword ? (
                    <Tag tone="success">{T.stored}</Tag>
                  ) : (
                    <Tag tone="neutral">{T.none}</Tag>
                  )}
                </Fact>
              </>
            ) : null}
            <Fact term={T.concurrentRuns}>
              <span className="font-mono">{c.effectiveMaxConcurrentRuns}</span>
              <span className="text-muted-foreground">
                {c.maxConcurrentRuns == null
                  ? T.globalDefault
                  : c.maxConcurrentRuns !== c.effectiveMaxConcurrentRuns
                    ? T.capped(c.maxConcurrentRuns)
                    : T.setHere}
              </span>
            </Fact>
            <Fact term={T.updated}>{formatDateTime(c.updatedAt)}</Fact>
          </Facts>
          {!s ? <p className="text-xs text-muted-foreground">{T.adminsOnly}</p> : null}
        </LedgerSection>

        <TransportSection c={c} isAdmin={isAdmin} />
        <SshSection c={c} isAdmin={isAdmin} />

        {isAdmin ? (
          <LedgerSection title={T.delete} lead={T.deleteLead}>
            <Button
              type="button"
              variant="outline"
              className="self-start text-destructive"
              onClick={() => {
                setDeleting(true);
              }}
            >
              <Trash2 aria-hidden="true" /> {T.deleteConnection}
            </Button>
            <DeleteDialog
              open={deleting}
              onOpenChange={setDeleting}
              kind="connection"
              name={c.name}
              onDelete={async () => {
                await withStepUp(() => remove({ variables: { id: c.id } }));
                toast.success(T.deleted(c.name));
                void navigate("/connections");
              }}
            />
          </LedgerSection>
        ) : null}
      </div>
    </div>
  );
}
