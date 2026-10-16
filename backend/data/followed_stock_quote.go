package data

// followed_stock_quote.go — 自选股行情回写。
//
// 背景（Q8=B3）：「(b) 自选股恒 0 涨跌幅」。followed_stock 的 price /
// change_percent / price_change 只在 Follow() 建档那一刻被写死为 0
//（stock_data_api.go 的 FollowedStock{...ChangePercent: 0, PriceChange: 0}），
// 此后没有任何写回路径，于是自选列表的涨跌幅永远是 0。
//
// 这里提供一次批量回写：调用方已经拿到行情快照（GetStockCodeRealTimeData 是
// variadic，一次请求覆盖全部自选股），本函数只负责匹配 + 落库。
//
// 绝不改写 followed_stock.time：它是「加入自选的时间」，「只保留最近 N 个交易日」
// 的滚动清理（PruneScriptFollowedStocks）依赖它。

import (
	"strings"

	"go-stock/backend/db"
	"go-stock/backend/logger"

	"github.com/duke-git/lancet/v2/convertor"
)

// quoteDigits 取代码中的纯数字主体，用于匹配行情返回项。
// 行情接口返回的可能是 sh601138 / 601138.SH / 601138，本地存的又是另一种形态，
// 用数字主体匹配可以绕开这些差异。
func quoteDigits(code string) string {
	var b strings.Builder
	for _, r := range code {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// UpdateFollowedStockQuotes 把行情快照回写到自选股（现价 / 涨跌幅 / 涨跌额）。
//
// rows 是本次要回写的自选股记录（须含主键），infos 是对应的行情快照。
// 涨跌幅与涨跌额用「现价 vs 昨收」现算：GetStockCodeRealTimeData 的 A 股分支
// 不填 StockInfo.ChangePercent（只有虚拟币分支填），所以不能直接用那个字段。
//
// 价格取不到（停牌 / 非交易时段 / 匹配不到）的记录会被跳过，不会把已有值抹成 0。
// 返回实际回写的行数。
func UpdateFollowedStockQuotes(rows []FollowedStock, infos *[]StockInfo) int {
	if len(rows) == 0 || infos == nil || len(*infos) == 0 {
		return 0
	}

	byDigits := make(map[string]*StockInfo, len(*infos))
	for i := range *infos {
		info := &(*infos)[i]
		if d := quoteDigits(info.Code); d != "" {
			byDigits[d] = info
		}
	}

	updated := 0
	for i := range rows {
		row := &rows[i]
		info, ok := byDigits[quoteDigits(row.StockCode)]
		if !ok {
			continue
		}
		price, _ := convertor.ToFloat(info.Price)
		if price <= 0 {
			continue
		}
		preClose := info.PrePrice
		if preClose <= 0 {
			preClose, _ = convertor.ToFloat(info.PreClose)
		}
		changePrice := 0.0
		changePercent := 0.0
		if preClose > 0 {
			changePrice = price - preClose
			changePercent = changePrice / preClose * 100
		}
		// 表无主键 / 无 id 列，按 stock_code 定位（软删标记会自动附加 is_del = 0）
		if e := db.Dao.Model(&FollowedStock{}).
			Where("stock_code = ?", row.StockCode).
			Updates(map[string]any{
				"price":          price,
				"price_change":   changePrice,
				"change_percent": changePercent,
			}).Error; e != nil {
			logger.SugaredLogger.Errorf("回写自选股行情失败 %s: %v", row.StockCode, e)
			continue
		}
		updated++
	}
	if updated > 0 {
		logger.SugaredLogger.Debugf("已回写 %d 只自选股的现价与涨跌幅", updated)
	}
	return updated
}
