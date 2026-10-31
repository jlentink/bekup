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
import { DestinationTest } from "./test-panel";
import { UnrecordedFilesBanner, UnrecordedFilesSection } from "./unrecorded-files";

type Destination = NonNullable<DetailResult["destination"]>;
type Settings = NonNullable<Destination["settings"]>;

const AUTH_LABEL = { NONE: "None yet", PASSWORD: "Password", PRIVATE_KEY: "Private key" } as const;

function stored(has: boolean) {
  return has ? <Tag tone="success">Stored</Tag> : <Tag tone="neutral">None</Tag>;
}

function S3Facts({ s3 }: { s3: NonNullable<Settings["s3"]> }) {
  return (
    <>
      <Facts>
        <Fact term="Endpoint" mono>
          {s3.endpoint ?? "AWS S3"}
        </Fact>
        <Fact term="Region" mono>
          {s3.region}
        </Fact>
        <Fact term="Bucket" mono>
          {s3.bucket}
        </Fact>
        <Fact term="Prefix" mono>
          {s3.prefix || "(bucket root)"}
        </Fact>
        <Fact term="Addressing">{s3.pathStyle ? "Path style" : "Virtual-hosted style"}</Fact>
        <Fact term="Encryption">
          {ENCRYPTION_LABEL[s3.encryption]}
          {s3.kmsKeyId ? <span className="ml-2 font-mono text-xs">{s3.kmsKeyId}</span> : null}
        </Fact>
        {s3.storageClass ? (
          <Fact term="Storage class" mono>
            {s3.storageClass}
          </Fact>
        ) : null}
        <Fact term="Checksums">{CHECKSUM_LABEL[s3.checksumMode]}</Fact>
        <Fact term="Overwrites">
          {s3.conditionalWrites
            ? "Refused atomically (If-None-Match)"
            : "Not prevented by the store"}
        </Fact>
        <Fact term="Access key ID" mono>
          {s3.accessKeyId}
        </Fact>
        <Fact term="Secret key">{stored(s3.hasSecretAccessKey)}</Fact>
      </Facts>
      {s3.caBundlePem ? (
        <div className="flex flex-col gap-1.5">
          <p className="eyebrow">Custom CA bundle</p>
          <CopyValue value={s3.caBundlePem} label="Copy the CA bundle" multiline />
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
      title="SSH"
      lead="The server's host key is pinned, never trusted automatically. Prefer a generated key over a password."
      aside={
        sftp.hostKey ? (
          <Tag tone="success">Host key pinned</Tag>
        ) : (
          <Tag tone="danger">Host key not confirmed</Tag>
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
              {sftp.hostKey ? "Re-check host key" : "Fetch and confirm host key"}
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
      <Facts>
        <Fact term="Signs in with">
          {AUTH_LABEL[sftp.authMethod]}
          {sftp.authMethod === "PRIVATE_KEY" && sftp.hasPassphrase ? " (with passphrase)" : ""}
        </Fact>
        <Fact term="Host key">
          {sftp.hostKey ? (
            <PinnedHostKey hostKey={sftp.hostKey} />
          ) : (
            <span className="text-status-failed-fg">Not confirmed: uploads cannot start</span>
          )}
        </Fact>
      </Facts>
      {sftp.publicKey ? (
        <div className="flex flex-col gap-1.5">
          <p className="eyebrow">Public key to install on the server</p>
          <CopyValue value={sftp.publicKey} label="Copy the SSH public key" />
        </div>
      ) : null}
      {isAdmin ? (
        <>
          <HostKeyDialog
            open={dialog === "hostkey"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
            subject="the SFTP server"
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
              toast.success("Host key pinned.");
            }}
          />
          <KeypairDialog
            open={dialog === "keypair"}
            onOpenChange={(o) => {
              if (!o) setDialog(undefined);
            }}
            subject="the SFTP server"
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
        <ArrowLeft aria-hidden="true" /> Destinations
      </Link>
    </Button>
  );

  if (!data && loading) return <DetailSkeleton label="Loading destination" />;
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
  const d = data?.destination;
  if (!d) {
    return (
      <div className="flex flex-col gap-6">
        {back}
        <EmptyState icon={Trash2} title="Destination not found">
          It was deleted or never existed.
        </EmptyState>
      </div>
    );
  }
  const s = d.settings;

  return (
    <div className="flex flex-col gap-6">
      {back}
      <PageHeader
        eyebrow={`${TYPE_LABEL[d.type]} destination`}
        title={d.name}
        actions={
          isAdmin ? (
            <Button asChild variant="outline">
              <Link to={`/destinations/${d.id}/edit`}>
                <Pencil aria-hidden="true" /> Edit
              </Link>
            </Button>
          ) : null
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        {d.ready ? (
          <Tag tone="success">Ready</Tag>
        ) : (
          <Tag tone="danger">Not ready{d.notReadyReason ? `: ${d.notReadyReason}` : ""}</Tag>
        )}
        {d.objectLockMode ? (
          <Tag tone="info" title={LOCK_DESCRIPTION[d.objectLockMode]}>
            Object Lock · {LOCK_LABEL[d.objectLockMode]}
            {d.objectLockRetentionDays != null ? ` · ${d.objectLockRetentionDays} days` : ""}
          </Tag>
        ) : null}
        {d.noDelete ? <Tag tone="info">No-delete</Tag> : null}
        {!isAdmin ? <Tag tone="neutral">Read only</Tag> : null}
      </div>
      <UnrecordedFilesBanner destinationId={d.id} enabled={isAdmin} />

      <div className="flex flex-col">
        <LedgerSection
          title="Test"
          lead="Writes, reads back and deletes a small probe object below the destination's prefix."
        >
          <DestinationTest id={d.id} lastTest={d.lastTest} />
        </LedgerSection>

        <LedgerSection
          title="Retention"
          lead={
            d.noDelete
              ? "bekup never deletes from this destination: configure lifecycle rules for retention."
              : d.objectLockMode
                ? "bekup applies retention once an object's lock has expired."
                : "bekup deletes backups here according to each job's retention policy."
          }
        >
          <Facts>
            <Fact term="No-delete">{d.noDelete ? "On" : "Off"}</Fact>
            <Fact term="Object Lock">
              {d.objectLockMode
                ? `${LOCK_LABEL[d.objectLockMode]}, ${d.objectLockRetentionDays ?? "?"} days`
                : "Off"}
            </Fact>
          </Facts>
        </LedgerSection>

        {s ? (
          <LedgerSection title="Configuration">
            {s.disk ? (
              <Facts>
                <Fact term="Directory" mono>
                  {s.disk.path}
                </Fact>
              </Facts>
            ) : null}
            {s.s3 ? <S3Facts s3={s.s3} /> : null}
            {s.sftp ? (
              <Facts>
                <Fact term="Server" mono>
                  {s.sftp.user}@{s.sftp.host}:{s.sftp.port}
                </Fact>
                <Fact term="Base path" mono>
                  {s.sftp.basePath}
                </Fact>
                <Fact term="Directory" mono>
                  {s.sftp.dir || "(base path)"}
                </Fact>
                <Fact term="Password">{stored(s.sftp.hasPassword)}</Fact>
                <Fact term="Private key">{stored(s.sftp.hasPrivateKey)}</Fact>
              </Facts>
            ) : null}
            <p className="text-xs text-muted-foreground">Updated {formatDateTime(d.updatedAt)}</p>
          </LedgerSection>
        ) : (
          <LedgerSection title="Configuration">
            <p className="text-sm text-muted-foreground">
              The full configuration is visible to admins only.
            </p>
          </LedgerSection>
        )}

        {d.type === "SFTP" ? <SftpSection d={d} isAdmin={isAdmin} /> : null}

        {isAdmin ? <UnrecordedFilesSection destinationId={d.id} noDelete={d.noDelete} /> : null}

        {isAdmin ? (
          <LedgerSection
            title="Delete"
            lead="Soft delete: the stored backups stay where they are. Jobs that write here block it."
          >
            <Button
              type="button"
              variant="outline"
              className="self-start text-destructive"
              onClick={() => {
                setDeleting(true);
              }}
            >
              <Trash2 aria-hidden="true" /> Delete destination
            </Button>
            <DeleteDialog
              open={deleting}
              onOpenChange={setDeleting}
              kind="destination"
              name={d.name}
              onDelete={async () => {
                await withStepUp(() => remove({ variables: { id: d.id } }));
                toast.success(`Destination "${d.name}" deleted.`);
                void navigate("/destinations");
              }}
            />
          </LedgerSection>
        ) : null}
      </div>
    </div>
  );
}
