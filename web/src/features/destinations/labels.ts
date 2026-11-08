import type {
  DestinationType,
  ObjectLockMode,
  S3ChecksumMode,
  S3Encryption,
} from "@/graphql/gen/graphql";
import { defineMessages } from "@/lib/i18n";

const L = defineMessages({
  en: {
    type: {
      DISK: "Disk",
      S3: "S3",
      SFTP: "SFTP",
      WEBDAV: "WebDAV",
    } satisfies Record<DestinationType, string>,
    typeDescription: {
      DISK: "A directory below the server's storage.disk_root.",
      S3: "AWS S3 or an S3-compatible object store, optionally with Object Lock.",
      SFTP: "A directory on an SSH server, with a pinned host key.",
      WEBDAV:
        "A WebDAV folder: Nextcloud or ownCloud (TransIP STACK too), a NAS (Synology, QNAP), Apache, nginx, rclone or Caddy, or hosted WebDAV such as a Hetzner Storage Box.",
    } satisfies Record<DestinationType, string>,
    encryption: {
      NONE: "None (bucket default)",
      SSE_S3: "SSE-S3 (provider-managed keys)",
      SSE_KMS: "SSE-KMS",
    } satisfies Record<S3Encryption, string>,
    checksum: {
      WHEN_SUPPORTED: "SHA-256 with every upload",
      WHEN_REQUIRED: "Only where required",
    } satisfies Record<S3ChecksumMode, string>,
    lock: {
      GOVERNANCE: "Governance",
      COMPLIANCE: "Compliance",
    } satisfies Record<ObjectLockMode, string>,
    lockDescription: {
      GOVERNANCE: "Users with a special permission can shorten or remove the lock.",
      COMPLIANCE:
        "Nobody, including the account root, can delete a locked object before it expires.",
    } satisfies Record<ObjectLockMode, string>,
    step: {
      connect: "Connect",
      overwrite: "Overwrite protection",
      write: "Write probe",
      read: "Read back",
      delete: "Delete probe",
    } satisfies Record<string, string>,
  },
  nl: {
    type: {
      DISK: "Schijf",
      S3: "S3",
      SFTP: "SFTP",
      WEBDAV: "WebDAV",
    },
    typeDescription: {
      DISK: "Een map onder storage.disk_root van de server.",
      S3: "AWS S3 of een S3-compatibele objectopslag, optioneel met Object Lock.",
      SFTP: "Een map op een SSH-server, met een vastgepinde hostsleutel.",
      WEBDAV:
        "Een WebDAV-map: Nextcloud of ownCloud (ook TransIP STACK), een NAS (Synology, QNAP), Apache, nginx, rclone of Caddy, of gehoste WebDAV zoals een Hetzner Storage Box.",
    },
    encryption: {
      NONE: "Geen (standaard van de bucket)",
      SSE_S3: "SSE-S3 (sleutels beheerd door de provider)",
      SSE_KMS: "SSE-KMS",
    },
    checksum: {
      WHEN_SUPPORTED: "SHA-256 bij elke upload",
      WHEN_REQUIRED: "Alleen waar vereist",
    },
    lock: {
      GOVERNANCE: "Governance",
      COMPLIANCE: "Compliance",
    },
    lockDescription: {
      GOVERNANCE: "Gebruikers met een speciaal recht kunnen de vergrendeling inkorten of opheffen.",
      COMPLIANCE:
        "Niemand, ook de root van het account niet, kan een vergrendeld object verwijderen voordat het verloopt.",
    },
    step: {
      connect: "Verbinden",
      overwrite: "Bescherming tegen overschrijven",
      write: "Schrijftest",
      read: "Teruglezen",
      delete: "Verwijdertest",
    },
  },
});

export const TYPE_LABEL: Record<DestinationType, string> = L.type;

export const TYPE_DESCRIPTION: Record<DestinationType, string> = L.typeDescription;

export const TYPES: readonly DestinationType[] = ["S3", "SFTP", "WEBDAV", "DISK"];

export const ENCRYPTION_LABEL: Record<S3Encryption, string> = L.encryption;

export const CHECKSUM_LABEL: Record<S3ChecksumMode, string> = L.checksum;

export const LOCK_LABEL: Record<ObjectLockMode, string> = L.lock;

export const LOCK_DESCRIPTION: Record<ObjectLockMode, string> = L.lockDescription;

const STEP_LABEL: Record<string, string | undefined> = L.step;

export function stepLabel(name: string): string {
  return STEP_LABEL[name] ?? name;
}
