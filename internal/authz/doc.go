// Package authz is the central, deny-by-default authorization policy
// (D-053, D-106).
//
//   - Operation is the typed catalog of everything the API can do
//     (operations.go); every operation has exactly one Rule in the policy
//     table: the roles that may perform it, the backup-owner grant
//     (OwnerJob: own jobs only; OwnerList: listings filtered by JobScope)
//     and the freshness it needs (step-up, or a fresh second factor for
//     restores). Unknown operations are always denied.
//   - Policy.Authorize(ctx, op, resources...) checks the auth.Principal of
//     ctx in this order: authenticated (UNAUTHENTICATED), role or ownership
//     (FORBIDDEN), restore target rules (FORBIDDEN), freshness
//     (STEP_UP_REQUIRED / REQUIRES_2FA_ENROLLMENT). Lookup failures are
//     internal errors and deny.
//   - Object-level facts come from the Ownership and SecondFactors hooks,
//     implemented by later tasks with DB queries; until then NoOwnership
//     and NoSecondFactors deny. authztest has in-memory fakes.
//   - The owner grant applies to role user only: a maintainer listed as an
//     owner gets nothing extra, in particular no downloads (D-067).
//
// matrix_test.go holds the expected outcome of every operation for admin,
// maintainer, maintainer-owner, owner, non-owner and anonymous callers and
// fails when an operation has no rule or no matrix row.
package authz
