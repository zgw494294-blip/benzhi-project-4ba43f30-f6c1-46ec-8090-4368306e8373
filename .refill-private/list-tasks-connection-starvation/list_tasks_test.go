package listtasksconnectionstarvation_test

import (
	"context"
	"testing"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

func TestListTasksReturnsWithoutConnectionStarvation(t *testing.T) {
	repository, err := store.Open("file:list-tasks-connection-starvation?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	app := service.New(repository)
	manager := domain.Actor{Name: "负责人", Role: domain.RoleManager}
	_, err = app.CreateTask(context.Background(), service.CreateTaskInput{
		TaskCode: "LIST-1", SiteName: "一号场地", BoreholeNo: "ZK-1",
		TotalDepthM: 10, StrataSummary: "稳定岩层", Actor: manager,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tasks, err := app.ListTasks(ctx)
	if err != nil {
		t.Fatalf("任务列表不应因连接池饥饿失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskCode != "LIST-1" {
		t.Fatalf("任务列表内容错误: %#v", tasks)
	}
}
