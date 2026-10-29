import { graphql } from "@/graphql/gen";

export const ChangePasswordMutation = graphql(`
  mutation ChangePassword($currentPassword: String!, $newPassword: String!) {
    changePassword(currentPassword: $currentPassword, newPassword: $newPassword)
  }
`);

export const MySessionsQuery = graphql(`
  query MySessions {
    mySessions {
      id
      createdAt
      lastSeenAt
      idleExpiresAt
      absoluteExpiresAt
      ip
      userAgent
      aal
      current
    }
  }
`);

export const RevokeSessionMutation = graphql(`
  mutation RevokeSession($id: UUID!) {
    revokeSession(id: $id)
  }
`);

export const TwoFactorOverviewQuery = graphql(`
  query TwoFactorOverview {
    myTwoFactorStatus {
      totpEnabled
      totpEnabledAt
      totpEnrollmentPending
      recoveryCodesRemaining
    }
    webauthnStatus {
      webauthnAvailable
      reason
      rpId
    }
    myPasskeys {
      id
      name
      createdAt
      lastUsedAt
      transports
      backupEligible
      backedUp
      userVerified
      signCountAnomalyAt
    }
  }
`);

export const BeginTotpEnrollmentMutation = graphql(`
  mutation BeginTotpEnrollment {
    beginTotpEnrollment {
      qrCodeSvg
      manualEntryKey
      expiresAt
    }
  }
`);

export const ConfirmTotpEnrollmentMutation = graphql(`
  mutation ConfirmTotpEnrollment($code: String!) {
    confirmTotpEnrollment(code: $code) {
      codes
    }
  }
`);

export const DisableTotpMutation = graphql(`
  mutation DisableTotp {
    disableTotp
  }
`);

export const RegenerateRecoveryCodesMutation = graphql(`
  mutation RegenerateRecoveryCodes {
    regenerateRecoveryCodes {
      codes
    }
  }
`);

export const BeginPasskeyRegistrationMutation = graphql(`
  mutation BeginPasskeyRegistration {
    beginPasskeyRegistration
  }
`);

export const FinishPasskeyRegistrationMutation = graphql(`
  mutation FinishPasskeyRegistration($name: String!, $response: JSON!) {
    finishPasskeyRegistration(name: $name, response: $response) {
      id
      name
    }
  }
`);

export const RenamePasskeyMutation = graphql(`
  mutation RenamePasskey($id: UUID!, $name: String!) {
    renamePasskey(id: $id, name: $name) {
      id
      name
    }
  }
`);

export const DeletePasskeyMutation = graphql(`
  mutation DeletePasskey($id: UUID!) {
    deletePasskey(id: $id)
  }
`);
