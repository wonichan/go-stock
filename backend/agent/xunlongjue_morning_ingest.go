package agent

// xunlongjue_morning_ingest.go — 把寻龙诀「早盘一进二」脚本结果写入 go-stock。
//
// 用户需求（三条思路之 #1）：盘前策略采用寻龙诀的早盘一进二策略，脚本选出的个股
// 要体现在「研究中心 - 股票推荐记录」，并自动加入「股票自选」（自选只保留最近 5 个
// 交易日的脚本来源个股，滚动更新）。
//
// 数据来自 T1 文件桥（见 data/xunlongjue_bridge.go）：寻龙诀 09:26 跑完
// morning_push.py 后把 JSON 落到共享目录，这里读取、落推荐记录、加自选。
// 本文件不负责生成文档——文档由盘前策略的 LLM 环节写（见 morning_strategy_api.go）。

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/agent/tools"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
)

// XunlongjueSkillIdMorningPush 早盘一进二脚本结果的来源标识。
//
// 写入 followed_stock.skill_id 与 ai_recommend_stocks.skill_id，用于两件事：
//  1. 「自选只保留最近 5 个交易日」的滚动清理只清 skill_id 非空的记录（Q10），
//     手工加的自选股不受影响；
//  2. 推荐记录可按来源筛选，与 LLM 选股区分。
const XunlongjueSkillIdMorningPush = "xunlongjue-morning-push"

// xunlongjueMorningPushMaxAge 桥接文件的新鲜度上限。09:26 采集，盘前策略 09:26 生成；
// 超过这个时长说明是隔夜残留（寻龙诀没跑或跑失败），宁可没有素材也不要用旧数据。
const xunlongjueMorningPushMaxAge = 6 * time.Hour

// IngestXunlongjueMorningPush 读取桥接文件并把一进二选中个股写入推荐记录 + 自选。
//
// 返回值是本次新写入的推荐记录条数（去重后）。文件不存在 / 过期 / 日期不符时
// 返回 (0, nil)：这是正常状态，不是错误（本机可能压根没装寻龙诀）。
// 全部失败只记日志，不阻断盘前策略生成。
func IngestXunlongjueMorningPush(now time.Time, expectDate string) (int, error) {
	push, err := data.ReadXunlongjueMorningPush(xunlongjueMorningPushMaxAge)
	if err != nil {
		logger.SugaredLogger.Warnf("寻龙诀早盘结果读取失败: %v", err)
		return 0, nil
	}
	if push == nil {
		logger.SugaredLogger.Infof("寻龙诀早盘结果不可用（文件缺失或超过 %s），跳过脚本选股入库", xunlongjueMorningPushMaxAge)
		return 0, nil
	}
	if expectDate != "" && !push.IsForDate(expectDate) {
		logger.SugaredLogger.Warnf("寻龙诀早盘结果日期(%s)与目标交易日(%s)不符，跳过脚本选股入库", push.Date, expectDate)
		return 0, nil
	}

	logger.SugaredLogger.Infof("寻龙诀早盘结果：%s", push.SummaryLine())

	// 只收正式推送的个股（脚本内部已按 16 闸 + 10 维评分过滤，观察位不进推荐记录，
	// 避免把 Gap 区外的次优标的混进回测样本）。
	if len(push.Pushed) == 0 {
		logger.SugaredLogger.Infof("寻龙诀早盘结果本次无推送个股（%s），无需入库", push.Market.Diagnosis)
		pruneScriptFollowedStocks(now)
		return 0, nil
	}

	picks := make([]*promptBacktestPickJSON, 0, len(push.Pushed))
	for _, p := range push.Pushed {
		code := normalizePickCode(p.Code)
		if code == "" {
			continue
		}
		picks = append(picks, &promptBacktestPickJSON{
			Code:   code,
			Name:   strings.TrimSpace(p.Name),
			Rating: fmt.Sprintf("%.0f分", p.Score),
			Reason: xunlongjuePickReason(p),
		})
	}
	if len(picks) == 0 {
		return 0, nil
	}

	// 09:26 的现价就是竞价开盘价，落库时带上，推荐列表能立刻看到买点。
	priceMap := fetchPickRealtimePrices(picks)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	already := xunlongjueRecommendedCodes(dayStart, picks)

	saved := 0
	for _, p := range picks {
		suffixed := aStockSuffixedCode(p.Code)
		if already[suffixed] || already[p.Code] {
			continue
		}
		rec := &models.AiRecommendStocks{
			DataTime:        &now,
			Rating:          p.Rating,
			StockCode:       suffixed,
			StockName:       p.Name,
			RecommendReason: p.Reason,
			Remarks:         "寻龙诀早盘一进二脚本自动写入",
			SkillId:         XunlongjueSkillIdMorningPush,
			SystemPrompt:    "寻龙诀 早盘一进二策略（脚本：morning_push.py，16 闸过滤 + 10 维评分）",
			UserPrompt:      fmt.Sprintf("%s 情绪%s 系数%.2f", push.Date, push.Market.Label, push.Market.Coeff),
		}
		if info, ok := priceMap[tools.GetStockCode(suffixed)]; ok {
			rec.StockPrice = info.Price
			rec.StockPrePrice = info.PreClose
			rec.StockCurrentPrice = info.Price
			rec.StockCurrentPriceTime = info.Date + " " + info.Time
		}
		if err := data.NewAiRecommendStocksService().CreateAiRecommendStocks(rec); err != nil {
			logger.SugaredLogger.Errorf("寻龙诀选股写入推荐记录失败 %s: %v", suffixed, err)
			continue
		}
		saved++
	}
	if saved > 0 {
		logger.SugaredLogger.Infof("寻龙诀早盘一进二入选 %d 只已写入推荐记录", saved)
	}

	// 自动加入自选（TOP autoFollowTopN）+ 滚动清理脚本来源的自选股。
	autoFollowSkillPicks(picks, XunlongjueSkillIdMorningPush, now)
	return saved, nil
}

// xunlongjuePickReason 把脚本给出的量化依据拼成人类可读的推荐理由。
func xunlongjuePickReason(p data.XunlongjuePick) string {
	var b strings.Builder
	fmt.Fprintf(&b, "一进二入选：综合%.0f分", p.Score)
	fmt.Fprintf(&b, "，竞价开盘%+.2f%%", p.Gap)
	if p.MarketCap > 0 {
		fmt.Fprintf(&b, "，市值%.1f亿", p.MarketCap)
	}
	if s := strings.TrimSpace(p.Industry); s != "" {
		fmt.Fprintf(&b, "，%s", s)
	}
	if p.AuctionAmt > 0 {
		fmt.Fprintf(&b, "，竞价成交%.0f万元", p.AuctionAmt)
	}
	return b.String()
}

// xunlongjueRecommendedCodes 返回当天已经由脚本写入过推荐记录的股票代码集合，
// 用于幂等：cron 重试或手工重跑不会产生重复的推荐记录。
func xunlongjueRecommendedCodes(dayStart time.Time, picks []*promptBacktestPickJSON) map[string]bool {
	set := make(map[string]bool, len(picks))
	wanted := make([]string, 0, len(picks)*2)
	for _, p := range picks {
		wanted = append(wanted, p.Code, aStockSuffixedCode(p.Code))
	}
	if len(wanted) == 0 {
		return set
	}
	var existing []string
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("skill_id = ? AND data_time >= ? AND stock_code IN ?",
			XunlongjueSkillIdMorningPush, dayStart, wanted).
		Pluck("stock_code", &existing).Error; err != nil {
		logger.SugaredLogger.Warnf("查询当日寻龙诀推荐记录失败（按未入库处理）: %v", err)
		return set
	}
	for _, code := range existing {
		set[code] = true
	}
	return set
}

// pruneScriptFollowedStocks 单独跑一次自选滚动清理（无新增选股时也要清理过期项）。
func pruneScriptFollowedStocks(now time.Time) {
	if n, err := data.PruneScriptFollowedStocks(now, followedStockKeepTradingDays); err != nil {
		logger.SugaredLogger.Errorf("自选股滚动清理失败: %v", err)
	} else if n > 0 {
		logger.SugaredLogger.Infof("自选股滚动清理：移除 %d 只超出窗口的脚本来源自选股", n)
	}
}
