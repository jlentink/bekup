import { graphql } from "@/graphql/gen";

/**
 * Connection operations (task 6.3). Every runner-calling mutation is its
 * own request with one root field (D-117); credentials are write-only and
 * only their has* flags are ever selected.
 */

export const ConnectionsListQuery = graphql(`
  query ConnectionsList {
    connections {
      id
      name
      engine
      versionKey
      legacyVersion
      host
      port
      tlsMode
      insecureTransport
      pinStatus
      pinExpiresAt
      sshEnabled
      ready
      notReadyReason
      maxConcurrentRuns
      effectiveMaxConcurrentRuns
      lastTest {
        at
        ok
        code
        stale
        privileges {
          status
          reasons
          acknowledgementRequired
        }
      }
    }
  }
`);

export const ConnectionDetailQuery = graphql(`
  query ConnectionDetail($id: UUID!) {
    connection(id: $id) {
      id
      name
      engine
      versionKey
      legacyVersion
      host
      port
      tlsMode
      insecureTransport
      pinStatus
      pinExpiresAt
      sshEnabled
      ready
      notReadyReason
      maxConcurrentRuns
      effectiveMaxConcurrentRuns
      createdAt
      updatedAt
      lastTest {
        at
        ok
        code
        stale
        privileges {
          status
          reasons
          acknowledgementRequired
        }
      }
      settings {
        imageKey
        username
        hasPassword
        clientCertificatePem
        tlsPin {
          certificatePem
          subject
          issuer
          isCa
          leafSpkiSha256
          notAfter
          pinnedAt
        }
        backupPin {
          certificatePem
          subject
          issuer
          isCa
          leafSpkiSha256
          notAfter
          pinnedAt
        }
        ssh {
          host
          port
          user
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
      }
    }
  }
`);

export const ConnectionEngineVersionsQuery = graphql(`
  query ConnectionEngineVersions($engine: DatabaseEngine) {
    engineVersions(engine: $engine) {
      engine
      key
      clientVersion
      legacy
    }
  }
`);

export const CreateConnectionMutation = graphql(`
  mutation CreateConnection($input: CreateConnectionInput!) {
    createConnection(input: $input) {
      id
      name
    }
  }
`);

export const UpdateConnectionMutation = graphql(`
  mutation UpdateConnection($id: UUID!, $input: UpdateConnectionInput!) {
    updateConnection(id: $id, input: $input) {
      id
      name
    }
  }
`);

export const DeleteConnectionMutation = graphql(`
  mutation DeleteConnection($id: UUID!) {
    deleteConnection(id: $id)
  }
`);

export const TestConnectionMutation = graphql(`
  mutation TestConnection($id: UUID!) {
    testConnection(id: $id) {
      ok
      code
      message
      serverVersion
      detectedVersionKey
      compatible
      legacy
      warnings
      databaseCount
      privileges {
        status
        reasons
        acknowledgementRequired
      }
    }
  }
`);

export const DetectVersionMutation = graphql(`
  mutation DetectConnectionVersion($id: UUID!) {
    detectVersion(id: $id) {
      serverVersion
      suggestedVersionKey
      legacy
      currentCompatible
      warnings
    }
  }
`);

export const PrePullImageMutation = graphql(`
  mutation PrePullConnectionImage($id: UUID!) {
    prePullImage(id: $id) {
      imageRef
      alreadyPresent
    }
  }
`);

export const FetchCertificateMutation = graphql(`
  mutation FetchConnectionCertificate($id: UUID!) {
    fetchCertificate(id: $id) {
      suggestedAnchorIndex
      leafSpkiSha256
      certificates {
        pem
        subject
        issuer
        dnsNames
        ipAddresses
        notBefore
        notAfter
        isCa
        selfSigned
        spkiSha256
        spkiSha256Hex
        fingerprintSha256
      }
    }
  }
`);

export const ConfirmCertificatePinMutation = graphql(`
  mutation ConfirmConnectionCertificatePin($id: UUID!, $input: CertificatePinInput!) {
    confirmCertificatePin(id: $id, input: $input) {
      id
      pinStatus
      pinExpiresAt
    }
  }
`);

export const StageBackupPinMutation = graphql(`
  mutation StageConnectionBackupPin($id: UUID!, $input: CertificatePinInput!) {
    stageBackupPin(id: $id, input: $input) {
      id
      pinStatus
    }
  }
`);

export const FetchSshHostKeyMutation = graphql(`
  mutation FetchConnectionSshHostKey($id: UUID!) {
    fetchSshHostKey(id: $id) {
      type
      fingerprintSha256
      authorizedKey
      confirmedAt
      host
      port
    }
  }
`);

export const ConfirmSshHostKeyMutation = graphql(`
  mutation ConfirmConnectionSshHostKey(
    $id: UUID!
    $authorizedKey: String!
    $expectedHost: String!
    $expectedPort: Int!
  ) {
    confirmSshHostKey(
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

export const SetTlsModeMutation = graphql(`
  mutation SetConnectionTlsMode($id: UUID!, $mode: ConnectionTlsMode!) {
    setTlsMode(id: $id, mode: $mode) {
      id
      tlsMode
      insecureTransport
      pinStatus
    }
  }
`);

export const GenerateConnectionSshKeypairMutation = graphql(`
  mutation GenerateConnectionSshKeypair($id: UUID!) {
    generateConnectionSshKeypair(id: $id) {
      publicKey
      fingerprintSha256
      connection {
        id
      }
    }
  }
`);
