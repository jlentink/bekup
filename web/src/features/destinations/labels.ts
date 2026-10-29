import type {
  DestinationType,
  ObjectLockMode,
  S3ChecksumMode,
  S3Encryption,
} from "@/graphql/gen/graphql";

export const TYPE_LABEL: Record<DestinationType, string> = {
  DISK: "Disk",
  S3: "S3",
  SFTP: "SFTP",
};

export const TYPE_DESCRIPTION: Record<DestinationType, string> = {
  DISK: "A directory below the server's storage.disk_root.",
  S3: "AWS S3 or an S3-compatible object store, optionally with Object Lock.",
  SFTP: "A directory on an SSH server, with a pinned host key.",
};

export const TYPES: readonly DestinationType[] = ["S3", "SFTP", "DISK"];

export const ENCRYPTION_LABEL: Record<S3Encryption, string> = {
  NONE: "None (bucket default)",
  SSE_S3: "SSE-S3 (provider-managed keys)",
  SSE_KMS: "SSE-KMS",
};

export const CHECKSUM_LABEL: Record<S3ChecksumMode, string> = {
  WHEN_SUPPORTED: "SHA-256 with every upload",
  WHEN_REQUIRED: "Only where required",
};

export const LOCK_LABEL: Record<ObjectLockMode, string> = {
  GOVERNANCE: "Governance",
  COMPLIANCE: "Compliance",
};

export const LOCK_DESCRIPTION: Record<ObjectLockMode, string> = {
  GOVERNANCE: "Users with a special permission can shorten or remove the lock.",
  COMPLIANCE: "Nobody, including the account root, can delete a locked object before it expires.",
};

const STEP_LABEL: Record<string, string> = {
  connect: "Connect",
  write: "Write probe",
  read: "Read back",
  delete: "Delete probe",
};

export function stepLabel(name: string): string {
  return STEP_LABEL[name] ?? name;
}
