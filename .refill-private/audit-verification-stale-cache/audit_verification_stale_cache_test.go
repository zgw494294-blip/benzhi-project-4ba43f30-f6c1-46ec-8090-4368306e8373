package audit_verification_stale_cache_test

import (
	"context"
	"testing"

	"drill-seal-handover/internal/audit"
	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/store"
)

func TestAuditVerificationDoesNotReuseStaleSuccess(t *testing.T) {
	repository, err := store.Open("file:audit-verification-stale-cache?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	ctx := context.Background()
	chain := audit.New(repository)
	actor := domain.Actor{Name: "复核员", Role: domain.RoleReviewer}
	if err := chain.Append(ctx, "task-1", actor, "review.completed", 1, "review-1", map[string]string{"result": "passed"}); err != nil {
		t.Fatal(err)
	}
	if err := chain.Append(ctx, "task-1", actor, "manifest.frozen", 2, "freeze-1", map[string]string{"digest": "original"}); err != nil {
		t.Fatal(err)
	}
	if err := chain.Verify(ctx); err != nil {
		t.Fatalf("初次审计链校验应成功: %v", err)
	}

	if _, err := repository.DB().ExecContext(ctx, `UPDATE audit_events SET payload_json=? WHERE sequence=1`, `{"result":"tampered"}`); err != nil {
		t.Fatal(err)
	}
	if err := chain.Verify(ctx); err == nil {
		t.Fatal("TestAuditVerificationDoesNotReuseStaleSuccess: 篡改既有事件后不应复用缓存的成功结果")
	}
}
