// 精简版界面配置（feat/slim-ui）：只保留 5 个菜单分区。
// 集中在此处维护，便于与上游合并：菜单按 key 过滤，路由表与研究中心标签页按同一份清单裁剪。
//
//   1. 股票自选  stock（默认首页）
//   2. 市场行情  market（全部子标签）
//   3. 复盘策略  reviewStrategy → dailyReview / morningStrategy / promptBacktest / recommendBacktestStats
//   4. 研究中心  research（仅保留下列子标签）
//   5. 设置      settings → settings / aiConfigs / userProfile

export const DEFAULT_ROUTE = '/stock'

// 顶层菜单 key → 保留的子菜单 key（null 表示保留全部子项，包括动态生成的分组）
export const KEPT_MENU = {
    stock: null,
    market: null,
    reviewStrategy: null,
    research: ['research1', 'research2', 'uplimitLadder', 'research3', 'research4',
        'research_select_stock', 'research6', 'dailyOperationPlan'],
    settings: null,
}

// 研究中心保留的标签页（与 researchIndex.vue 中 n-tab-pane 的 name 一致）
export const KEPT_RESEARCH_TABS = ['AI分析报告', '股票推荐记录', '涨停梯队', '提示词模板',
    '形态选股', '指标选股', '交易日志', '每日操作计划']

export function slimMenu(options) {
    return options
        .filter((item) => Object.prototype.hasOwnProperty.call(KEPT_MENU, item.key))
        .map((item) => {
            const keep = KEPT_MENU[item.key]
            if (keep && Array.isArray(item.children)) {
                return {...item, children: item.children.filter((c) => keep.includes(c.key))}
            }
            return item
        })
}
