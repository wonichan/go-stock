package data

import (
	"testing"

	"go-stock/backend/db"
)

func TestEnsureDarkThemeDefaultOnce(t *testing.T) {
	db.Init("file:settings_dark_theme_migrate_test?mode=memory&cache=shared&_pragma=busy_timeout(10000)")
	if err := db.Dao.AutoMigrate(&Settings{}, &settingsMigrationMarker{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	db.Dao.Exec("DELETE FROM settings")
	db.Dao.Exec("DELETE FROM settings_migration_markers")

	light := &Settings{DarkTheme: false, OpenAiEnable: true}
	alreadyDark := &Settings{DarkTheme: true}
	if err := db.Dao.Create(light).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Create(alreadyDark).Error; err != nil {
		t.Fatal(err)
	}

	n, err := EnsureDarkThemeDefaultOnce()
	if err != nil || n != 1 {
		t.Fatalf("first run: n=%d err=%v", n, err)
	}
	var got Settings
	if err := db.Dao.First(&got, light.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.DarkTheme {
		t.Fatal("light setting was not switched to dark")
	}

	// 用户之后关掉暗黑主题：再次启动不能改回
	db.Dao.Model(&Settings{}).Where("id = ?", light.ID).Update("dark_theme", false)
	n, err = EnsureDarkThemeDefaultOnce()
	if err != nil || n != 0 {
		t.Fatalf("second run should be a no-op: n=%d err=%v", n, err)
	}
	if err := db.Dao.First(&got, light.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.DarkTheme {
		t.Fatal("user opt-out was overridden")
	}
}
