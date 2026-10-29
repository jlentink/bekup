import { graphql } from "@/graphql/gen";

export const TwoFactorPolicyQuery = graphql(`
  query AdminTwoFactorPolicy {
    twoFactorPolicy {
      requireForAll
      requiredRoles
      gracePeriodDays
      updatedAt
      passkeysAvailable
    }
  }
`);

export const UpdateTwoFactorPolicyMutation = graphql(`
  mutation AdminUpdateTwoFactorPolicy($input: TwoFactorPolicyInput!) {
    updateTwoFactorPolicy(input: $input) {
      requireForAll
      requiredRoles
      gracePeriodDays
      updatedAt
      passkeysAvailable
    }
  }
`);
