package audit

import (
	"context"
	"testing"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/store"
)

func TestChainAndCredentialDetectTampering(t *testing.T) {
	repository, err := store.Open("file:audit-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	chain := New(repository)
	ctx := context.Background()
	actor := domain.Actor{Name: "复核员", Role: domain.RoleReviewer}
	if err := chain.Append(ctx, "task-1", actor, "test", 1, "k1", map[string]string{"value": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := chain.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	c := domain.HandoverCredential{ID: "cred-1", TaskID: "task-1", SerialNo: "SH-1", ManifestDigest: domain.Digest([]byte(`{"a":1}`)), IssuedBy: "负责人", IssuedAt: time.Now()}
	c.PayloadJSON, err = CredentialPayload(c.ID, c.TaskID, c.SerialNo, c.ManifestDigest, c.IssuedBy, c.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredential(c, `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredential(c, `{"a":2}`); err == nil {
		t.Fatal("tampered manifest accepted")
	}
}
