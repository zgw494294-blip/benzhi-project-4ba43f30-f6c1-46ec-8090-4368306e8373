package concurrent_preflight_cache_test

import (
	"context"
	"sync"
	"testing"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

func TestConcurrentPreflightCacheIsSynchronized(t *testing.T) {
	repository, err := store.Open(t.TempDir() + "/preflight.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	app := service.New(repository)
	task, err := app.CreateTask(context.Background(), service.CreateTaskInput{
		TaskCode:      "RACE-PREFLIGHT-1",
		SiteName:      "并发预检场地",
		BoreholeNo:    "ZK-RACE-1",
		TotalDepthM:   10,
		StrataSummary: "测试地层",
		Actor:         domain.Actor{Name: "现场负责人", Role: domain.RoleManager},
	})
	if err != nil {
		t.Fatal(err)
	}

	const workers = 24
	start := make(chan struct{})
	errs := make(chan error, workers)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(workers)
	done.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			for attempt := 0; attempt < 8; attempt++ {
				_, err := app.Preflight(context.Background(), task.ID)
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发预检失败: %v", err)
	}
}
