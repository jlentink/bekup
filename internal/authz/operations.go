package authz

import (
	"slices"
	"sort"

	"github.com/jlentink/bekup/internal/auth"
)

// Operation is one thing a caller can do through the API. Every GraphQL
// root field maps to exactly one Operation (graph.RootFieldOperations) and
// every Operation has exactly one Rule in the policy table below (deny by
// default: an unknown Operation is always denied).
//
// Adding an operation (see AGENTS.md): add the constant here, its Rule to
// policy, its expected row to the matrix in matrix_test.go and, for a new
// root field, the field → operation entry in graph.RootFieldOperations.
// The tests fail when any of these is missing.
type Operation string

// Operation catalog, grouped by area. Names are "<area>.<action>".
// Comments name the planned task; nested GraphQL fields inherit the
// operation of their root field.
const (
	// Own account and session (every authenticated user; tasks 1.5-1.9).
	OpAccountView   Operation = "account.view"    // me
	OpAccountLogout Operation = "account.logout"  // logout
	OpAccountStepUp Operation = "account.step_up" // beginStepUp, completeStepUp
	// OpAccountSecondFactorLogin completes a login waiting for its second
	// factor (beginPasskeyLogin, completeSecondFactor; task 1.9).
	OpAccountSecondFactorLogin Operation = "account.second_factor_login"
	OpAccountPasswordChange    Operation = "account.password_change"    // change own password
	OpAccountSessionsView      Operation = "account.sessions_view"      // list own sessions
	OpAccountSessionsRevoke    Operation = "account.sessions_revoke"    // revoke own sessions
	OpAccountSecondFactorEdit  Operation = "account.second_factor_edit" // TOTP, passkeys, recovery codes (own)

	// User management (admin; tasks 1.6, 6.8).
	OpUserList             Operation = "user.list"
	OpUserView             Operation = "user.view"
	OpUserCreate           Operation = "user.create"
	OpUserUpdate           Operation = "user.update" // incl. role changes
	OpUserDisable          Operation = "user.disable"
	OpUserEnable           Operation = "user.enable"
	OpUserDelete           Operation = "user.delete"
	OpUserPasswordReset    Operation = "user.password_reset"
	OpUserSecondFactorEdit Operation = "user.second_factor_reset"
	OpUserSessionsView     Operation = "user.sessions_view"
	OpUserSessionsRevoke   Operation = "user.sessions_revoke"
	// OpUserDirectoryView lists minimal user facts (id, name, role) for the
	// owner picker of the job editor.
	OpUserDirectoryView Operation = "user.directory_view"

	// 2FA policy (admin; task 1.9).
	OpMFAPolicyView   Operation = "mfa_policy.view"
	OpMFAPolicyUpdate Operation = "mfa_policy.update"

	// Runtime settings (admin; tasks 2.3, 5.1, 6.9).
	OpSettingsView        Operation = "settings.view"
	OpSettingsUpdate      Operation = "settings.update"
	OpSMTPView            Operation = "smtp.view"
	OpSMTPUpdate          Operation = "smtp.update"
	OpSMTPTest            Operation = "smtp.test"
	OpSSRFAllowlistView   Operation = "ssrf_allowlist.view"
	OpSSRFAllowlistUpdate Operation = "ssrf_allowlist.update"

	// Connections (task 2.11, D-135). Maintainers use connections:
	// non-secret metadata, test/detect/list databases; no CRUD, no
	// secrets, no pins or host keys, no image pulls. Every admin change
	// needs step-up (as for destinations, D-131).
	OpConnectionList            Operation = "connection.list"
	OpConnectionView            Operation = "connection.view"
	OpConnectionCreate          Operation = "connection.create"
	OpConnectionUpdate          Operation = "connection.update"        // non-secret fields, TLS mode upgrades
	OpConnectionSecretUpdate    Operation = "connection.secret_update" // checked in addition when an update sets credentials
	OpConnectionDelete          Operation = "connection.delete"
	OpConnectionTest            Operation = "connection.test" // test, detect version
	OpConnectionListDatabases   Operation = "connection.list_databases"
	OpConnectionPrePull         Operation = "connection.pre_pull"
	OpConnectionHostKeyFetch    Operation = "connection.host_key_fetch" // TOFU fetch through the runner, never trusted automatically
	OpConnectionHostKeyConfirm  Operation = "connection.host_key_confirm"
	OpConnectionCertFetch       Operation = "connection.cert_fetch"
	OpConnectionPinConfirm      Operation = "connection.pin_confirm" // confirmCertificatePin, stageBackupPin
	OpConnectionTLSDowngrade    Operation = "connection.tls_downgrade"
	OpConnectionSSHKeyGenerate  Operation = "connection.ssh_key_generate"
	OpTemporaryConnectionCreate Operation = "connection.temporary_create" // restore wizard (task 4.4)

	// Destinations (task 2.12, D-131). Maintainers use destinations:
	// metadata (name, type, mode flags, readiness) and test; no CRUD, no
	// configuration details, no secrets. Every change needs step-up: a
	// changed endpoint can redirect backups as surely as a changed secret.
	// Owners (task 3.5) will see name/type of the destinations their jobs
	// use through the job, not through these operations.
	OpDestinationList           Operation = "destination.list"
	OpDestinationView           Operation = "destination.view"
	OpDestinationCreate         Operation = "destination.create"
	OpDestinationUpdate         Operation = "destination.update"
	OpDestinationSecretUpdate   Operation = "destination.secret_update" // checked in addition when an update sets credentials
	OpDestinationDelete         Operation = "destination.delete"
	OpDestinationTest           Operation = "destination.test"
	OpDestinationHostKeyFetch   Operation = "destination.host_key_fetch" // TOFU fetch, never trusted automatically
	OpDestinationHostKeyConfirm Operation = "destination.host_key_confirm"
	OpDestinationSSHKeyGenerate Operation = "destination.ssh_key_generate"
	OpStorageUsageView          Operation = "destination.usage_view"

	// Jobs (tasks 3.2, 3.5, 4.6). Owners see their own jobs only.
	OpJobList               Operation = "job.list"
	OpJobView               Operation = "job.view"
	OpJobCreate             Operation = "job.create"
	OpJobUpdate             Operation = "job.update"
	OpJobDelete             Operation = "job.delete"
	OpJobSchedulePreview    Operation = "job.schedule_preview"
	OpJobOwnersAssign       Operation = "job.owners_assign"
	OpJobRetentionUpdate    Operation = "job.retention_update"
	OpJobEncryptionKeysEdit Operation = "job.encryption_keys_edit" // passphrase / age keypair
	OpJobEncryptionOptOut   Operation = "job.encryption_opt_out"
	OpJobPassphraseExport   Operation = "job.passphrase_export"
	OpJobTestRestoreEdit    Operation = "job.test_restore_edit"
	OpJobSharedChannelsEdit Operation = "job.shared_channels_edit"

	// Runs (tasks 3.6, 3.7).
	OpRunList     Operation = "run.list"
	OpRunView     Operation = "run.view" // incl. runUpdated
	OpRunLogsView Operation = "run.logs_view"
	OpRunNow      Operation = "run.now"
	OpRunCancel   Operation = "run.cancel"

	// Artifacts and downloads (tasks 3.7, 3.9).
	OpArtifactList     Operation = "artifact.list"
	OpArtifactView     Operation = "artifact.view"
	OpArtifactDownload Operation = "artifact.download" // createDownloadTicket (raw or plain)

	// Restores (tasks 4.2-4.6).
	OpRestoreTargetsList Operation = "restore.targets_list"
	OpRestoreArtifact    Operation = "restore.artifact" // needs a RestoreTarget resource
	OpRestoreGlobals     Operation = "restore.globals"
	OpRestoreView        Operation = "restore.view"

	// Notifications (tasks 5.3b, 6.10).
	OpSharedChannelList     Operation = "notification.shared_channel_list"
	OpSharedChannelEdit     Operation = "notification.shared_channel_edit"
	OpSharedChannelTest     Operation = "notification.shared_channel_test"
	OpPersonalChannelEdit   Operation = "notification.personal_channel_edit" // own email/webhooks
	OpPersonalChannelTest   Operation = "notification.personal_channel_test"
	OpNotificationPrefsEdit Operation = "notification.prefs_edit" // own preference matrix
	OpNotificationInbox     Operation = "notification.inbox"      // own inbox, unread, mark read, subscription

	// Monitoring (tasks 5.5, 5.6, 6.2).
	OpDashboardView    Operation = "monitor.dashboard_view" // scoped to visible jobs
	OpAlertList        Operation = "monitor.alert_list"     // security alerts
	OpAlertAcknowledge Operation = "monitor.alert_acknowledge"
	OpRunnerHealthView Operation = "monitor.runner_health_view"

	// Audit and keys (tasks 1.3, 1.4, 6.9).
	OpAuditLogView         Operation = "audit.log_view"
	OpAuditChainStatusView Operation = "audit.chain_status_view"
	OpKeysView             Operation = "keys.view" // instance signing key fingerprints, keyring status
)

// Fresh is the authentication freshness an operation needs.
type Fresh int

// Freshness levels.
const (
	// FreshNone: a valid session is enough.
	FreshNone Fresh = iota
	// FreshStepUp: a step-up within the step-up window (D-047).
	FreshStepUp
	// FreshAAL2: a passkey/TOTP check within the step-up window; users
	// without a second factor get REQUIRES_2FA_ENROLLMENT (D-057).
	FreshAAL2
)

// OwnerGrant is how a backup owner (role user listed in job_owners) gets an
// operation. It applies only to role user: a maintainer gets nothing extra
// from being an owner (D-067) and an admin needs nothing extra.
type OwnerGrant int

// Owner grants.
const (
	// OwnerNone: owners get nothing beyond their role.
	OwnerNone OwnerGrant = iota
	// OwnerJob: allowed when every Job resource is a job the user owns
	// (at least one Job resource is required).
	OwnerJob
	// OwnerList: allowed; the resolver must limit results to JobScope.
	OwnerList
)

// Rule is the policy for one operation.
type Rule struct {
	// Roles are allowed by role alone.
	Roles []auth.Role
	// Owner is the additional grant for backup owners.
	Owner OwnerGrant
	// Fresh is the required authentication freshness.
	Fresh Fresh
	// NeedsTarget requires a RestoreTarget resource.
	NeedsTarget bool
}

var (
	adminOnly = []auth.Role{auth.RoleAdmin}
	staff     = []auth.Role{auth.RoleAdmin, auth.RoleMaintainer}
	everyone  = []auth.Role{auth.RoleAdmin, auth.RoleMaintainer, auth.RoleUser}
)

// policy is the central role × operation matrix (D-011, D-012, D-013,
// D-014, D-015, D-047, D-053, D-057, D-067, D-106).
var policy = map[Operation]Rule{
	OpAccountView:   {Roles: everyone},
	OpAccountLogout: {Roles: everyone},
	OpAccountStepUp: {Roles: everyone},
	// Only meaningful for a session waiting for its second factor; the
	// resolver refuses other sessions (CONFLICT).
	OpAccountSecondFactorLogin: {Roles: everyone},
	OpAccountPasswordChange:    {Roles: everyone, Fresh: FreshStepUp},
	OpAccountSessionsView:      {Roles: everyone},
	OpAccountSessionsRevoke:    {Roles: everyone},
	OpAccountSecondFactorEdit:  {Roles: everyone, Fresh: FreshStepUp},

	OpUserList:             {Roles: adminOnly},
	OpUserView:             {Roles: adminOnly},
	OpUserCreate:           {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserUpdate:           {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserDisable:          {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserEnable:           {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserDelete:           {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserPasswordReset:    {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserSecondFactorEdit: {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserSessionsView:     {Roles: adminOnly},
	OpUserSessionsRevoke:   {Roles: adminOnly, Fresh: FreshStepUp},
	OpUserDirectoryView:    {Roles: staff},

	OpMFAPolicyView:   {Roles: adminOnly},
	OpMFAPolicyUpdate: {Roles: adminOnly, Fresh: FreshStepUp},

	OpSettingsView:        {Roles: adminOnly},
	OpSettingsUpdate:      {Roles: adminOnly, Fresh: FreshStepUp},
	OpSMTPView:            {Roles: adminOnly},
	OpSMTPUpdate:          {Roles: adminOnly, Fresh: FreshStepUp},
	OpSMTPTest:            {Roles: adminOnly},
	OpSSRFAllowlistView:   {Roles: adminOnly},
	OpSSRFAllowlistUpdate: {Roles: adminOnly, Fresh: FreshStepUp},

	OpConnectionList:            {Roles: staff},
	OpConnectionView:            {Roles: staff},
	OpConnectionCreate:          {Roles: adminOnly, Fresh: FreshStepUp},
	OpConnectionUpdate:          {Roles: adminOnly, Fresh: FreshStepUp},
	OpConnectionSecretUpdate:    {Roles: adminOnly, Fresh: FreshStepUp},
	OpConnectionDelete:          {Roles: adminOnly, Fresh: FreshStepUp},
	OpConnectionTest:            {Roles: staff},
	OpConnectionListDatabases:   {Roles: staff},
	OpConnectionPrePull:         {Roles: staff}, // D-106: maintainers may pre-pull (D-135 amended)
	OpConnectionHostKeyFetch:    {Roles: adminOnly},
	OpConnectionHostKeyConfirm:  {Roles: adminOnly, Fresh: FreshStepUp},
	OpConnectionCertFetch:       {Roles: adminOnly},
	OpConnectionPinConfirm:      {Roles: adminOnly, Fresh: FreshStepUp},
	OpConnectionTLSDowngrade:    {Roles: adminOnly, Fresh: FreshStepUp},
	OpConnectionSSHKeyGenerate:  {Roles: adminOnly, Fresh: FreshStepUp},
	OpTemporaryConnectionCreate: {Roles: adminOnly, Fresh: FreshStepUp},

	OpDestinationList:           {Roles: staff},
	OpDestinationView:           {Roles: staff},
	OpDestinationCreate:         {Roles: adminOnly, Fresh: FreshStepUp},
	OpDestinationUpdate:         {Roles: adminOnly, Fresh: FreshStepUp},
	OpDestinationSecretUpdate:   {Roles: adminOnly, Fresh: FreshStepUp},
	OpDestinationDelete:         {Roles: adminOnly, Fresh: FreshStepUp},
	OpDestinationTest:           {Roles: staff},
	OpDestinationHostKeyFetch:   {Roles: adminOnly},
	OpDestinationHostKeyConfirm: {Roles: adminOnly, Fresh: FreshStepUp},
	OpDestinationSSHKeyGenerate: {Roles: adminOnly, Fresh: FreshStepUp},
	OpStorageUsageView:          {Roles: staff},

	OpJobList:               {Roles: staff, Owner: OwnerList},
	OpJobView:               {Roles: staff, Owner: OwnerJob},
	OpJobCreate:             {Roles: staff},
	OpJobUpdate:             {Roles: staff},
	OpJobDelete:             {Roles: staff},
	OpJobSchedulePreview:    {Roles: staff},
	OpJobOwnersAssign:       {Roles: staff, Fresh: FreshStepUp},
	OpJobRetentionUpdate:    {Roles: staff},
	OpJobEncryptionKeysEdit: {Roles: staff, Fresh: FreshStepUp},
	OpJobEncryptionOptOut:   {Roles: adminOnly, Fresh: FreshStepUp},
	OpJobPassphraseExport:   {Roles: adminOnly, Fresh: FreshStepUp},
	OpJobTestRestoreEdit:    {Roles: staff},
	OpJobSharedChannelsEdit: {Roles: adminOnly},

	OpRunList:     {Roles: staff, Owner: OwnerList},
	OpRunView:     {Roles: staff, Owner: OwnerJob},
	OpRunLogsView: {Roles: staff, Owner: OwnerJob},
	OpRunNow:      {Roles: staff, Owner: OwnerJob},
	OpRunCancel:   {Roles: staff},

	OpArtifactList: {Roles: staff, Owner: OwnerList},
	OpArtifactView: {Roles: staff, Owner: OwnerJob},
	// D-012/D-067: admins and owners only; never maintainers, not even
	// when they are listed as owners.
	OpArtifactDownload: {Roles: adminOnly, Owner: OwnerJob, Fresh: FreshStepUp},

	OpRestoreTargetsList: {Roles: staff, Owner: OwnerJob},
	OpRestoreArtifact:    {Roles: staff, Owner: OwnerJob, Fresh: FreshAAL2, NeedsTarget: true},
	OpRestoreGlobals:     {Roles: adminOnly, Fresh: FreshAAL2},
	OpRestoreView:        {Roles: staff, Owner: OwnerJob},

	OpSharedChannelList:     {Roles: adminOnly},
	OpSharedChannelEdit:     {Roles: adminOnly, Fresh: FreshStepUp},
	OpSharedChannelTest:     {Roles: adminOnly},
	OpPersonalChannelEdit:   {Roles: everyone, Fresh: FreshStepUp},
	OpPersonalChannelTest:   {Roles: everyone},
	OpNotificationPrefsEdit: {Roles: everyone},
	OpNotificationInbox:     {Roles: everyone},

	OpDashboardView:    {Roles: staff, Owner: OwnerList},
	OpAlertList:        {Roles: adminOnly},
	OpAlertAcknowledge: {Roles: adminOnly},
	OpRunnerHealthView: {Roles: staff},

	OpAuditLogView:         {Roles: adminOnly},
	OpAuditChainStatusView: {Roles: adminOnly},
	OpKeysView:             {Roles: adminOnly},
}

// Operations returns every operation of the policy, sorted.
func Operations() []Operation {
	out := make([]Operation, 0, len(policy))
	for op := range policy {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RuleFor returns the rule of op; false for an unknown operation.
func RuleFor(op Operation) (Rule, bool) {
	r, ok := policy[op]
	if !ok {
		return Rule{}, false
	}
	r.Roles = slices.Clone(r.Roles)
	return r, true
}

// Known reports whether op is in the catalog.
func (op Operation) Known() bool {
	_, ok := policy[op]
	return ok
}
