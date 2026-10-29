import { graphql } from "@/graphql/gen";

/**
 * GraphQL operations used by more than one feature. Each operation exists
 * once, so the selection (and what may be cached) cannot drift between
 * pages.
 */

/** Databases on a connection (connection checks, job database picker). */
export const ListDatabasesMutation = graphql(`
  mutation ListDatabases($id: UUID!) {
    listDatabases(id: $id) {
      databases
      warnings
    }
  }
`);

/** Owner grants (job owners panel, user detail → owned jobs). */
export const AssignJobOwnersMutation = graphql(`
  mutation AssignJobOwners($jobId: UUID!, $users: [UUID!]!) {
    assignJobOwners(jobId: $jobId, users: $users) {
      userId
      email
      displayName
      role
      disabled
      addedAt
    }
  }
`);

export const RemoveJobOwnerMutation = graphql(`
  mutation RemoveJobOwner($jobId: UUID!, $userId: UUID!) {
    removeJobOwner(jobId: $jobId, userId: $userId) {
      userId
    }
  }
`);

/**
 * Passphrase export (job encryption panel, admin recovery page). The file
 * content is a secret: always run it with fetchPolicy "no-cache".
 */
export const ExportJobPassphrasesMutation = graphql(`
  mutation ExportJobPassphrases($jobId: UUID!) {
    exportJobPassphrases(jobId: $jobId) {
      fileName
      content
      versions
    }
  }
`);
