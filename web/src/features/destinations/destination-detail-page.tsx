import { useMutation, useQuery } from "@apollo/client/react";
import { ArrowLeft, Fingerprint, KeyRound, Pencil, Trash2 } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import { CopyValue } from "@/components/copy-value";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { HostKeyDialog, PinnedHostKey } from "@/components/ssh/host-key-dialog";
import { KeypairDialog } from "@/components/ssh/keypair-dialog";
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useSessionUser } from "@/features/auth/use-session-user";
import { useStepUp } from "@/features/auth/use-step-up";
import { DeleteDialog } from "@/features/connections/delete-dialog";
import { DetailSkeleton, Fact, Facts, LedgerSection } from "@/features/connections/ui";
import type { DestinationDetailQuery as DetailResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { defineMessages, plural } from "@/lib/i18n";

import {
  CHECKSUM_LABEL,
  ENCRYPTION_LABEL,
  LOCK_DESCRIPTION,
  LOCK_LABEL,
  TYPE_LABEL,
} from "./labels";
import {
  ConfirmDestinationHostKeyMutation,
  DeleteDestinationMutation,
  DestinationDetailQuery,
  DestinationsListQuery,
  FetchDestinationHostKeyMutation,
  GenerateDestinationSshKeypairMutation,
} from "./operations";
import { DestinationStorageSection } from "./destination-storage";
import { DestinationTest } from "./test-panel";
import { UnrecordedFilesBanner, UnrecordedFilesSection } from "./unrecorded-files";
import { WebdavTlsSection } from "./webdav-certificate";
import { FLAVOR_LABEL, WT } from "./webdav-text";

type Destination = NonNullable<DetailResult["destination"]>;
type Settings = NonNullable<Destination["settings"]>;

const D = defineMessages({
  en: {
    auth: { NONE: "None yet", PASSWORD: "Password", PRIVATE_KEY: "Private key" },
    stored: "Stored",
    none: "None",
    endpoint: "Endpoint",
    awsS3: "AWS S3",
    region: "Region",
    bucket: "Bucket",
    prefix: "Prefix",
    bucketRoot: "(bucket root)",
    addressing: "Addressing",
    pathStyle: "Path style",
    virtualHosted: "Virtual-hosted style",
    encryption: "Encryption",
    storageClass: "Storage class",
    checksums: "Checksums",
    overwrites: "Overwrites",
    overwritesRefused: "Refused atomically (If-None-Match)",
    overwritesAllowed: "Not prevented by the store",
    accessKeyId: "Access key ID",
    secretKey: "Secret key",
    caBundle: "Custom CA bundle",
    copyCaBundle: "Copy the CA bundle",
    sshTitle: "SSH",
    sshLead:
      "The server's host key is pinned, never trusted automatically. Prefer a generated key over a password.",
    hostKeyPinned: "Host key pinned",
    hostKeyNotConfirmed: "Host key not confirmed",
    recheckHostKey: "Re-check host key",
    fetchHostKey: "Fetch and confirm host key",
    generateKeypair: "Generate keypair",
    signsInWith: "Signs in with",
    withPassphrase: " (with passphrase)",
    hostKey: "Host key",
    notConfirmed: "Not confirmed: uploads cannot start",
    publicKey: "Public key to install on the server",
    copyPublicKey: "Copy the SSH public key",
    sftpSubject: "the SFTP server",
    hostKeyPinnedToast: "Host key pinned.",
    destinations: "Destinations",
    loading: "Loading destination",
    retry: "Try again",
    notFound: "Destination not found",
    notFoundBody: "It was deleted or never existed.",
    eyebrow: (type: string) => `${type} destination`,
    edit: "Edit",
    ready: "Ready",
    notReady: "Not ready",
    notReadyReason: (reason: string) => `Not ready: ${reason}`,
    objectLockTag: (mode: string) => `Object Lock · ${mode}`,
    objectLockTagDays: (mode: string, days: number) => `Object Lock · ${mode} · ${days} days`,
    noDeleteTag: "No-delete",
    readOnly: "Read only",
    testTitle: "Test",
    testLead: "Writes, reads back and deletes a small probe object below the destination's prefix.",
    retentionTitle: "Retention",
    retentionNoDelete:
      "bekup never deletes from this destination: configure lifecycle rules for retention.",
    retentionLocked: "bekup applies retention once an object's lock has expired.",
    retentionNormal: "bekup deletes backups here according to each job's retention policy.",
    noDelete: "No-delete",
    on: "On",
    off: "Off",
    objectLock: "Object Lock",
    lockValue: (mode: string, days: string) => `${mode}, ${days} days`,
    configuration: "Configuration",
    directory: "Directory",
    server: "Server",
    basePath: "Base path",
    basePathDefault: "(base path)",
    password: "Password",
    privateKey: "Private key",
    updated: (when: string) => `Updated ${when}`,
    adminOnly: "The full configuration is visible to admins only.",
    deleteTitle: "Delete",
    deleteLead:
      "Soft delete: the stored backups stay where they are. Jobs that write here block it.",
    deleteButton: "Delete destination",
    deleted: (name: string) => `Destination "${name}" deleted.`,
  },
  nl: {
    auth: { NONE: "Nog geen", PASSWORD: "Wachtwoord", PRIVATE_KEY: "Privésleutel" },
    stored: "Opgeslagen",
    none: "Geen",
    endpoint: "Endpoint",
    awsS3: "AWS S3",
    region: "Regio",
    bucket: "Bucket",
    prefix: "Prefix",
    bucketRoot: "(root van de bucket)",
    addressing: "Adressering",
    pathStyle: "Path style",
    virtualHosted: "Virtual-hosted style",
    encryption: "Versleuteling",
    storageClass: "Opslagklasse",
    checksums: "Checksums",
    overwrites: "Overschrijven",
    overwritesRefused: "Atomair geweigerd (If-None-Match)",
    overwritesAllowed: "Niet verhinderd door de opslag",
    accessKeyId: "Access key ID",
    secretKey: "Secret key",
    caBundle: "Eigen CA-bundel",
    copyCaBundle: "CA-bundel kopiëren",
    sshTitle: "SSH",
    sshLead:
      "De hostsleutel van de server is vastgepind en wordt nooit automatisch vertrouwd. Gebruik liever een gegenereerde sleutel dan een wachtwoord.",
    hostKeyPinned: "Hostsleutel vastgepind",
    hostKeyNotConfirmed: "Hostsleutel niet bevestigd",
    recheckHostKey: "Hostsleutel opnieuw controleren",
    fetchHostKey: "Hostsleutel ophalen en bevestigen",
    generateKeypair: "Sleutelpaar genereren",
    signsInWith: "Logt in met",
    withPassphrase: " (met wachtwoordzin)",
    hostKey: "Hostsleutel",
    notConfirmed: "Niet bevestigd: uploads kunnen niet starten",
    publicKey: "Publieke sleutel om op de server te installeren",
    copyPublicKey: "Publieke SSH-sleutel kopiëren",
    sftpSubject: "de SFTP-server",
    hostKeyPinnedToast: "Hostsleutel vastgepind.",
    destinations: "Bestemmingen",
    loading: "Bestemming laden",
    retry: "Opnieuw proberen",
    notFound: "Bestemming niet gevonden",
    notFoundBody: "Ze is verwijderd of heeft nooit bestaan.",
    eyebrow: (type: string) => `Bestemming: ${type}`,
    edit: "Bewerken",
    ready: "Gereed",
    notReady: "Niet gereed",
    notReadyReason: (reason: string) => `Niet gereed: ${reason}`,
    objectLockTag: (mode: string) => `Object Lock · ${mode}`,
    objectLockTagDays: (mode: string, days: number) =>
      `Object Lock · ${mode} · ${plural(days, { one: "1 dag", other: `${days} dagen` })}`,
    noDeleteTag: "Niet verwijderen",
    readOnly: "Alleen lezen",
    testTitle: "Test",
    testLead:
      "Schrijft een klein testobject onder de prefix van de bestemming, leest het terug en verwijdert het.",
    retentionTitle: "Bewaarbeleid",
    retentionNoDelete:
      "bekup verwijdert nooit iets van deze bestemming: stel lifecycle-regels in voor het bewaarbeleid.",
    retentionLocked:
      "bekup past het bewaarbeleid toe zodra de vergrendeling van een object is verlopen.",
    retentionNormal: "bekup verwijdert hier back-ups volgens het bewaarbeleid van elke taak.",
    noDelete: "Niet verwijderen",
    on: "Aan",
    off: "Uit",
    objectLock: "Object Lock",
    lockValue: (mode: string, days: string) => `${mode}, ${days} dagen`,
    configuration: "Configuratie",
    directory: "Map",
    server: "Server",
    basePath: "Basispad",
    basePathDefault: "(basispad)",
    password: "Wachtwoord",
    privateKey: "Privésleutel",
    updated: (when: string) => `Bijgewerkt ${when}`,
    adminOnly: "De volledige configuratie is alleen zichtbaar voor beheerders.",
    deleteTitle: "Verwijderen",
    deleteLead:
      "Zachte verwijdering: de opgeslagen back-ups blijven staan. Taken die hierheen schrijven blokkeren het.",
    deleteButton: "Bestemming verwijderen",
    deleted: (name: string) => `Bestemming „${name}” verwijderd.`,
  },
});

const AUTH_LABEL = D.auth;

function stored(has: boolean) {
  return has ? <Tag tone="success">{D.stored}</Tag> : <Tag tone="neutral">{D.none}</Tag>;
}

function S3Facts({ s3 }: { s3: NonNullable<Settings["s3"]> }) {
  return (
    <>
      <Facts>
        <Fact term={D.endpoint} mono>
          {s3.endpoint ?? D.awsS3}
        </Fact>
        <Fact term={D.region} mono>
          {s3.region}
        </Fact>
        <Fact term={D.bucket} mono>
          {s3.bucket}
        </Fact>
        <Fact term={D.prefix} mono>
          {s3.prefix || D.bucketRoot}
        </Fact>
        <Fact term={D.addressing}>{s3.pathStyle ? D.pathStyle : D.virtualHosted}</Fact>
        <Fact term={D.encryption}>
          {ENCRYPTION_LABEL[s3.encryption]}
          {s3.kmsKeyId ? <span className="ml-2 font-mono text-xs">{s3.kmsKeyId}</span> : null}
        </Fact>
        {s3.storageClass ? (
          <Fact term={D.storageClass} mono>
            {s3.storageClass}
          </Fact>
        ) : null}
        <Fact term={D.checksums}>{CHECKSUM_LABEL[s3.checksumMode]}</Fact>
        <Fact term={D.overwrites}>
          {s3.conditionalWrites ? D.overwritesRefused : D.overwritesAllowed}
        </Fact>
        <Fact term={D.accessKeyId} mono>
          {s3.accessKeyId}
        </Fact>
        <Fact term={D.secretKey}>{stored(s3.hasSecretAccessKey)}</Fact>
      </Facts>
      {s3.caBundlePem ? (
        <div className="flex flex-col gap-1.5">
          <p className="eyebrow">{D.caBundle}</p>
          <CopyValue value={s3.caBundlePem} label={D.copyCaBundle} multiline />
        </div>
      ) : null}
    </>
  );
}

function WebdavFacts({ webdav }: { webdav: NonNullable<Settings["webdav"]> }) {
  return (
    <>
      <Facts>
        <Fact term={WT.facts.url} mono>
          {webdav.url}
        </Fact>
        <Fact term={WT.facts.flavor}>{FLAVOR_LABEL[webdav.flavor]}</Fact>
        <Fact term={WT.facts.path} mono>
          {webdav.path || (webdav.flavor === "NEXTCLOUD" ? WT.facts.pathFiles : WT.facts.pathBase)}
        </Fact>
        <Fact term={WT.facts.username} mono>
          {webdav.username}
        </Fact>
        <Fact term={WT.facts.password}>{stored(webdav.hasPassword)}</Fact>
        <Fact term={WT.facts.timeout}>{WT.facts.timeoutValue(webdav.timeoutSeconds)}</Fact>
      </Facts>
      {webdav.caBundlePem ? (
        <div className="flex flex-col gap-1.5">
          <p className="eyebrow">{WT.facts.caBundle}</p>
          <CopyValue value={webdav.caBundlePem} label={WT.facts.copyCaBundle} multiline />
        </div>
      ) : null}
    </>
  );
}

function SftpSection({ d, isAdmin }: { d: Destination; isAdmin: boolean }) {
  const { withStepUp } = useStepUp();
  const refetch = { refetchQueries: [DestinationDetailQuery], awaitRefetchQueries: true };
  const [fetchKey] = useMutation(FetchDestinationHostKeyMutation);
  const [confirmKey] = useMutation(ConfirmDestinationHostKeyMutation, refetch);
  const [generate] = useMutation(GenerateDestinationSshKeypairMutation, refetch);
  const [dialog, setDialog] = useState<"hostkey" | "keypair">();
  const sftp = d.settings?.sftp;
  if (!sftp) return null;
  return (
    <LedgerSection
      title={D.sshTitle}
      lead={D.sshLead}
      aside={
        sftp.hostKey ? (
          <Tag tone="success">{D.hostKeyPinned}</Tag>
        ) : (
          <Tag tone="danger">{D.hostKeyNotConfirmed}</Tag>
        )
      }
      actions={
        isAdmin ? (
          <>
            <Button
              type="button"
              size="sm"
              variant={sftp.hostKey ? "outline" : "default"}
              onClick={() => {
                setDialog("hostkey");
              }}
            >
              <Fingerprint aria-hidden="true" />
              {sftp.hostKey ? D.recheckHostKey : D.fetchHostKey}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => {
                setDialog("keypair");
              }}
            >
              <KeyRound aria-hidden="true" /> {D.generateKeypair}
            </Button>
          </>
        ) : null
      }
    >
      <Facts>
        <Fact term={D.signsInWith}>
          {AUTH_LABEL[sftp.authMethod]}
          {sftp.authMethod === "PRIVATE_KEY" && sftp.hasPassphrase ? D.withPassphrase : ""}
        </Fact>
        <Fact term={D.hostKey}>
          {sftp.hostKey ? (
            <PinnedHostKey hostKey={sftp.hostKey} />
          ) : (
            <span className="text-status-failed-fg">{D.notConfirmed}</span>
          )}
        </Fact>
      </Facts>
      {sftp.publicKey ? (
        <div className="flex flex-col gap-1.5">
          <p className="eyebrow">{D.publicKey}</p>
          <CopyValue value={sftp.publicKey} label={D.copyPublicKey} />
        </div>
      ) : null}
      {isAdmin ? (
        <>
          <HostKeyDialog
            open={dialog === "hostkey"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
            subject={D.sftpSubject}
            pinned={sftp.hostKey}
            onFetch={async () => {
              const r = await fetchKey({ variables: { id: d.id } });
              if (!r.data) throw new Error("no host key");
              return r.data.fetchDestinationHostKey;
            }}
            onConfirm={async (key, host, port) => {
              await withStepUp(() =>
                confirmKey({
                  variables: {
                    id: d.id,
                    authorizedKey: key.authorizedKey,
                    expectedHost: host,
                    expectedPort: port,
                  },
                }),
              );
              toast.success(D.hostKeyPinnedToast);
            }}
          />
          <KeypairDialog
            open={dialog === "keypair"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
            subject={D.sftpSubject}
            replaces={sftp.authMethod !== "NONE"}
            onGenerate={async () => {
              const r = await withStepUp(() => generate({ variables: { id: d.id } }));
              if (!r.data) throw new Error("no keypair");
              return r.data.generateDestinationSshKeypair;
            }}
          />
        </>
      ) : null}
    </LedgerSection>
  );
}

/** /destinations/:id */
export function DestinationDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { role } = useSessionUser();
  const isAdmin = role === "ADMIN";
  const { withStepUp } = useStepUp();
  const { data, loading, error, refetch } = useQuery(DestinationDetailQuery, {
    variables: { id },
    fetchPolicy: "cache-and-network",
  });
  const [remove] = useMutation(DeleteDestinationMutation, {
    refetchQueries: [DestinationsListQuery],
  });
  const [deleting, setDeleting] = useState(false);

  const back = (
    <Button asChild variant="ghost" size="sm" className="self-start">
      <Link to="/destinations">
        <ArrowLeft aria-hidden="true" /> {D.destinations}
      </Link>
    </Button>
  );

  if (!data && loading) return <DetailSkeleton label={D.loading} />;
  if (error && !data) {
    return (
      <div className="flex flex-col gap-6">
        {back}
        <Alert variant="destructive">
          <AlertDescription>
            {userMessage(error)}
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              {D.retry}
            </Button>
          </AlertDescription>
        </Alert>
      </div>
    );
  }
  const d = data?.destination;
  if (!d) {
    return (
      <div className="flex flex-col gap-6">
        {back}
        <EmptyState icon={Trash2} title={D.notFound}>
          {D.notFoundBody}
        </EmptyState>
      </div>
    );
  }
  const s = d.settings;

  return (
    <div className="flex flex-col gap-6">
      {back}
      <PageHeader
        eyebrow={D.eyebrow(TYPE_LABEL[d.type])}
        title={d.name}
        actions={
          isAdmin ? (
            <Button asChild variant="outline">
              <Link to={`/destinations/${d.id}/edit`}>
                <Pencil aria-hidden="true" /> {D.edit}
              </Link>
            </Button>
          ) : null
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        {d.ready ? (
          <Tag tone="success">{D.ready}</Tag>
        ) : (
          <Tag tone="danger">
            {d.notReadyReason ? D.notReadyReason(d.notReadyReason) : D.notReady}
          </Tag>
        )}
        {d.objectLockMode ? (
          <Tag tone="info" title={LOCK_DESCRIPTION[d.objectLockMode]}>
            {d.objectLockRetentionDays != null
              ? D.objectLockTagDays(LOCK_LABEL[d.objectLockMode], d.objectLockRetentionDays)
              : D.objectLockTag(LOCK_LABEL[d.objectLockMode])}
          </Tag>
        ) : null}
        {d.noDelete ? <Tag tone="info">{D.noDeleteTag}</Tag> : null}
        {!isAdmin ? <Tag tone="neutral">{D.readOnly}</Tag> : null}
      </div>
      <UnrecordedFilesBanner destinationId={d.id} enabled={isAdmin} />

      <div className="flex flex-col">
        <LedgerSection title={D.testTitle} lead={D.testLead}>
          <DestinationTest
            id={d.id}
            lastTest={d.lastTest}
            viewer={{ isAdmin, flavor: s?.webdav?.flavor }}
          />
        </LedgerSection>

        <DestinationStorageSection id={d.id} />

        <LedgerSection
          title={D.retentionTitle}
          lead={
            d.noDelete
              ? D.retentionNoDelete
              : d.objectLockMode
                ? D.retentionLocked
                : D.retentionNormal
          }
        >
          <Facts>
            <Fact term={D.noDelete}>{d.noDelete ? D.on : D.off}</Fact>
            <Fact term={D.objectLock}>
              {d.objectLockMode
                ? D.lockValue(
                    LOCK_LABEL[d.objectLockMode],
                    d.objectLockRetentionDays == null ? "?" : String(d.objectLockRetentionDays),
                  )
                : D.off}
            </Fact>
          </Facts>
        </LedgerSection>

        {s ? (
          <LedgerSection title={D.configuration}>
            {s.disk ? (
              <Facts>
                <Fact term={D.directory} mono>
                  {s.disk.path}
                </Fact>
              </Facts>
            ) : null}
            {s.s3 ? <S3Facts s3={s.s3} /> : null}
            {s.webdav ? <WebdavFacts webdav={s.webdav} /> : null}
            {s.sftp ? (
              <Facts>
                <Fact term={D.server} mono>
                  {s.sftp.user}@{s.sftp.host}:{s.sftp.port}
                </Fact>
                <Fact term={D.basePath} mono>
                  {s.sftp.basePath}
                </Fact>
                <Fact term={D.directory} mono>
                  {s.sftp.dir || D.basePathDefault}
                </Fact>
                <Fact term={D.password}>{stored(s.sftp.hasPassword)}</Fact>
                <Fact term={D.privateKey}>{stored(s.sftp.hasPrivateKey)}</Fact>
              </Facts>
            ) : null}
            <p className="text-xs text-muted-foreground">
              {D.updated(formatDateTime(d.updatedAt))}
            </p>
          </LedgerSection>
        ) : (
          <LedgerSection title={D.configuration}>
            <p className="text-sm text-muted-foreground">{D.adminOnly}</p>
          </LedgerSection>
        )}

        {d.type === "SFTP" ? <SftpSection d={d} isAdmin={isAdmin} /> : null}
        {s?.webdav ? (
          <WebdavTlsSection
            id={d.id}
            trust={s.webdav.trust}
            pinned={s.webdav.pinnedCertificate}
            isAdmin={isAdmin}
          />
        ) : null}

        {isAdmin ? <UnrecordedFilesSection destinationId={d.id} noDelete={d.noDelete} /> : null}

        {isAdmin ? (
          <LedgerSection title={D.deleteTitle} lead={D.deleteLead}>
            <Button
              type="button"
              variant="outline"
              className="self-start text-destructive"
              onClick={() => {
                setDeleting(true);
              }}
            >
              <Trash2 aria-hidden="true" /> {D.deleteButton}
            </Button>
            <DeleteDialog
              open={deleting}
              onOpenChange={setDeleting}
              kind="destination"
              name={d.name}
              onDelete={async () => {
                await withStepUp(() => remove({ variables: { id: d.id } }));
                toast.success(D.deleted(d.name));
                void navigate("/destinations");
              }}
            />
          </LedgerSection>
        ) : null}
      </div>
    </div>
  );
}
