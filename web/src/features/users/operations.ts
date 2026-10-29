import { graphql } from "@/graphql/gen";

/** The admin's view of an account (list rows and the detail page). */
export const AdminUserFields = graphql(`
  fragment AdminUserFields on User {
    id
    email
    displayName
    role
    disabled
    lockedUntil
    passwordChangeRequired
    temporaryPasswordExpiresAt
    twoFactorRequired
    twoFactorEnrollmentDeadline
    twoFactorEnrolled
    lastSeenAt
    emailVerified
    createdAt
    updatedAt
    deletedAt
  }
`);

export const UsersQuery = graphql(`
  query AdminUsers($filter: UserFilter, $first: Int, $after: String) {
    users(filter: $filter, first: $first, after: $after) {
      edges {
        cursor
        node {
          ...AdminUserFields
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

export const UserQuery = graphql(`
  query AdminUser($id: UUID!) {
    user(id: $id) {
      ...AdminUserFields
    }
  }
`);

export const UserSessionsQuery = graphql(`
  query AdminUserSessions($userId: UUID!) {
    userSessions(userId: $userId) {
      id
      createdAt
      lastSeenAt
      absoluteExpiresAt
      ip
      userAgent
      aal
      current
    }
  }
`);

/*
 * The mutations that return a temporary password run with fetchPolicy
 * "no-cache": the password must never enter the Apollo cache, so nothing
 * can render it again after its dialog is closed.
 */
export const CreateUserMutation = graphql(`
  mutation AdminCreateUser($input: CreateUserInput!) {
    createUser(input: $input) {
      user {
        id
        email
        displayName
        role
      }
      temporaryPassword {
        password
        expiresAt
      }
    }
  }
`);

export const ResetUserPasswordMutation = graphql(`
  mutation AdminResetUserPassword($id: UUID!) {
    resetUserPassword(id: $id) {
      password
      expiresAt
    }
  }
`);

export const UpdateUserMutation = graphql(`
  mutation AdminUpdateUser($id: UUID!, $input: UpdateUserInput!) {
    updateUser(id: $id, input: $input) {
      ...AdminUserFields
    }
  }
`);

export const DisableUserMutation = graphql(`
  mutation AdminDisableUser($id: UUID!) {
    disableUser(id: $id) {
      ...AdminUserFields
    }
  }
`);

export const EnableUserMutation = graphql(`
  mutation AdminEnableUser($id: UUID!) {
    enableUser(id: $id) {
      ...AdminUserFields
    }
  }
`);

export const UnlockUserMutation = graphql(`
  mutation AdminUnlockUser($id: UUID!) {
    unlockUser(id: $id) {
      ...AdminUserFields
    }
  }
`);

export const DeleteUserMutation = graphql(`
  mutation AdminDeleteUser($id: UUID!) {
    deleteUser(id: $id)
  }
`);

export const RevokeUserSessionsMutation = graphql(`
  mutation AdminRevokeUserSessions($userId: UUID!) {
    revokeUserSessions(userId: $userId)
  }
`);

export const ResetUserSecondFactorsMutation = graphql(`
  mutation AdminResetUserSecondFactors($userId: UUID!) {
    resetUserSecondFactors(userId: $userId) {
      user {
        ...AdminUserFields
      }
      removedTotp
      removedPasskeys
      removedRecoveryCodes
      revokedSessions
    }
  }
`);

/** The jobs an account owns (JobFilter.ownerId, staff only; D-184). */
export const OwnedJobsQuery = graphql(`
  query AdminOwnedJobs($ownerId: UUID!, $first: Int, $after: String) {
    jobs(first: $first, after: $after, filter: { ownerId: $ownerId }) {
      edges {
        node {
          id
          name
          slug
          enabled
          owners {
            userId
            addedAt
          }
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

/** Jobs to choose from in the assign dialog (server-side search). */
export const AssignableJobsQuery = graphql(`
  query AdminAssignableJobs($search: String, $first: Int) {
    jobs(first: $first, filter: { search: $search }) {
      edges {
        node {
          id
          name
          slug
          enabled
        }
      }
      pageInfo {
        hasNextPage
      }
    }
  }
`);

/**
 * Candidates for the UserPicker: the staff user directory (D-184), usable
 * by admins and maintainers. Only id, name, e-mail, role and the disabled
 * flag are reachable; deleted accounts are never listed.
 */
export const UserPickerQuery = graphql(`
  query UserPickerCandidates($search: String) {
    userDirectory(search: $search, first: 50) {
      edges {
        node {
          id
          email
          displayName
          role
          disabled
        }
      }
    }
  }
`);
