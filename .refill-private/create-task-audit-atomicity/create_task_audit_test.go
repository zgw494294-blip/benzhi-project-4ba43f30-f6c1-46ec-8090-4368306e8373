package createtaskauditatomicity_test

import (
	"context"
	"testing"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

func TestCreateTaskRollsBackWhenAuditAppendFails(t *testing.T) {
	repository, err := store.Open("file:create-task-audit-atomicity?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	_, err = repository.DB().ExecContext(context.Background(), `
		CREATE TRIGGER reject_task_audit
		BEFORE INSERT ON audit_events
		WHEN NEW.action = 'task.created'
		BEGIN
			SELECT RAISE(ABORT, 'forced task audit failure');
		END`)
	if err != nil {
		t.Fatal(err)
	}

	app := service.New(repository)
	task, err := app.CreateTask(context.Background(), service.CreateTaskInput{
		TaskCode: "ATOMIC-1", SiteName: "三号场地", BoreholeNo: "ZK-3",
		TotalDepthM: 10, StrataSummary: "稳定岩层",
		Actor: domain.Actor{Name: "负责人", Role: domain.RoleManager},
	})
	if err == nil {
		t.Fatal("审计写入被拒绝时建档应返回失败")
	}
	_, lookupErr := repository.GetTask(context.Background(), task.ID)
	if domain.KindOf(lookupErr) != domain.KindNotFound {
		t.Fatalf("建档失败后不应残留无审计任务: task=%s lookupErr=%v", task.ID, lookupErr)
	}
}
