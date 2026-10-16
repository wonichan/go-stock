package data

// 自选股滚动清理（需求来自「盘前/选好票自动加自选，只保留最近 5 个交易日」）：
// 脚本来源的自选股只在「最近 N 个交易日」窗口内保留，窗口之外的软删除；
// 手动加入的自选股（skill_id 为空）不受影响。
//
// 交易日判断统一走 holiday_cache.go 的 IsTradingDayInfo / PrevTradingDayBefore，
// 不要用 daily_review_api.go 的 prevTradeDate（它只跳周末、不处理法定节假日）。

import (
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/logger"
)

// CurrentTradingDay 返回不晚于 t 的最近一个 A 股交易日（当天为交易日则返回当天）。
func CurrentTradingDay(t time.Time) (time.Time, error) {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	trading, _, _, err := IsTradingDayInfo(day)
	if err != nil {
		return time.Time{}, err
	}
	if trading {
		return day, nil
	}
	return PrevTradingDayBefore(day, 30)
}

// FollowedStockWindowStart 返回「最近 keepTradingDays 个交易日」窗口的起始日（含当日）。
// 例：keepTradingDays=5 且当前交易日为 T，则返回 T 往前数第 4 个交易日，
// 窗口为 [start, T] 共 5 个交易日。
func FollowedStockWindowStart(now time.Time, keepTradingDays int) (time.Time, error) {
	if keepTradingDays <= 0 {
		keepTradingDays = 1
	}
	cur, err := CurrentTradingDay(now)
	if err != nil {
		return time.Time{}, err
	}
	if keepTradingDays == 1 {
		return cur, nil
	}
	return NthTradingDayBefore(cur, keepTradingDays-1)
}

// PruneScriptFollowedStocks 按「最近 keepTradingDays 个交易日」窗口滚动清理脚本来源的自选股。
// 只清理带来源标记（skill_id 非空）的记录，手动加入的自选股不受影响。返回被清理的记录数。
func PruneScriptFollowedStocks(now time.Time, keepTradingDays int) (int64, error) {
	start, err := FollowedStockWindowStart(now, keepTradingDays)
	if err != nil {
		return 0, err
	}
	res := db.Dao.Model(&FollowedStock{}).
		Where("skill_id IS NOT NULL AND skill_id <> ''").
		Where("time < ?", start).
		Delete(&FollowedStock{})
	if res.Error != nil {
		return 0, res.Error
	}
	logger.SugaredLogger.Infof("自选股滚动清理：窗口起点 %s（保留最近 %d 个交易日），清理脚本来源记录 %d 条",
		start.Format("2006-01-02"), keepTradingDays, res.RowsAffected)
	return res.RowsAffected, nil
}

// FollowStockWithSource 把一个来源明确的股票加入自选（供脚本/AI 选股结果落库使用）。
//
// 与 Follow() 的区别：
//   - 不调用 GetStockCodeRealTimeData，因此在盘后 / 停牌 / 非交易时段也不会因取不到行情而失败；
//   - 不套用 Follow() 的 63 只上限与 VIP 判断（盘前/选好票要由调用方按 TOP N 控制数量）；
//   - 记录来源标记（skill_id），供「只清理脚本来源」的滚动窗口使用。
//
// 已存在时：
//   - 若已存在记录是脚本来源（skill_id 非空），刷新 skill_id 与加入时间（重新入选 → 窗口重新计时）；
//   - 若是手动加入（skill_id 为空），保持原样，不覆盖用户自己的记录。
//
// 返回 didCreate 表示是否新建，didRefresh 表示是否刷新了窗口计时。
func FollowStockWithSource(stockCode, stockName, skillId string, now time.Time) (didCreate, didRefresh bool, err error) {
	code := normalizeStockCode(strings.TrimSpace(stockCode))
	if code == "" {
		return false, false, nil
	}

	var existing FollowedStock
	found := db.Dao.Model(&FollowedStock{}).
		Where("stock_code = ?", code).
		First(&existing).Error == nil
	if found {
		if strings.TrimSpace(existing.SkillId) == "" {
			// 手动加入的自选股：不动。
			return false, false, nil
		}
		if existing.SkillId == skillId {
			return false, false, nil
		}
		if e := db.Dao.Model(&FollowedStock{}).
			Where("stock_code = ?", code).
			Updates(map[string]any{"skill_id": skillId, "time": now}).Error; e != nil {
			return false, false, e
		}
		return false, true, nil
	}

	maxSort := int64(0)
	db.Dao.Model(&FollowedStock{}).Raw("select max(sort) as sort from followed_stock").Scan(&maxSort)

	row := &FollowedStock{
		StockCode:          code,
		Name:               strings.TrimSpace(stockName),
		Time:               now,
		Sort:               maxSort + 1,
		SkillId:            skillId,
		AlarmChangePercent: 3,
	}
	if e := db.Dao.Create(row).Error; e != nil {
		return false, false, e
	}
	return true, false, nil
}
