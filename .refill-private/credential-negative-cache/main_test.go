package credentialnegativecache

import (
	"context"
	"testing"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

func TestCredentialReadDoesNotReuseNegativeCacheAfterIssue(t *testing.T) {
	repository, err := store.Open("file:credential-negative-cache?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	task := domain.SealTask{
		ID: "task-cache-1", TaskCode: "CACHE-1", SiteName: "场地", BoreholeNo: "ZK-1",
		TotalDepthM: 10, StrataSummary: "岩层", Status: domain.TaskFrozen, Version: 2,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	app := service.New(repository)
	if _, err := app.Credential(ctx, task.ID); domain.KindOf(err) != domain.KindNotFound {
		t.Fatalf("首次查询应为 NotFound，得到 %v", err)
	}
	credential := domain.HandoverCredential{
		ID: "cred-cache-1", TaskID: task.ID, SerialNo: "SH-000001", ManifestDigest: "sha256:test",
		IssuedBy: "负责人", IssuedAt: now, PayloadJSON: `{"schemaVersion":1}`,
	}
	if err := repository.CreateCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	got, err := app.Credential(ctx, task.ID)
	if err != nil {
		t.Fatalf("签发后读取凭据失败: %v", err)
	}
	if got.ID != credential.ID {
		t.Fatalf("读取了错误凭据: %#v", got)
	}
}
