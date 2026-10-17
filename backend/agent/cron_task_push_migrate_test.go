package agent

import (
	"encoding/json"
	"testing"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestReportTaskDefaultsHavePushOn(t *testing.T) {
	for name, raw := range map[string]string{"daily_review": DefaultDailyReviewTaskParams, "morning_strategy": DefaultMorningStrategyTaskParams} {
		var p reportTaskParams
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !p.PushFeishu && !p.PushDingDing {
			t.Fatalf("%s default must enable push: %s", name, raw)
		}
	}
}

func TestEnableReportPushOnce(t *testing.T) {
	db.Init("file:cron_task_push_migrate_test?mode=memory&cache=shared&_pragma=busy_timeout(10000)")
	if err := db.Dao.AutoMigrate(&models.CronTask{}, &cronMigrationMarker{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	db.Dao.Exec("DELETE FROM cron_tasks")
	db.Dao.Exec("DELETE FROM cron_migration_markers")
	api := NewCronTaskApi()
	off := `{"aiConfigId":3,"includeLhb":true,"pushFeishu":false,"pushDingDing":false}`
	review := &models.CronTask{Name: "每日复盘", CronExpr: "0 0 18 * * 1-5", TaskType: "daily_review", Enable: true, Params: off}
	morning := &models.CronTask{Name: "盘前策略", CronExpr: "0 30 9 * * 1-5", TaskType: "morning_strategy", Enable: true, Params: off}
	other := &models.CronTask{Name: "其它", CronExpr: "0 0 9 * * 1-5", TaskType: "stock_analysis", Enable: true, Params: off}
	for _, task := range []*models.CronTask{review, morning, other} {
		if err := api.Create(task); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	load := func(id uint) reportTaskParams {
		got, err := api.GetByID(id)
		if err != nil {
			t.Fatalf("load %d: %v", id, err)
		}
		var p reportTaskParams
		if err := json.Unmarshal([]byte(got.Params), &p); err != nil {
			t.Fatalf("params %d: %v", id, err)
		}
		return p
	}

	n, err := api.EnableReportPushOnce()
	if err != nil || n != 2 {
		t.Fatalf("first run: n=%d err=%v", n, err)
	}
	if p := load(review.ID); !p.PushFeishu || p.AiConfigId != 3 || !p.IncludeLhb {
		t.Fatalf("daily_review not migrated correctly: %+v", p)
	}
	if p := load(morning.ID); !p.PushFeishu {
		t.Fatalf("morning_strategy not migrated: %+v", p)
	}
	if p := load(other.ID); p.PushFeishu {
		t.Fatalf("other task types must not be touched: %+v", p)
	}

	// 用户之后关掉推送：再次启动不能改回。
	db.Dao.Model(&models.CronTask{}).Where("id = ?", review.ID).Update("params", off)
	n, err = api.EnableReportPushOnce()
	if err != nil || n != 0 {
		t.Fatalf("second run should be a no-op: n=%d err=%v", n, err)
	}
	if p := load(review.ID); p.PushFeishu {
		t.Fatalf("user opt-out was overridden: %+v", p)
	}
}
