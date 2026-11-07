import { graphql } from "@/graphql/gen";

/* External API tokens (D-220). The token itself is returned only by
 * CreateApiToken (fetchPolicy "no-cache") and lives only in component
 * state until the show-once dialog is closed. */

export const ApiTokensQuery = graphql(`
  query ApiTokens($jobId: UUID) {
    apiTokens(jobId: $jobId) {
      id
      name
      prefix
      jobs {
        id
        name
        deleted
      }
      hiddenJobCount
      createdBy {
        id
        email
        displayName
        deleted
      }
      createdByMe
      createdAt
      expiresAt
      neverExpires
      revokedAt
      allowedCidrs
      allowAnyAddress
      lastUsedAt
      lastSeenAt
      lastSeenIp
      lastSeenRefused
    }
  }
`);

/**
 * The jobs the caller may put into a token (admins: all; owners: their
 * own), searched on the server one page at a time.
 */
export const ApiAccessJobsQuery = graphql(`
  query ApiAccessJobs($search: String, $first: Int) {
    jobs(filter: { search: $search }, first: $first) {
      edges {
        node {
          id
          name
        }
      }
      pageInfo {
        hasNextPage
      }
    }
  }
`);

export const CreateApiTokenMutation = graphql(`
  mutation CreateApiToken($input: CreateApiTokenInput!) {
    createApiToken(input: $input) {
      plaintext
      token {
        id
        name
        prefix
        jobs {
          id
          name
          deleted
        }
        expiresAt
        neverExpires
      }
    }
  }
`);

export const RevokeApiTokenMutation = graphql(`
  mutation RevokeApiToken($id: UUID!) {
    revokeApiToken(id: $id) {
      id
      revokedAt
    }
  }
`);

export const UpdateApiTokenAddressesMutation = graphql(`
  mutation UpdateApiTokenAddresses(
    $id: UUID!
    $allowedCidrs: [String!]!
    $allowAnyAddress: Boolean!
  ) {
    updateApiTokenAddresses(
      id: $id
      allowedCidrs: $allowedCidrs
      allowAnyAddress: $allowAnyAddress
    ) {
      id
      allowedCidrs
      allowAnyAddress
    }
  }
`);
