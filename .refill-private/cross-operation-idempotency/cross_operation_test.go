package crossoperationidempotency_test

import (
	"context"
	"testing"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

func TestIdempotencyKeyIsScopedToOperation(t *testing.T) {
	repository, err := store.Open("file:cross-operation-idempotency?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	ctx := context.Background()
	app := service.New(repository)
	manager := domain.Actor{Name: "负责人", Role: domain.RoleManager}
	worker := domain.Actor{Name: "施工员", Role: domain.RoleWorker}
	task, err := app.CreateTask(ctx, service.CreateTaskInput{
		TaskCode: "IDEM-1", SiteName: "二号场地", BoreholeNo: "ZK-2",
		TotalDepthM: 10, StrataSummary: "稳定岩层", Actor: manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	agg, err := app.PublishPlan(ctx, task.ID, service.PublishPlanInput{
		ExpectedVersion: task.Version, Actor: manager,
		Segments: []service.PlanSegmentInput{{Sequence: 1, FromDepthM: 0, ToDepthM: 10, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.RecordConstruction(ctx, task.ID, service.ConstructionRequest{
		SegmentID: agg.Segments[0].ID, ExpectedVersion: agg.Task.Version,
		IdempotencyKey: "shared-key", ActualVolumeL: 10, ActualMixRatio: "1:1",
		MaterialBatch: "B-1", PerformedAt: time.Now().UTC(), Operator: "施工员",
		Result: domain.SegmentComplete, Actor: worker,
	})
	if err != nil {
		t.Fatal(err)
	}
	agg, err = app.GetAggregate(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	deviation, err := app.CreateDeviation(ctx, task.ID, service.CreateDeviationInput{
		SegmentID: agg.Segments[0].ID, ExpectedVersion: agg.Task.Version,
		IdempotencyKey: "shared-key", Description: "现场补充偏差",
		Severity: "low", EvidenceNote: "影像已归档", Actor: worker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deviation.ID == "" || deviation.TaskID != task.ID {
		t.Fatalf("跨操作幂等缓存返回了伪造的成功结果: %#v", deviation)
	}
	agg, err = app.GetAggregate(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg.Deviations) != 1 || agg.Deviations[0].ID != deviation.ID {
		t.Fatalf("偏差未按独立操作持久化: %#v", agg.Deviations)
	}
}
