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
