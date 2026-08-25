package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/store"
)

func testService(t *testing.T) (*Service, context.Context, domain.Actor, domain.Actor, domain.Actor) {
	t.Helper()
	repository, err := store.Open("file:service-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return New(repository), context.Background(), domain.Actor{Name: "负责人", Role: domain.RoleManager}, domain.Actor{Name: "施工员", Role: domain.RoleWorker}, domain.Actor{Name: "复核员", Role: domain.RoleReviewer}
}

func TestConstructionIdempotencyAndVersionConflict(t *testing.T) {
	app, ctx, manager, worker, _ := testService(t)
	task, err := app.CreateTask(ctx, CreateTaskInput{TaskCode: "T-1", SiteName: "场地", BoreholeNo: "ZK-1", TotalDepthM: 10, StrataSummary: "岩层", Actor: manager})
	if err != nil {
		t.Fatal(err)
	}
	agg, err := app.PublishPlan(ctx, task.ID, PublishPlanInput{ExpectedVersion: task.Version, Actor: manager, Segments: []PlanSegmentInput{{Sequence: 1, FromDepthM: 0, ToDepthM: 10, MaterialType: "浆", PlannedVolumeL: 20, MixRatio: "1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	input := ConstructionRequest{SegmentID: agg.Segments[0].ID, ExpectedVersion: agg.Task.Version, IdempotencyKey: "same", ActualVolumeL: 20, ActualMixRatio: "1:1", MaterialBatch: "B-1", PerformedAt: time.Now(), Operator: "施工员", Result: domain.SegmentComplete, Actor: worker}
	first, err := app.RecordConstruction(ctx, task.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := app.RecordConstruction(ctx, task.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Segment.ID != first.Segment.ID {
		t.Fatalf("expected idempotent replay: %#v", replay)
	}
	_, err = app.RecordConstruction(ctx, task.ID, input)
	if err != nil {
		t.Fatalf("replay should not conflict: %v", err)
	}
	_, err = app.PublishPlan(ctx, task.ID, PublishPlanInput{ExpectedVersion: 1, Actor: manager, Segments: inputSegments()})
	if domain.KindOf(err) != domain.KindConflict {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func inputSegments() []PlanSegmentInput {
	return []PlanSegmentInput{{Sequence: 1, FromDepthM: 0, ToDepthM: 10, MaterialType: "浆", PlannedVolumeL: 20, MixRatio: "1:1"}}
}

func TestDuplicateBoreholeAndPlanSnapshots(t *testing.T) {
	app, ctx, manager, _, _ := testService(t)
	task, err := app.CreateTask(ctx, CreateTaskInput{TaskCode: "T-1", SiteName: "场地  一", BoreholeNo: "zk-01", CollarEasting: 100.123, CollarNorthing: 200.456, TotalDepthM: 10, StrataSummary: "岩层", Actor: manager})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.CreateTask(ctx, CreateTaskInput{TaskCode: "T-2", SiteName: " 场地 一 ", BoreholeNo: " ZK- 01 ", TotalDepthM: 10, StrataSummary: "岩层", Actor: manager})
	var de *domain.Error
	if !errors.As(err, &de) || de.MatchedTaskCode != "T-1" {
		t.Fatalf("应返回重复任务编号: %v", err)
	}
	first, err := app.PublishPlan(ctx, task.ID, PublishPlanInput{ExpectedVersion: task.Version, Actor: manager, Segments: []PlanSegmentInput{{Sequence: 1, FromDepthM: 0, ToDepthM: 5, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}, {Sequence: 2, FromDepthM: 5, ToDepthM: 10, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.PublishPlan(ctx, task.ID, PublishPlanInput{ExpectedVersion: first.Task.Version, Actor: manager, Segments: []PlanSegmentInput{{Sequence: 1, FromDepthM: 0, ToDepthM: 5, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}, {Sequence: 2, FromDepthM: 5, ToDepthM: 10, MaterialType: "膨润土", PlannedVolumeL: 12, MixRatio: "1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Task.PlanVersion != 2 || second.PlanDiff == nil || len(second.PlanDiff.Changed) != 2 {
		t.Fatalf("方案差异错误: %#v", second)
	}
	for version := int64(1); version <= 2; version++ {
		snapshot, err := app.PlanSnapshot(ctx, task.ID, version)
		if err != nil || snapshot.PlanVersion != version {
			t.Fatalf("方案快照 %d: %#v %v", version, snapshot, err)
		}
	}
}

func TestBatchUsageReworkReviewAndPreflight(t *testing.T) {
	app, ctx, manager, worker, reviewer := testService(t)
	created := time.Now().UTC().Add(-time.Minute)
	app.now = func() time.Time { return created }
	task, err := app.CreateTask(ctx, CreateTaskInput{TaskCode: "T-R", SiteName: "场地", BoreholeNo: "R-1", TotalDepthM: 10, StrataSummary: "岩层", Actor: manager})
	if err != nil {
		t.Fatal(err)
	}
	agg, err := app.PublishPlan(ctx, task.ID, PublishPlanInput{ExpectedVersion: task.Version, Actor: manager, Segments: []PlanSegmentInput{{Sequence: 1, FromDepthM: 0, ToDepthM: 5, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}, {Sequence: 2, FromDepthM: 5, ToDepthM: 10, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	app.now = func() time.Time { return created.Add(time.Minute) }
	result, err := app.RecordConstruction(ctx, task.ID, ConstructionRequest{SegmentID: agg.Segments[0].ID, ExpectedVersion: agg.Task.Version, IdempotencyKey: "c1", ActualVolumeL: 8, ActualMixRatio: "1:1", MaterialBatch: " batch-1 ", PerformedAt: created.Add(time.Second), Operator: "施工员", Result: domain.SegmentFailed, Actor: worker})
	if err != nil {
		t.Fatal(err)
	}
	agg, _ = app.GetAggregate(ctx, task.ID)
	_, err = app.RecordConstruction(ctx, task.ID, ConstructionRequest{SegmentID: agg.Segments[1].ID, ExpectedVersion: agg.Task.Version, IdempotencyKey: "c2", ActualVolumeL: 10, ActualMixRatio: "2:1", MaterialBatch: "BATCH-1", PerformedAt: created.Add(2 * time.Second), Operator: "施工员", Result: domain.SegmentComplete, Actor: worker})
	if domain.KindOf(err) != domain.KindValidation {
		t.Fatalf("批次矛盾应拒绝: %v", err)
	}
	item := *result.AutoDeviation
	agg, _ = app.GetAggregate(ctx, task.ID)
	item, err = app.CorrectDeviation(ctx, task.ID, CorrectionInput{DeviationID: item.ID, ExpectedVersion: agg.Task.Version, IdempotencyKey: "fix", Correction: "返工", ReworkRequired: true, Actor: manager})
	if err != nil {
		t.Fatal(err)
	}
	agg, _ = app.GetAggregate(ctx, task.ID)
	_, err = app.RecordRework(ctx, task.ID, ReworkInput{DeviationID: item.ID, ExpectedVersion: agg.Task.Version, IdempotencyKey: "rw", MaterialBatch: "BATCH-2", ActualMixRatio: "1:1", ActualVolumeL: 10, PerformedAt: created.Add(3 * time.Second), Operator: "施工员", Result: domain.SegmentComplete, EvidenceNote: "返工影像已归档", Actor: worker})
	if err != nil {
		t.Fatal(err)
	}
	agg, _ = app.GetAggregate(ctx, task.ID)
	_, err = app.ReviewDeviation(ctx, task.ID, ReviewInput{DeviationID: item.ID, ExpectedVersion: agg.Task.Version, IdempotencyKey: "review", Note: "符合方案", Result: domain.ReviewPassed, Actor: reviewer})
	if err != nil {
		t.Fatal(err)
	}
	// 完成第二段后，放行预检应采用第一段的合格返工事实。
	agg, _ = app.GetAggregate(ctx, task.ID)
	_, err = app.RecordConstruction(ctx, task.ID, ConstructionRequest{SegmentID: agg.Segments[1].ID, ExpectedVersion: agg.Task.Version, IdempotencyKey: "c3", ActualVolumeL: 10, ActualMixRatio: "1:1", MaterialBatch: "BATCH-2", PerformedAt: created.Add(4 * time.Second), Operator: "施工员", Result: domain.SegmentComplete, Actor: worker})
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := app.Preflight(ctx, task.ID)
	if err != nil || !preflight.Ready {
		t.Fatalf("预检应通过: %#v %v", preflight, err)
	}
	agg, _ = app.GetAggregate(ctx, task.ID)
	if len(agg.Reviews) != 1 || agg.Reviews[0].Snapshot.ActualVolumeL != 10 || len(agg.MaterialUsage) != 2 {
		t.Fatalf("历史或批次汇总错误: %#v", agg)
	}
}
