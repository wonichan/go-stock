package agent

// xunlongjue_bridge_smoke_test.go — 桥接读取 + 素材段拼装的冒烟测试（全离线，不碰网络/DB）。
// 用法：go test ./backend/agent/ -run TestSmokeXunlongjueBridge -v

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/data"
)

const smokeJSON = `{
  "date": "20261016",
  "display_date": "2026年10月16日(周五)",
  "market": {"score": 62, "label": "偏暖", "coeff": 1.1, "diagnosis": "首板家数回升，接力意愿改善"},
  "delivery_alert": {"delivery_date": "2026-10-16", "trading_days_until": 0},
  "stats": {"candidates": 7, "pushed": 2, "observed": 3, "ice_day": false, "first_boards": 31},
  "pushed": [
    {"code": "601138", "name": "工业富联", "score": 78, "gap": 3.21, "market_cap": 4200.5,
     "industry": "消费电子", "sub_scores": {"gap": 12, "cap": 5, "seal": 18, "vol": 9, "open": 7, "turn": 6, "strength": 11, "kdj": 5, "bomb": -3, "resonance": 8},
     "auction_vol": 12.3, "auction_amt": 8600.0},
    {"code": "000625", "name": "长安汽车", "score": 71, "gap": 1.85, "market_cap": 1180.2,
     "industry": "汽车整车", "sub_scores": {"gap": 8, "cap": 6, "seal": 15, "vol": 7, "open": 6, "turn": 8, "strength": 9, "kdj": 4, "resonance": 6},
     "auction_vol": 8.1, "auction_amt": 4200.0}
  ],
  "observations": [
    {"code": "300750", "name": "宁德时代", "score": 66, "gap": 5.90, "market_cap": 9800.0, "industry": "电池"}
  ],
  "multi_board": {"3": 1, "2": 4},
  "resonance_sectors": ["消费电子", "汽车整车"],
  "sectors": {"消费电子": 9, "汽车整车": 7},
  "max_score": {"name": "工业富联", "score": 78}
}`

func TestSmokeXunlongjueBridge(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, data.XunlongjueMorningPushFile), []byte(smokeJSON), 0o644); err != nil {
		t.Fatalf("写样例失败: %v", err)
	}
	t.Setenv("XUNLONGJUE_BRIDGE_DIR", dir)

	push, err := data.ReadXunlongjueMorningPush(6 * time.Hour)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if push == nil {
		t.Fatal("读取返回 nil（应读到样例）")
	}
	if !push.IsForDate("20261016") {
		t.Errorf("IsForDate 判定错误: %s", push.Date)
	}
	if len(push.Pushed) != 2 || len(push.Observations) != 1 {
		t.Fatalf("条数解析错误: pushed=%d observations=%d", len(push.Pushed), len(push.Observations))
	}
	if got := push.Pushed[1].SubScores["resonance"]; got != 6 {
		t.Errorf("sub_scores 解析错误: resonance=%v", got)
	}
	t.Logf("SummaryLine: %s", push.SummaryLine())

	var sb strings.Builder
	writeXunlongjueMorningPushSection(&sb)
	t.Logf("素材段输出:\n%s", sb.String())

	out := sb.String()
	for _, want := range []string{"工业富联", "601138", "竞价开盘 +3.21%", "共振板块：消费电子、汽车整车", "观察位", "漏斗统计"} {
		if !strings.Contains(out, want) {
			t.Errorf("素材段缺少 %q", want)
		}
	}

	// 日期不符时必须跳过
	if err := os.WriteFile(filepath.Join(dir, data.XunlongjueMorningPushFile), []byte(smokeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, _ := data.ReadXunlongjueMorningPush(0)
	if stale.IsForDate("20261017") {
		t.Error("不同日期不应判定为当日结果")
	}
}
