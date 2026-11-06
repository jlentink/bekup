import { graphql } from "@/graphql/gen";

/**
 * Destination operations (task 6.4). Runner/network-calling mutations are
 * each their own single-root request (D-117); credentials are write-only
 * and only their has* flags are selected.
 */

export const DestinationsListQuery = graphql(`
  query DestinationsList {
    destinations {
      id
      name
      type
      noDelete
      objectLockMode
      objectLockRetentionDays
      ready
      notReadyReason
      updatedAt
      lastTest {
        at
        ok
        code
        stale
      }
    }
  }
`);

export const DestinationDetailQuery = graphql(`
  query DestinationDetail($id: UUID!) {
    destination(id: $id) {
      id
      name
      type
      noDelete
      objectLockMode
      objectLockRetentionDays
      ready
      notReadyReason
      createdAt
      updatedAt
      lastTest {
        at
        ok
        code
        stale
      }
      settings {
        disk {
          path
        }
        s3 {
          endpoint
          region
          bucket
          prefix
          pathStyle
          encryption
          kmsKeyId
          storageClass
          checksumMode
          conditionalWrites
          accessKeyId
          hasSecretAccessKey
          caBundlePem
        }
        sftp {
          host
          port
          user
          basePath
          dir
          authMethod
          hasPassword
          hasPrivateKey
          hasPassphrase
          publicKey
          hostKey {
            type
            fingerprintSha256
            authorizedKey
            confirmedAt
          }
        }
        webdav {
          url
          flavor
          username
          path
          caBundlePem
          timeoutSeconds
          hasPassword
          trust
          pinnedCertificate {
            spkiSha256
            spkiSha256Base64
            fingerprintSha256
            notBefore
            notAfter
            selfSigned
            pinnedAt
          }
        }
      }
    }
  }
`);

export const CreateDestinationMutation = graphql(`
  mutation CreateDestination($input: CreateDestinationInput!) {
    createDestination(input: $input) {
      id
      name
    }
  }
`);

export const UpdateDestinationMutation = graphql(`
  mutation UpdateDestination($id: UUID!, $input: UpdateDestinationInput!) {
    updateDestination(id: $id, input: $input) {
      id
      name
    }
  }
`);

export const DeleteDestinationMutation = graphql(`
  mutation DeleteDestination($id: UUID!) {
    deleteDestination(id: $id)
  }
`);

export const TestDestinationMutation = graphql(`
  mutation TestDestination($id: UUID!) {
    testDestination(id: $id) {
      ok
      probeKey
      probeRemains
      note
      steps {
        name
        ok
        code
        message
      }
      webdav {
        trust
        trusted
        certificate {
          spkiSha256
          spkiSha256Base64
          fingerprintSha256
          notBefore
          notAfter
          selfSigned
        }
        pinMatches
        server
        suggestedFlavor
        uploadMode
        overwriteProtection
      }
    }
  }
`);

export const FetchDestinationHostKeyMutation = graphql(`
  mutation FetchDestinationHostKey($id: UUID!) {
    fetchDestinationHostKey(id: $id) {
      type
      fingerprintSha256
      authorizedKey
      confirmedAt
      host
      port
    }
  }
`);

export const ConfirmDestinationHostKeyMutation = graphql(`
  mutation ConfirmDestinationHostKey(
    $id: UUID!
    $authorizedKey: String!
    $expectedHost: String!
    $expectedPort: Int!
  ) {
    confirmDestinationHostKey(
      id: $id
      authorizedKey: $authorizedKey
      expectedHost: $expectedHost
      expectedPort: $expectedPort
    ) {
      id
      ready
    }
  }
`);

export const GenerateDestinationSshKeypairMutation = graphql(`
  mutation GenerateDestinationSshKeypair($id: UUID!) {
    generateDestinationSshKeypair(id: $id) {
      publicKey
      fingerprintSha256
      destination {
        id
      }
    }
  }
`);

/**
 * Pins the certificate a WebDAV server presents (admin + step-up, D-222):
 * the server captures it again and pins it only if its SPKI SHA-256 equals
 * the fingerprint the admin compared. Must be the only root field.
 */
export const PinWebdavCertificateMutation = graphql(`
  mutation PinWebdavCertificate($id: UUID!, $fingerprint: String!) {
    pinWebdavCertificate(id: $id, fingerprint: $fingerprint) {
      id
      ready
    }
  }
`);

/** Removes the certificate pin of a WebDAV destination (admin + step-up; audited, alerted). */
export const ClearWebdavCertificatePinMutation = graphql(`
  mutation ClearWebdavCertificatePin($id: UUID!) {
    clearWebdavCertificatePin(id: $id) {
      id
      ready
    }
  }
`);

/**
 * Unrecorded files of a destination (D-209, admins only): files in bekup's
 * layout that no backup record references, found by the daily scan.
 */
export const DestinationUnrecordedFilesQuery = graphql(`
  query DestinationUnrecordedFiles($destinationId: UUID!, $first: Int, $after: String) {
    destinationUnrecordedFiles(destinationId: $destinationId, first: $first, after: $after) {
      destinationId
      scannedAt
      totalCount
      totalBytes
      truncated
      prunedDirectories
      files {
        id
        key
        size
        modifiedAt
        firstSeenAt
        lastSeenAt
      }
      nextCursor
    }
  }
`);

/** Deletes one unrecorded file (admin + step-up); the only root field of its request. */
export const DeleteUnrecordedFileMutation = graphql(`
  mutation DeleteUnrecordedFile($destinationId: UUID!, $id: UUID!) {
    deleteUnrecordedFile(destinationId: $destinationId, id: $id) {
      ok
      code
      message
    }
  }
`);

/**
 * What bekup stores per destination (destinationUsage, staff; one
 * aggregate): the list's "Stored" column and the detail page.
 */
export const DestinationStoredQuery = graphql(`
  query DestinationStored {
    destinationUsage {
      destinationId
      storedBytes
      artifactCount
      backupCount
    }
  }
`);

/**
 * The total and free space behind a destination (D-225, staff): asked on
 * demand, rate limited and cached by the server; fixed reasons only.
 */
export const DestinationSpaceQuery = graphql(`
  query DestinationSpace($id: UUID!) {
    destinationSpace(id: $id) {
      destinationId
      status
      reason
      totalBytes
      freeBytes
      usedBytes
      newestSetBytes
      low
      checkedAt
      stale
    }
  }
`);

/**
 * The stored answer of the newest space check (D-227, staff): the hourly
 * check or the last on-demand check; reading it never asks the backend.
 */
export const DestinationSpaceCheckQuery = graphql(`
  query DestinationSpaceCheck($id: UUID!) {
    destination(id: $id) {
      id
      spaceCheck {
        destinationId
        status
        reason
        totalBytes
        freeBytes
        usedBytes
        newestSetBytes
        low
        checkedAt
        stale
      }
    }
  }
`);
