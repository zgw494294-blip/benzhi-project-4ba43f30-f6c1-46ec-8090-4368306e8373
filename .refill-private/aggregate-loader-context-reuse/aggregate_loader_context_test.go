package aggregate_loader_context_reuse_test

import (
	"context"
	"errors"
	"testing"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

func TestAggregateLoaderDoesNotReuseCanceledRequestContext(t *testing.T) {
	repository, err := store.Open("file:aggregate-loader-context-reuse?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	app := service.New(repository)
	manager := domain.Actor{Name: "现场负责人", Role: domain.RoleManager}
	task, err := app.CreateTask(context.Background(), service.CreateTaskInput{
		TaskCode:      "CTX-001",
		SiteName:      "上下文复现场地",
		BoreholeNo:    "ZK-CTX-01",
		TotalDepthM:   12,
		StrataSummary: "砂岩",
		Actor:         manager,
	})
	if err != nil {
		t.Fatal(err)
	}

	requestCtx, finishRequest := context.WithCancel(context.Background())
	first, err := app.GetAggregate(requestCtx, task.ID)
	if err != nil || first.Task.ID != task.ID {
		t.Fatalf("首次聚合读取失败: task=%q err=%v", first.Task.ID, err)
	}
	finishRequest()

	second, err := app.GetAggregate(context.Background(), task.ID)
	if errors.Is(err, context.Canceled) {
		t.Fatalf("新请求继承了已结束请求的 context: %v", err)
	}
	if err != nil {
		t.Fatalf("新请求读取聚合失败: %v", err)
	}
	if second.Task.ID != task.ID {
		t.Fatalf("新请求返回了错误任务: got %q want %q", second.Task.ID, task.ID)
	}
}
