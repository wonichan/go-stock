package data

import (
	"fmt"
	"testing"
	"time"

	"go-stock/backend/db"
)

func initFollowedStockRollingTestDB(t *testing.T) {
	t.Helper()
	db.Init("file:followed_stock_rolling_test?mode=memory&cache=shared&_pragma=busy_timeout(10000)")
	if err := db.Dao.AutoMigrate(&FollowedStock{}, &Settings{}, &AIConfig{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := db.Dao.Exec("DELETE FROM followed_stock").Error; err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

// 同一来源重复入选必须刷新 time，让 5 个交易日窗口重新计时。
func TestFollowStockWithSourceRefreshesTimeOnSameSource(t *testing.T) {
	initFollowedStockRollingTestDB(t)
	t0 := time.Date(2026, 10, 12, 9, 30, 0, 0, time.Local)
	t1 := t0.Add(72 * time.Hour)

	created, refreshed, err := FollowStockWithSource("600519", "贵州茅台", "skill-a", t0)
	if err != nil || !created || refreshed {
		t.Fatalf("first follow: created=%v refreshed=%v err=%v", created, refreshed, err)
	}
	created, refreshed, err = FollowStockWithSource("600519", "贵州茅台", "skill-a", t1)
	if err != nil || created || !refreshed {
		t.Fatalf("re-pick same source: created=%v refreshed=%v err=%v", created, refreshed, err)
	}
	var row FollowedStock
	if err := db.Dao.Where("stock_code = ?", "sh600519").First(&row).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if !row.Time.Equal(t1) {
		t.Fatalf("time not refreshed: got %v want %v", row.Time, t1)
	}
}

// 非 VIP 已满 63 只时，脚本来源也不能新增（与 Follow() 一致）。
func TestFollowStockWithSourceRespectsNonVipLimit(t *testing.T) {
	initFollowedStockRollingTestDB(t)
	now := time.Now()
	for i := 0; i < 63; i++ {
		if err := db.Dao.Create(&FollowedStock{StockCode: fmt.Sprintf("sz%06d", i+1), Time: now}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	created, refreshed, err := FollowStockWithSource("600519", "贵州茅台", "skill-a", now)
	if err != nil || created || refreshed {
		t.Fatalf("over limit should skip: created=%v refreshed=%v err=%v", created, refreshed, err)
	}
	var n int64
	db.Dao.Model(&FollowedStock{}).Where("stock_code = ?", "sh600519").Count(&n)
	if n != 0 {
		t.Fatalf("stock was added despite the 63 limit")
	}
}
