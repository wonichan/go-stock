package agent

// xunlongjue_pick_api.go — 「选好票」入口（Q9=S1）。
//
// 背景：寻龙诀 skill 原本用脚本算分选股。按用户决策（Q9=S1），「选好票」改为调用
// go-stock 的这个入口：用 prompt_templates 里的【七维选股策略】提示词跑一轮 AI 分析，
// 结果由既有自动保存链路写入「研究中心 - 股票推荐记录」，并按技能来源自动加入自选
// （TOP5 + 「只保留最近 5 个交易日」滚动清理，见 auto_recommend_saver.go）。
//
// 为什么本入口不自己写推荐记录：ChatWithContext 收尾时会调 autoSaveRecommendRecords，
// 并把 AgentMeta.SkillId 一并写进记录（agent_api.go 的 `go autoSaveRecommendRecords`），
// 这里再写一次必然重复。所以本入口只负责「起一轮带技能标记的分析」，然后按
// (skill_id, id) 把刚落库的记录读回来返回。
//
// Q19=同步等：PickAStock 阻塞到分析完成（七维 + 154 个工具，实测量级约 4 分钟），
// 调用方（寻龙诀）必须按分钟级设置超时。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
)

const (
	// XunlongjueSkillIdPickAStock 「选好票」入口的推荐来源标记：写入
	// ai_recommend_stocks.skill_id，同时驱动「自动加入自选 + 滚动清理」。
	// 与早盘一进二的 XunlongjueSkillIdMorningPush 区分，互不干扰。
	XunlongjueSkillIdPickAStock = "xunlongjue-pick-a-stock"

	// XunlongjuePickPromptName 【七维选股策略】模板名（按名字解析，避免写死 id）。
	XunlongjuePickPromptName = "七维选股策略"

	// xunlongjuePickDefaultQuestion 默认提问。策略正文在模板里，这里刻意不重复策略内容。
	xunlongjuePickDefaultQuestion = "请按上述策略推荐 5 只上涨概念最大的股票。"

	// xunlongjuePickSaveWait 等待收尾保存落库的上限。
	// autoSaveRecommendRecords 跑在 goroutine 里，含一次实时行情 HTTP 请求，给它留足时间。
	xunlongjuePickSaveWait = 45 * time.Second
	xunlongjuePickSavePoll = 500 * time.Millisecond
	// xunlongjuePickFollowWait 推荐记录落库后，等待同一 goroutine 里紧随其后的自动加自选完成的上限。
	xunlongjuePickFollowWait = 10 * time.Second

	// xunlongjuePickMaxReturn 返回给调用方的推荐条数上限。
	xunlongjuePickMaxReturn = 20
)

// ErrXunlongjuePickRunning 已有一轮「选好票」在跑时返回，调用方稍后重试即可。
var ErrXunlongjuePickRunning = errors.New("选好票已在运行中，请等待上一轮完成后再试")

// xunlongjuePickMu 串行化 PickAStock：一轮约 4 分钟且结果按 (skill_id, id > beforeID)
// 认领，并发跑会互相认领对方落库的记录，因此同一时刻只允许一轮。
var xunlongjuePickMu sync.Mutex

// XunlongjuePickApi 「选好票」入口。
type XunlongjuePickApi struct{}

// NewXunlongjuePickApi 创建「选好票」入口。
func NewXunlongjuePickApi() *XunlongjuePickApi {
	return &XunlongjuePickApi{}
}

// XunlongjuePickItem 返回给调用方的个股条目。
type XunlongjuePickItem struct {
	Code   string `json:"code"` // 6 位 A 股代码
	Name   string `json:"name"`
	Rating string `json:"rating"`
	Reason string `json:"reason"`
}

// XunlongjuePickResult 「选好票」的返回结构（同步返回，JSON 友好）。
type XunlongjuePickResult struct {
	Date        string               `json:"date"` // 分析日期 YYYY-MM-DD
	SkillId     string               `json:"skillId"`
	SysPromptId int                  `json:"sysPromptId"`
	DurationMs  int64                `json:"durationMs"`
	Report      string               `json:"report"` // AI 最终正文（Markdown）
	Picks       []XunlongjuePickItem `json:"picks"`  // 已写入推荐记录并加入自选的个股
	RecordCount int                  `json:"recordCount"`
	Followed    bool                 `json:"followed"` // 本轮是否至少有一只个股实际加入（或刷新）了自选
}

// ResolveXunlongjuePickPromptID 按名字解析【七维选股策略】模板 ID。
// 同名多条时取 id 最小的一条；找不到返回 0。
func ResolveXunlongjuePickPromptID() int {
	templates := data.PromptTemplateApi{}.GetPromptTemplates(XunlongjuePickPromptName, "")
	if templates == nil || len(*templates) == 0 {
		return 0
	}
	best := 0
	for _, tpl := range *templates {
		// 优先精确同名，其次退化为 LIKE 命中的第一条（id 最小）
		if tpl.Name == XunlongjuePickPromptName {
			return tpl.ID
		}
		if best == 0 || tpl.ID < best {
			best = tpl.ID
		}
	}
	return best
}

// PickAStock 同步跑一轮【七维选股策略】，选出的个股由收尾保存链路写入推荐记录并加入自选。
//
// aiConfigId 为 0 时用第一个 AI 配置；sysPromptId 为 0 时按名字解析【七维选股策略】。
// agentMode 为空串时由 ChatWithContext 按提问复杂度自动选择。
func (a *XunlongjuePickApi) PickAStock(ctx context.Context, aiConfigId, sysPromptId int, agentMode string) (*XunlongjuePickResult, error) {
	if !xunlongjuePickMu.TryLock() {
		return nil, ErrXunlongjuePickRunning
	}
	defer xunlongjuePickMu.Unlock()

	if sysPromptId <= 0 {
		sysPromptId = ResolveXunlongjuePickPromptID()
	}
	if sysPromptId <= 0 {
		return nil, fmt.Errorf("未找到【%s】提示词模板，请在「提示词管理」中确认模板存在，或显式传入 sysPromptId", XunlongjuePickPromptName)
	}
	aiConfigId = FirstAiConfigId(aiConfigId)

	// 记录本轮开始前的最大 id：收尾保存跑在 goroutine 里，只能靠「新增的行」来认领结果。
	// 不用时间戳比较，避免 created_at 的时区/格式差异造成误判。
	var beforeID int
	db.Dao.Model(&models.AiRecommendStocks{}).
		Where("skill_id = ?", XunlongjueSkillIdPickAStock).
		Select("COALESCE(MAX(id), 0)").Scan(&beforeID)

	start := time.Now()
	logger.SugaredLogger.Infof("选好票开始：aiConfigId=%d sysPromptId=%d agentMode=%q", aiConfigId, sysPromptId, agentMode)

	content := &strings.Builder{}
	ch := NewStockAiAgentApi().ChatWithContext(ctx, ChatRequest{
		Question:     xunlongjuePickDefaultQuestion,
		AIConfigID:   aiConfigId,
		SysPromptID:  &sysPromptId,
		AgentMode:    agentMode,
		SkillDirName: XunlongjueSkillIdPickAStock,
	})
	for msg := range ch {
		if msg == nil {
			continue
		}
		// 只收最终正文：ReasoningContent 是步骤与工具调用预告
		if msg.Content != "" {
			content.WriteString(msg.Content)
		}
	}

	report := strings.TrimSpace(content.String())
	durationMs := time.Since(start).Milliseconds()
	if report == "" {
		return nil, fmt.Errorf("选好票失败：AI 返回内容为空（aiConfigId=%d, sysPromptId=%d, agentMode=%q）", aiConfigId, sysPromptId, agentMode)
	}

	rows := waitNewSkillRecommendations(XunlongjueSkillIdPickAStock, beforeID, xunlongjuePickSaveWait, xunlongjuePickSavePoll)
	if len(rows) == 0 {
		// 报告有内容但没落推荐记录：多半是模型没按【推荐记录保存】规则输出。不静默。
		logger.SugaredLogger.Warnf("选好票已生成报告但未落推荐记录（skill_id=%s，耗时 %dms）：模型可能未输出推荐契约", XunlongjueSkillIdPickAStock, durationMs)
	}

	result := &XunlongjuePickResult{
		Date:        time.Now().Format("2006-01-02"),
		SkillId:     XunlongjueSkillIdPickAStock,
		SysPromptId: sysPromptId,
		DurationMs:  durationMs,
		Report:      report,
		Picks:       make([]XunlongjuePickItem, 0, len(rows)),
		RecordCount: len(rows),
	}
	for _, row := range rows {
		code := normalizePickCode(row.StockCode)
		if code == "" {
			code = row.StockCode
		}
		result.Picks = append(result.Picks, XunlongjuePickItem{
			Code:   code,
			Name:   row.StockName,
			Rating: row.Rating,
			Reason: row.RecommendReason,
		})
	}
	result.Followed = waitSkillPicksFollowed(XunlongjueSkillIdPickAStock, result.Picks, start, xunlongjuePickFollowWait, xunlongjuePickSavePoll)
	logger.SugaredLogger.Infof("选好票完成：落推荐记录 %d 条，加入自选=%v，耗时 %dms", len(rows), result.Followed, durationMs)
	return result, nil
}

// waitNewSkillRecommendations 轮询等待该 skill 新增的推荐记录落库。
//
// autoSaveRecommendRecords 由 ChatWithContext 以 `go` 方式触发（agent_api.go），
// 通道关闭时它可能还没写完，因此这里按 id > beforeID 轮询认领；超时返回 nil。
func waitNewSkillRecommendations(skillId string, beforeID int, timeout, interval time.Duration) []models.AiRecommendStocks {
	deadline := time.Now().Add(timeout)
	for {
		var rows []models.AiRecommendStocks
		err := db.Dao.Where("skill_id = ? AND id > ?", skillId, beforeID).
			Order("id ASC").Limit(xunlongjuePickMaxReturn).Find(&rows).Error
		if err != nil {
			logger.SugaredLogger.Errorf("查询选好票推荐记录失败: %v", err)
		} else if len(rows) > 0 {
			return rows
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(interval)
	}
}

// waitSkillPicksFollowed 判断本轮选股是否实际加入（或刷新）了自选：自动加自选在
// autoSaveRecommendRecords 的 goroutine 里、推荐记录落库之后执行，因此短暂轮询。
// 判定口径：followed_stock 里存在该来源（skill_id）且加入时间不早于本轮开始的记录，
// 且代码命中本轮 picks。手动自选（skill_id 为空）、63 只上限被跳过的都不算。
func waitSkillPicksFollowed(skillId string, picks []XunlongjuePickItem, since time.Time, timeout, interval time.Duration) bool {
	if len(picks) == 0 {
		return false
	}
	codes := make(map[string]bool, len(picks))
	for _, p := range picks {
		if c := normalizePickCode(p.Code); c != "" {
			codes[c] = true
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		var rows []data.FollowedStock
		if err := db.Dao.Model(&data.FollowedStock{}).Where("skill_id = ?", skillId).Find(&rows).Error; err != nil {
			logger.SugaredLogger.Errorf("查询选好票自选记录失败: %v", err)
		}
		for _, r := range rows {
			if codes[normalizePickCode(r.StockCode)] && !r.Time.Before(since) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
