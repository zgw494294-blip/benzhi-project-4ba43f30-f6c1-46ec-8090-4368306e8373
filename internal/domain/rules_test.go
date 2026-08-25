package domain

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestValidatePlanRequiresContinuousCoverage(t *testing.T) {
	task := SealTask{TotalDepthM: 30, Status: TaskDraft}
	segments := []SealSegment{{Sequence: 1, FromDepthM: 0, ToDepthM: 20, MaterialType: "水泥浆", PlannedVolumeL: 80, MixRatio: "1:1"}, {Sequence: 2, FromDepthM: 21, ToDepthM: 30, MaterialType: "水泥浆", PlannedVolumeL: 40, MixRatio: "1:1"}}
	if err := ValidatePlan(task, segments); err == nil || !strings.Contains(err.Error(), "不连续") {
		t.Fatalf("expected continuity error, got %v", err)
	}
	segments[1].FromDepthM = 20
	if err := ValidatePlan(task, segments); err != nil {
		t.Fatalf("continuous plan rejected: %v", err)
	}
}

func TestValidateNewTaskNormalizesAndRejectsInvalidNumbers(t *testing.T) {
	task := SealTask{TaskCode: " T-1 ", SiteName: " 场地   一 ", BoreholeNo: " zk- 01 ", CollarEasting: 100.123, CollarNorthing: 200.456, TotalDepthM: 20.125, StrataSummary: " 岩层 "}
	if err := ValidateNewTask(&task); err != nil {
		t.Fatal(err)
	}
	if task.SiteName != "场地 一" || task.BoreholeNo != "ZK-01" {
		t.Fatalf("规范化结果错误: %#v", task)
	}
	task.CollarEasting = math.Inf(1)
	if err := ValidateNewTask(&task); err == nil {
		t.Fatal("非有限坐标应被拒绝")
	}
	task.CollarEasting = 100.1234
	if err := ValidateNewTask(&task); err == nil {
		t.Fatal("超精度坐标应被拒绝")
	}
}

func TestPlanDifferenceAndProgress(t *testing.T) {
	old := []SealSegment{{Sequence: 1, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}, {Sequence: 2, MaterialType: "水泥浆", PlannedVolumeL: 20, MixRatio: "1:1"}}
	next := []SealSegment{{Sequence: 1, MaterialType: "水泥浆", PlannedVolumeL: 10, MixRatio: "1:1"}, {Sequence: 2, MaterialType: "膨润土", PlannedVolumeL: 25, MixRatio: "2:1"}}
	diff := PlanDifference(1, 2, old, next)
	if len(diff.Changed) != 3 {
		t.Fatalf("应有三项字段变更: %#v", diff)
	}
	progress := Progress([]SealSegment{{Result: SegmentComplete, PlannedVolumeL: 10, ActualVolumeL: 9}, {Result: SegmentComplete, PlannedVolumeL: 20, ActualVolumeL: 21}, {Result: SegmentPending, PlannedVolumeL: 5}})
	if progress.CompletedCount != 2 || progress.PendingCount != 1 || progress.CompletionPercent != 66.67 {
		t.Fatalf("进度汇总错误: %#v", progress)
	}
}

func TestReleaseNeedsCompletedSegmentsAndClosedDeviations(t *testing.T) {
	now := time.Now()
	aggregate := Aggregate{Task: SealTask{TotalDepthM: 10}, Segments: []SealSegment{{Sequence: 1, Result: SegmentComplete, PlannedVolumeL: 10, ActualVolumeL: 10}}, Deviations: []DeviationCase{{Code: "D1", Status: DeviationOpen}}}
	if err := CheckRelease(aggregate); err == nil {
		t.Fatal("open deviation should block release")
	}
	aggregate.Deviations[0].Status = DeviationClosed
	if _, _, err := BuildManifest(aggregate); err != nil {
		t.Fatalf("closed deviation should release: %v", err)
	}
	_ = now
}

func TestApplyConstructionCalculatesVariance(t *testing.T) {
	segment := SealSegment{Sequence: 1, PlannedVolumeL: 100, Result: SegmentPending}
	deviates, err := ApplyConstruction(&segment, ConstructionInput{ActualVolumeL: 112, ActualMixRatio: "1:1", MaterialBatch: "B-1", PerformedAt: time.Now(), Operator: "施工员", Result: SegmentComplete})
	if err != nil || !deviates || segment.VariancePercent != 12 {
		t.Fatalf("unexpected construction result: deviates=%v variance=%v err=%v", deviates, segment.VariancePercent, err)
	}
}
