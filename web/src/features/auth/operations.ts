import { graphql } from "@/graphql/gen";

/** The signed-in user and the restriction facts the session gate needs. */
export const MeQuery = graphql(`
  query Me {
    me {
      id
      email
      displayName
      role
      passwordChangeRequired
      twoFactorRequired
      twoFactorEnrollmentDeadline
    }
  }
`);

export const LoginMutation = graphql(`
  mutation Login($email: String!, $password: String!) {
    login(email: $email, password: $password) {
      secondFactorRequired
      availableFactors
      passwordChangeRequired
      twoFactorEnrollmentRequired
      user {
        id
        email
        displayName
        role
      }
    }
  }
`);

export const LogoutMutation = graphql(`
  mutation Logout {
    logout
  }
`);

export const BeginPasskeyLoginMutation = graphql(`
  mutation BeginPasskeyLogin {
    beginPasskeyLogin
  }
`);

export const CompleteSecondFactorMutation = graphql(`
  mutation CompleteSecondFactor($method: SecondFactorKind!, $code: String, $assertion: JSON) {
    completeSecondFactor(method: $method, code: $code, assertion: $assertion) {
      secondFactorRequired
      passwordChangeRequired
      twoFactorEnrollmentRequired
      user {
        id
      }
    }
  }
`);

export const BeginStepUpMutation = graphql(`
  mutation BeginStepUp {
    beginStepUp {
      methods
      passkeyOptions
    }
  }
`);

export const CompleteStepUpMutation = graphql(`
  mutation CompleteStepUp(
    $method: StepUpMethod!
    $code: String
    $password: String
    $assertion: JSON
  ) {
    completeStepUp(method: $method, code: $code, password: $password, assertion: $assertion) {
      stepUpAt
      aal2At
      aal
    }
  }
`);
