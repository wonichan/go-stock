package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
)

// MorningStrategyApi 盘前策略 API
type MorningStrategyApi struct{}

func NewMorningStrategyApi() *MorningStrategyApi {
	return &MorningStrategyApi{}
}

// morningStrategyDefaultSysPrompt 内置盘前策略师系统提示词（sysPromptId==0 时使用）
const morningStrategyDefaultSysPrompt = `你是一位拥有20年A股实战经验的顶级盘前策略师，精通隔夜外围解读、情绪周期定位与情景推演。你的任务是基于给定的真实素材，输出一份专业、克制、可执行的今日盘前策略。

硬性规则：
1. 只能基于给定素材进行分析，素材中标注"（无数据）"的部分必须如实说明数据缺失，严禁编造任何数字、个股或板块名称。
2. 每个关键判断后用括号标注数据依据。
3. 关注标的池需给出：代码/名称/关注理由/触发条件（价格或形态），且必须来自素材（寻龙诀一进二脚本结果、自选股清单、昨日复盘、龙虎榜）中出现的标的，不得凭空推荐。
4. 仓位与纪律建议要具体，风险提示具体化，不说空话。
5. 这是盘前策略，禁止给出"保证盈利"等收益承诺。
6. 本策略的核心是「一进二打板」：素材第 5 段寻龙诀脚本结果（16 闸过滤 + 10 维评分）就是今日一进二候选，重点解读其中入选个股的评分明细、竞价开盘与共振板块，不要自行重算或另找标的；该段标注"（无数据）"时如实说明脚本未产出并整体降级为观察。

输出章节（Markdown 二级标题）：
## 一、隔夜与外围
（美股/欧洲/亚太主要指数涨跌解读）
## 二、昨日复盘要点回顾
（浓缩昨日复盘结论，无数据时说明）
## 三、今日推演
（乐观/中性/悲观三情景，各给触发条件与应对）
## 四、关注方向与标的池
（基于素材的结构化标的列表）
## 五、仓位与纪律建议
## 六、风险提示`

// GenerateMorningStrategy 生成盘前策略（全流程编排，同步执行，调用方自行决定是否放协程）
func (a *MorningStrategyApi) GenerateMorningStrategy(ctx context.Context, date string, aiConfigId, sysPromptId int, thinking bool, agentMode, triggerType string) (*models.MorningStrategy, error) {
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	logger.SugaredLogger.Infof("开始生成盘前策略：%s（trigger=%s, aiConfigId=%d, sysPromptId=%d, agentMode=%s）", date, triggerType, aiConfigId, sysPromptId, agentMode)

	// 并发防抖：当日已有 generating 记录且未超时（10 分钟，防止进程被杀导致永久卡 generating）
	var existing models.MorningStrategy
	db.Dao.Where("strategy_date = ?", date).First(&existing)
	if existing.ID != 0 && existing.Status == "generating" && time.Since(existing.UpdatedAt) < 10*time.Minute {
		return nil, fmt.Errorf("%s 的盘前策略正在生成中，请稍候", date)
	}

	// cron 触发时：昨日既无复盘也无市场统计（长假期间）直接跳过
	if triggerType == "cron" {
		prev := prevTradeDate(date)
		var prevReview models.DailyReview
		db.Dao.Where("review_date = ? AND status = ?", prev, "success").First(&prevReview)
		if prevReview.ID == 0 && len(data.NewMarketStatisticApi().GetByDate(prev)) == 0 {
			logger.SugaredLogger.Infof("%s 前一交易日无复盘与市场数据（可能为长假），跳过盘前策略生成", date)
			return nil, nil
		}
	}

	// Upsert：一天一条，重新生成覆盖
	strategy := existing
	if strategy.ID == 0 {
		strategy = models.MorningStrategy{StrategyDate: date}
	}
	strategy.Status = "generating"
	strategy.TriggerType = triggerType
	strategy.AiConfigId = aiConfigId
	strategy.SysPromptId = sysPromptId
	strategy.ErrorMessage = ""
	if err := db.Dao.Save(&strategy).Error; err != nil {
		return nil, fmt.Errorf("保存盘前策略记录失败：%v", err)
	}

	// 桥接文件由宿主机 cron 在 09:26 之后写入，可能比本任务略晚；cron 触发时有界等待。
	// 手工触发不等待（用户在场，没必要卡住 3 分钟）。
	if triggerType == "cron" {
		if push := data.WaitForXunlongjueMorningPush(strings.ReplaceAll(date, "-", ""), 3*time.Minute, 10*time.Second); push != nil {
			logger.SugaredLogger.Infof("已等到寻龙诀早盘结果：%s", push.SummaryLine())
		} else {
			logger.SugaredLogger.Warnf("等待寻龙诀早盘结果超时，本次盘前策略将缺少脚本选股素材")
		}
	}

	// 寻龙诀早盘一进二脚本结果入库（推荐记录 + 自选）。放在素材拼装之前：
	// 素材第 5 段直接读桥接文件，而推荐记录/自选是落库副作用。
	// 失败只记日志，不阻断策略生成（本机可能压根没装寻龙诀）。
	if _, err := IngestXunlongjueMorningPush(time.Now(), strings.ReplaceAll(date, "-", "")); err != nil {
		logger.SugaredLogger.Warnf("寻龙诀早盘结果入库失败: %v", err)
	}

	start := time.Now()
	material := a.buildMorningStrategyMaterial(date)
	prompt := fmt.Sprintf("请生成 %s 的盘前策略。\n\n# 盘前素材\n%s", date, material)

	// sysPromptId>0 时使用提示词模板内容（ChatWithContext 内部按 ID 加载），否则注入内置盘前策略提示词
	var sysPromptOverride string
	if sysPromptId == 0 {
		sysPromptOverride = morningStrategyDefaultSysPrompt
	}
	content := &strings.Builder{}
	emitter := newProgressEmitter(ctx, "morningStrategyProgress", date)
	ch := NewStockAiAgentApi().ChatWithContext(ctx, ChatRequest{
		Question:          prompt,
		AIConfigID:        aiConfigId,
		SysPromptID:       &sysPromptId,
		ThinkingMode:      thinking,
		AgentMode:         agentMode,
		SysPromptOverride: sysPromptOverride,
	})
	for msg := range ch {
		if msg == nil {
			continue
		}
		// 只消费最终报告正文（Content）：ReasoningContent 携带的是 [STEP] 步骤/工具调用预告与摘要等中间过程，
		// 不推送前端也不落库，用户只看最终结果
		if msg.Content != "" {
			content.WriteString(msg.Content)
			emitter.write(msg.Content)
		}
	}
	emitter.flush()

	result := strings.TrimSpace(content.String())
	generatedAt := time.Now()
	if result == "" {
		strategy.Status = "failed"
		strategy.ErrorMessage = "AI 返回内容为空"
		strategy.DurationMs = time.Since(start).Milliseconds()
		db.Dao.Save(&strategy)
		a.emitEvent(ctx, date, strategy)
		return nil, fmt.Errorf("盘前策略生成失败：AI 返回内容为空")
	}

	strategy.Status = "success"
	strategy.Content = result
	strategy.Summary = extractSummary(result, 200)
	strategy.GeneratedAt = &generatedAt
	strategy.DurationMs = time.Since(start).Milliseconds()
	if err := db.Dao.Save(&strategy).Error; err != nil {
		return nil, fmt.Errorf("保存盘前策略失败：%v", err)
	}

	logger.SugaredLogger.Infof("盘前策略生成完成：%s（耗时 %dms）", date, strategy.DurationMs)
	// 同步保存到 AI 分析报告，供研究中心查看
	go data.NewDeepSeekOpenAi(ctx, aiConfigId).SaveAIResponseResult("盘前策略", "盘前策略", result, "", prompt)
	a.emitEvent(ctx, date, strategy)
	return &strategy, nil
}

// emitEvent 生成结束后向前端推送事件
func (a *MorningStrategyApi) emitEvent(ctx context.Context, date string, strategy models.MorningStrategy) {
	safeEventsEmit(ctx, "morningStrategyGenerated", map[string]any{
		"date":         date,
		"status":       strategy.Status,
		"summary":      strategy.Summary,
		"triggerType":  strategy.TriggerType,
		"errorMessage": strategy.ErrorMessage,
	})
}

// buildMorningStrategyMaterial 拼装盘前素材 Markdown（各段容错，无数据时显式标注防 AI 幻觉）
func (a *MorningStrategyApi) buildMorningStrategyMaterial(date string) string {
	sb := &strings.Builder{}
	prevDate := prevTradeDate(date)

	// 1. 昨日复盘全文（闭环核心）
	sb.WriteString("## 1. 昨日复盘报告\n")
	var prevReview models.DailyReview
	db.Dao.Where("review_date = ? AND status = ?", prevDate, "success").First(&prevReview)
	if prevReview.ID == 0 {
		// 回退：取最近一条成功复盘
		db.Dao.Where("review_date < ? AND status = ?", date, "success").Order("review_date DESC").First(&prevReview)
	}
	if prevReview.ID == 0 {
		sb.WriteString("（无数据）\n")
	} else {
		content := prevReview.Content
		if len(content) > 4000 {
			content = content[:4000] + "...(截断)"
		}
		fmt.Fprintf(sb, "昨日(%s)复盘：\n%s\n", prevReview.ReviewDate, content)
	}

	// 2. 昨日收盘市场统计快照
	sb.WriteString("\n## 2. 昨日市场统计\n")
	stats := data.NewMarketStatisticApi().GetByDate(prevDate)
	if len(stats) == 0 {
		sb.WriteString("（无数据）\n")
	} else {
		last := stats[len(stats)-1]
		fmt.Fprintf(sb, "- %s 收盘：上涨 %d 家 / 下跌 %d 家，涨停 %d 家 / 跌停 %d 家，市场情绪：%s\n",
			last.DataDate, last.UpCount, last.DownCount, last.LimitUp, last.LimitDown, last.SentimentDesc)
	}

	// 3. 隔夜外围（全球指数缓存）
	sb.WriteString("\n## 3. 隔夜外围市场\n")
	var indexes []models.GlobalStockIndex
	db.Dao.Find(&indexes)
	if len(indexes) == 0 {
		sb.WriteString("（无数据）\n")
	} else {
		// 主要指数优先（美股三大 + 亚太主要 + 欧洲主要），其余忽略避免素材过长
		keywords := []string{"道琼斯", "纳斯达克", "标普", "恒生", "日经", "韩国", "富时", "德国", "法国", "上证", "深证", "创业板"}
		for _, kw := range keywords {
			for _, idx := range indexes {
				if strings.Contains(idx.Name, kw) {
					fmt.Fprintf(sb, "- %s（%s）：%.2f%% 报 %s\n", idx.Name, idx.Location, parseZdf(idx.Zdf), idx.Zxj)
					break
				}
			}
		}
	}

	// 4. 昨日龙虎榜游资动向摘要（此时数据已可得）
	sb.WriteString("\n## 4. 昨日龙虎榜游资/机构动向\n")
	summary := data.NewLhbSeatApi().GetLhbDailySummary(prevDate)
	if summary == nil || (len(summary.HotMoneyActivities) == 0 && len(summary.InstitutionActions) == 0) {
		sb.WriteString("（无数据）\n")
	} else {
		fmt.Fprintf(sb, "上榜个股 %d 只\n", summary.StockCount)
		top := 8
		if len(summary.HotMoneyActivities) < top {
			top = len(summary.HotMoneyActivities)
		}
		for i := 0; i < top; i++ {
			hm := summary.HotMoneyActivities[i]
			fmt.Fprintf(sb, "- %s（%s）：买入 %.0f 万元 / 卖出 %.0f 万元", hm.HotMoneyName, hm.Tier, hm.TotalBuy/10000, hm.TotalSell/10000)
			if len(hm.Stocks) > 0 {
				sb.WriteString("，操作：")
				for j, st := range hm.Stocks {
					if j >= 3 {
						break
					}
					fmt.Fprintf(sb, "%s(%.2f%%) ", st.StockName, st.ChangeRate)
				}
			}
			sb.WriteString("\n")
		}
	}

	// 5. 寻龙诀早盘一进二脚本选股结果（T1 文件桥，09:26 采集）
	sb.WriteString("\n## 5. 寻龙诀早盘一进二脚本选股结果（09:26 采集）\n")
	writeXunlongjueMorningPushSection(sb)

	// 6. 自选股清单
	sb.WriteString("\n## 6. 自选股清单\n")
	var stocks []data.FollowedStock
	db.Dao.Order("sort ASC").Find(&stocks)
	if len(stocks) == 0 {
		sb.WriteString("（无数据，自选股为空）\n")
	} else {
		limit := 20
		if len(stocks) < limit {
			limit = len(stocks)
		}
		for i := 0; i < limit; i++ {
			s := stocks[i]
			fmt.Fprintf(sb, "- %s[%s] 现价 %.2f，涨跌 %.2f%%", s.Name, s.StockCode, s.Price, s.ChangePercent)
			if s.StopLossPrice > 0 || s.TakeProfitPrice > 0 {
				fmt.Fprintf(sb, "，止损 %.2f / 止盈 %.2f", s.StopLossPrice, s.TakeProfitPrice)
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// writeXunlongjueMorningPushSection 输出寻龙诀早盘一进二脚本的结构化结果。
//
// 这一段是盘前策略的事实底座：候选、10 维评分、竞价开盘、共振板块都由 Python 脚本
// 按固定规则算出（16 闸在脚本内已过滤），LLM 只负责解读与组织，不重算。
// 脚本结果不可用时显式标注，避免模型凭记忆编造一进二标的。
func writeXunlongjueMorningPushSection(sb *strings.Builder) {
	push, err := data.ReadXunlongjueMorningPush(xunlongjueMorningPushMaxAge)
	if err != nil {
		fmt.Fprintf(sb, "（读取脚本结果失败：%v）\n", err)
		return
	}
	if push == nil {
		sb.WriteString("（无数据——寻龙诀脚本未运行或结果已过期，本段缺失时不要臆测一进二标的）\n")
		return
	}
	fmt.Fprintf(sb, "%s\n", push.SummaryLine())
	if d := strings.TrimSpace(push.Market.Diagnosis); d != "" {
		fmt.Fprintf(sb, "- 市场诊断：%s\n", d)
	}
	if push.DeliveryAlert != nil {
		fmt.Fprintf(sb, "- 交割日提示：%s（还有 %d 个交易日）\n",
			push.DeliveryAlert.DeliveryDate, push.DeliveryAlert.TradingDaysUntil)
	}
	if len(push.ResonanceSectors) > 0 {
		fmt.Fprintf(sb, "- 共振板块：%s\n", strings.Join(push.ResonanceSectors, "、"))
	}
	if len(push.Pushed) > 0 {
		sb.WriteString("- 正式入选（脚本 16 闸过滤 + 10 维评分，按评分从高到低）：\n")
		for _, p := range push.Pushed {
			fmt.Fprintf(sb, "  - %s %s：综合 %.0f 分，竞价开盘 %+.2f%%，市值 %.1f 亿",
				p.Name, p.Code, p.Score, p.Gap, p.MarketCap)
			if s := strings.TrimSpace(p.Industry); s != "" {
				fmt.Fprintf(sb, "，%s", s)
			}
			if line := formatXunlongjueSubScores(p.SubScores); line != "" {
				fmt.Fprintf(sb, "；评分明细 %s", line)
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("- 正式入选：无（脚本判定今日无合格一进二标的，宁错过不做错）\n")
	}
	if len(push.Observations) > 0 {
		sb.WriteString("- 观察位（Gap 区外高分，仅供观察，不追）：\n")
		for _, p := range push.Observations {
			fmt.Fprintf(sb, "  - %s %s：综合 %.0f 分，竞价开盘 %+.2f%%\n", p.Name, p.Code, p.Score, p.Gap)
		}
	}
	if push.MaxScore != nil {
		fmt.Fprintf(sb, "- 全场最高分：%s（%.0f 分）\n", push.MaxScore.Name, push.MaxScore.Score)
	}
	fmt.Fprintf(sb, "- 漏斗统计：昨日首板 %d 家 → 候选 %d 只 → 推送 %d 只 / 观察 %d 只\n",
		push.Stats.FirstBoards, push.Stats.Candidates, push.Stats.Pushed, push.Stats.Observed)
}

// formatXunlongjueSubScores 把脚本的 10 维评分明细拼成一行（顺序固定，便于横向比较）。
// key 取自 morning_push.py::_build_pushed 的 s_xxx 字段与 bomb/resonance 附加项。
func formatXunlongjueSubScores(m map[string]float64) string {
	order := []struct{ key, label string }{
		{"gap", "竞价"}, {"cap", "市值"}, {"seal", "封单"}, {"vol", "量能"},
		{"open", "开盘"}, {"turn", "换手"}, {"strength", "强度"}, {"kdj", "KDJ"},
		{"bomb", "炸板"}, {"resonance", "共振"},
	}
	parts := make([]string, 0, len(order))
	for _, o := range order {
		if v, ok := m[o.key]; ok && v != 0 {
			parts = append(parts, fmt.Sprintf("%s %+.0f", o.label, v))
		}
	}
	return strings.Join(parts, " ")
}

// parseZdf 解析 GlobalStockIndex.Zdf（字符串涨跌幅，如 "-1.23"）
func parseZdf(zdf string) float64 {
	var v float64
	fmt.Sscanf(strings.TrimSpace(zdf), "%f", &v)
	return v
}

// GetMorningStrategyByDate 按日期查询盘前策略
func (a *MorningStrategyApi) GetMorningStrategyByDate(date string) *models.MorningStrategy {
	var strategy models.MorningStrategy
	err := db.Dao.Where("strategy_date = ?", date).First(&strategy).Error
	if err != nil {
		return nil
	}
	return &strategy
}

// GetLatestMorningStrategy 查询最近一条盘前策略
func (a *MorningStrategyApi) GetLatestMorningStrategy() *models.MorningStrategy {
	var strategy models.MorningStrategy
	err := db.Dao.Order("strategy_date DESC").First(&strategy).Error
	if err != nil {
		return nil
	}
	return &strategy
}

// GetMorningStrategyList 分页查询历史盘前策略（列表不带全文）
func (a *MorningStrategyApi) GetMorningStrategyList(page, pageSize int) *models.MorningStrategyPageData {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	var total int64
	db.Dao.Model(&models.MorningStrategy{}).Count(&total)
	var list []models.MorningStrategy
	db.Dao.Order("strategy_date DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list)
	for i := range list {
		list[i].Content = ""
	}
	totalPages := 0
	if pageSize > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return &models.MorningStrategyPageData{List: list, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages}
}

// DeleteMorningStrategy 删除盘前策略
func (a *MorningStrategyApi) DeleteMorningStrategy(id uint) error {
	return db.Dao.Delete(&models.MorningStrategy{}, id).Error
}
