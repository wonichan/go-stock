package data

// xunlongjue_bridge.go — 寻龙诀（/opt/xunlongjue）脚本结果的桥接读取层。
//
// 背景（Q5=T1 文件桥）：「寻龙诀」是一套纯 Python 脚本分析系统（16 闸过滤 + 10 维评分
// 的一进二选股），跑在宿主机上；go-stock 跑在容器里。两者不共享进程、不共享 DB，
// 只用「文件」通信：寻龙诀自己的调度在 09:26 跑完 morning_push.py 后，把它的
// 单行 JSON 输出落到共享目录，go-stock 只读该目录。
//
// 宿主目录：/opt/go-stock-web/data/xunlongjue/（docker-compose.yml 里以只读方式
// 挂进容器的 /data/xunlongjue）。可用环境变量 XUNLONGJUE_BRIDGE_DIR 覆盖。
//
// 契约由寻龙诀侧固化：/opt/xunlongjue/xunlongjue/scripts/morning_push.py 的
// _emit_json()（脚本内 ═══ JSON 输出 ═══ 段）。go-stock 只读、不改写。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	xunlongjueBridgeDirEnv      = "XUNLONGJUE_BRIDGE_DIR"
	xunlongjueBridgeDirDefault  = "/data/xunlongjue"
	xunlongjueBridgeDirFallback = "data/xunlongjue"

	// XunlongjueMorningPushFile 早盘一进二结果文件名（morning_push.py 产出）。
	XunlongjueMorningPushFile = "morning_push.json"
)

// XunlongjueBridgeDir 返回桥接目录：环境变量优先，其次容器内默认路径，
// 最后回退到相对当前工作目录的 data/xunlongjue（桌面版直接用仓库目录跑时）。
func XunlongjueBridgeDir() string {
	if v := strings.TrimSpace(os.Getenv(xunlongjueBridgeDirEnv)); v != "" {
		return v
	}
	if st, err := os.Stat(xunlongjueBridgeDirDefault); err == nil && st.IsDir() {
		return xunlongjueBridgeDirDefault
	}
	return xunlongjueBridgeDirFallback
}

// XunlongjuePick 一只入选/观察个股（morning_push.py 的 _build_pushed 输出）。
type XunlongjuePick struct {
	Code       string             `json:"code"`
	Name       string             `json:"name"`
	Score      float64            `json:"score"`
	Gap        float64            `json:"gap"`        // 竞价开盘涨幅 %
	MarketCap  float64            `json:"market_cap"` // 亿元
	Industry   string             `json:"industry"`
	SubScores  map[string]float64 `json:"sub_scores"`  // gap/cap/seal/vol/open/turn/strength/kdj/bomb/resonance
	AuctionVol float64            `json:"auction_vol"` // 万手
	AuctionAmt float64            `json:"auction_amt"` // 万元
}

// XunlongjueMorningPush 对应 morning_push.py::_emit_json 的输出块。
type XunlongjueMorningPush struct {
	Date        string `json:"date"`         // YYYYMMDD
	DisplayDate string `json:"display_date"` // 展示用

	Market struct {
		Score     float64 `json:"score"`
		Label     string  `json:"label"`
		Coeff     float64 `json:"coeff"`
		Diagnosis string  `json:"diagnosis"`
	} `json:"market"`

	DeliveryAlert *struct {
		DeliveryDate     string `json:"delivery_date"`
		TradingDaysUntil int    `json:"trading_days_until"`
	} `json:"delivery_alert"`

	Stats struct {
		Candidates  int  `json:"candidates"`
		Pushed      int  `json:"pushed"`
		Observed    int  `json:"observed"`
		IceDay      bool `json:"ice_day"`
		FirstBoards int  `json:"first_boards"`
	} `json:"stats"`

	Pushed           []XunlongjuePick `json:"pushed"`
	Observations     []XunlongjuePick `json:"observations"`
	ResonanceSectors []string         `json:"resonance_sectors"`
	MultiBoard       json.RawMessage  `json:"multi_board"`
	Sectors          json.RawMessage  `json:"sectors"`
	MaxScore         *struct {
		Name  string  `json:"name"`
		Score float64 `json:"score"`
	} `json:"max_score"`

	// CollectedAt 是文件写出时间（非脚本内容），用于判断新鲜度。
	CollectedAt time.Time `json:"-"`
}

// ReadXunlongjueMorningPush 读取早盘一进二桥接文件。
//
// 目录/文件不存在时返回 (nil, nil)：这是「本次尚未采集」的正常状态，不是错误，
// 调用方据此跳过该素材段即可（例如寻龙诀 cron 没跑、或本机没装寻龙诀）。
// maxAge > 0 且文件 mtime 早于 now-maxAge 时视为过期，同样返回 (nil, nil)。
func ReadXunlongjueMorningPush(maxAge time.Duration) (*XunlongjueMorningPush, error) {
	path := filepath.Join(XunlongjueBridgeDir(), XunlongjueMorningPushFile)
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取寻龙诀桥接文件失败 %s: %w", path, err)
	}
	if maxAge > 0 && time.Since(st.ModTime()) > maxAge {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取寻龙诀桥接文件失败 %s: %w", path, err)
	}
	var push XunlongjueMorningPush
	if err := json.Unmarshal(raw, &push); err != nil {
		return nil, fmt.Errorf("解析寻龙诀桥接文件失败 %s: %w", path, err)
	}
	push.CollectedAt = st.ModTime()
	return &push, nil
}

// WaitForXunlongjueMorningPush 有界轮询等待当日桥接文件出现。
//
// 用途：宿主机 cron 在 09:26 之后才写文件，而盘前策略任务可能排在 09:30，
// 脚本抓行情偶尔慢几十秒。与其让素材段直接落到「无数据」，不如等一小会儿。
// 等到（或找回过期文件）即返回；超时返回 nil，调用方照常降级处理。
func WaitForXunlongjueMorningPush(expectDate string, timeout, interval time.Duration) *XunlongjueMorningPush {
	deadline := time.Now().Add(timeout)
	if interval <= 0 {
		interval = 10 * time.Second
	}
	for {
		// 等待期间放宽新鲜度限制：只要日期对得上就算（date 由 JSON 内容给出）
		push, err := ReadXunlongjueMorningPush(0)
		if err == nil && push != nil && (expectDate == "" || push.IsForDate(expectDate)) {
			return push
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(interval)
		if time.Now().After(deadline) {
			return nil
		}
	}
}

// IsForDate 判断该结果是否属于指定交易日（YYYYMMDD）。
// 跨日残留的旧文件必须跳过，否则会把昨日选股当成今日选股写进推荐记录。
func (p *XunlongjueMorningPush) IsForDate(date string) bool {
	return p != nil && strings.TrimSpace(p.Date) == strings.TrimSpace(date)
}

// SummaryLine 生成一行人类可读的概况，供日志与素材段标题使用。
func (p *XunlongjueMorningPush) SummaryLine() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%s 情绪%s(%.1f/系数%.2f) 昨日首板%d 候选%d 推送%d 观察%d",
		p.DisplayDate, p.Market.Label, p.Market.Score, p.Market.Coeff,
		p.Stats.FirstBoards, p.Stats.Candidates, p.Stats.Pushed, p.Stats.Observed)
}
