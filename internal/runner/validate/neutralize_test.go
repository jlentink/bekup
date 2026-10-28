package validate

import (
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// NeutralizeRestoredDatabase only ever reaches the restore's staging
// database, a scratch database or a new (non-bekup) database.
func TestNeutralizeRestoredDatabase(t *testing.T) {
	v := newTestValidator(t)
	req := func(kind runnerpb.NeutralizeKind, db, id string) *runnerpb.NeutralizeRestoredDatabaseRequest {
		return &runnerpb.NeutralizeRestoredDatabaseRequest{Ctx: runCtx(), Target: validTarget(t), Kind: kind, Database: db, TempId: id}
	}
	for _, c := range []struct {
		r    *runnerpb.NeutralizeRestoredDatabaseRequest
		want string
	}{
		{req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING, "app", "0123456789ab"), "app__bekup_restore_0123456789ab"},
		{req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "app_copy", ""), "app_copy"},
		{req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH, "", "0123456789ab"), "bekup_test_0123456789ab"},
	} {
		got, err := v.NeutralizeRestoredDatabase(t.Context(), c.r)
		if err != nil || got.Database != c.want {
			t.Errorf("%v: %+v %v", c.r.GetKind(), got, err)
		}
	}
	my := req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "app", "")
	my.Target.Engine, my.Target.VersionKey = runnerpb.Engine_ENGINE_MYSQL, "8.4"
	bad := []*runnerpb.NeutralizeRestoredDatabaseRequest{
		my,
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_UNSPECIFIED, "app", ""),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING, "app", ""),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING, "app", "UPPER"),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING, "app__bekup_old_x1", "x2"),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING, "template1", "x2"),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "postgres", ""),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "template1", ""),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "app__bekup_restore_x1", ""),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "bekup_test_0123456789ab", ""),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "app", "x1"),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, "", ""),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH, "app", "0123456789ab"),
		req(runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH, "", "short"),
		{Target: validTarget(t), Kind: runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE, Database: "app"},
	}
	for i, r := range bad {
		if _, err := v.NeutralizeRestoredDatabase(t.Context(), r); code(err) != codes.InvalidArgument {
			t.Errorf("case %d (%v %q %q): %v", i, r.GetKind(), r.GetDatabase(), r.GetTempId(), err)
		}
	}
}
