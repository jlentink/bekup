import { graphql } from "@/graphql/gen";

/** The acceptances the caller may see, newest first (D-228). */
export const RiskAcceptancesQuery = graphql(`
  query RiskAcceptances($filter: RiskAcceptanceFilter, $first: Int) {
    riskAcceptances(filter: $filter, first: $first) {
      id
      kind
      connectionId
      jobId
      subjectName
      subjectDeleted
      condition
      duration
      reason
      createdBy {
        id
        displayName
        email
        deleted
      }
      createdByMe
      createdAt
      expiresAt
      endedAt
      endReason
      status
    }
  }
`);

/** Revokes an acceptance: the risk is flagged again (step-up). */
export const RevokeRiskAcceptanceMutation = graphql(`
  mutation RevokeRiskAcceptance($id: UUID!) {
    revokeRiskAcceptance(id: $id) {
      id
      status
      endedAt
      endReason
    }
  }
`);
