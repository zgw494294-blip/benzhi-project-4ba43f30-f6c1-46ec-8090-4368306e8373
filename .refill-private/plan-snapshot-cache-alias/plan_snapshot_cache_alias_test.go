package plan_snapshot_cache_alias_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

func TestPlanSnapshotCacheDoesNotLeakCallerMutations(t *testing.T) {
	repository, err := store.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	ctx := context.Background()
	app := service.New(repository)
	manager := domain.Actor{Name: "现场负责人", Role: domain.RoleManager}
	task, err := app.CreateTask(ctx, service.CreateTaskInput{
		TaskCode:      "CACHE-001",
		SiteName:      "缓存隔离试验场",
		BoreholeNo:    "ZK-CACHE-01",
		TotalDepthM:   12,
		StrataSummary: "0-12m 灰岩",
		Actor:         manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.PublishPlan(ctx, task.ID, service.PublishPlanInput{
		ExpectedVersion: task.Version,
		Actor:           manager,
		Segments: []service.PlanSegmentInput{{
			Sequence:       1,
			FromDepthM:     0,
			ToDepthM:       12,
			MaterialType:   "水泥浆",
			PlannedVolumeL: 24,
			MixRatio:       "1:1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := app.PlanSnapshots(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(first[0].Segments) != 1 {
		t.Fatalf("unexpected snapshot shape: %#v", first)
	}
	first[0].PublishedBy = "被调用方篡改"
	first[0].PublishedAt = time.Time{}
	first[0].Segments[0].MaterialType = "被调用方篡改材料"

	second, err := app.PlanSnapshots(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].PublishedBy != manager.Name || second[0].PublishedAt.IsZero() || second[0].Segments[0].MaterialType != "水泥浆" {
		t.Fatalf("immutable plan history was polluted through cached aliases: %#v", second[0])
	}
}
