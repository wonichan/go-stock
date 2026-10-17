package agent

import (
	"testing"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestMorningStrategyCronExprMigration(t *testing.T) {
	db.Init("file:cron_task_migrate_test?mode=memory&cache=shared&_pragma=busy_timeout(10000)")
	if err := db.Dao.AutoMigrate(&models.CronTask{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	db.Dao.Exec("DELETE FROM cron_tasks")
	api := NewCronTaskApi()
	oldDefault := &models.CronTask{Name: "盘前策略", CronExpr: "0 0 9 * * 1-5", TaskType: "morning_strategy", Enable: true}
	custom := &models.CronTask{Name: "盘前策略-自定义", CronExpr: "0 15 9 * * 1-5", TaskType: "morning_strategy", Enable: true}
	other := &models.CronTask{Name: "其它", CronExpr: "0 0 9 * * 1-5", TaskType: "daily_review", Enable: true}
	for _, task := range []*models.CronTask{oldDefault, custom, other} {
		if err := api.Create(task); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	n, err := api.MigrateCronExpr("morning_strategy", "0 0 9 * * 1-5", "0 30 9 * * 1-5")
	if err != nil || n != 1 {
		t.Fatalf("migrate: n=%d err=%v", n, err)
	}
	for id, want := range map[uint]string{oldDefault.ID: "0 30 9 * * 1-5", custom.ID: "0 15 9 * * 1-5", other.ID: "0 0 9 * * 1-5"} {
		got, err := api.GetByID(id)
		if err != nil || got.CronExpr != want {
			t.Fatalf("task %d: got %+v err=%v want %q", id, got, err, want)
		}
	}
	// 二次执行是幂等的
	if n, _ := api.MigrateCronExpr("morning_strategy", "0 0 9 * * 1-5", "0 30 9 * * 1-5"); n != 0 {
		t.Fatalf("second migrate should be a no-op, got %d", n)
	}
}

// 默认表达式必须是 6 段（应用的 cron 使用 cron.WithSeconds）。
func TestMorningStrategyDefaultCronExprParses(t *testing.T) {
	api := NewCronTaskApi()
	if err := api.ValidateCronExpr("0 30 9 * * 1-5"); err != nil {
		t.Fatalf("6-field expr should parse: %v", err)
	}
	if err := api.ValidateCronExpr("30 9 * * 1-5"); err == nil {
		t.Fatalf("5-field expr must be rejected by the seconds parser")
	}
}
