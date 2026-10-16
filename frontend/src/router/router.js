import {createMemoryHistory, createRouter, createWebHashHistory, createWebHistory} from 'vue-router'

import stockView from '../components/stock.vue'
import settingsView from '../components/settings.vue'
import marketView from '../components/market.vue'
import research from "../components/researchIndex.vue";
import aiConfigManager from "../components/ai-config-manager.vue"
import userProfile from "../components/user-profile.vue"
import dailyReview from "../components/DailyReview.vue"
import morningStrategy from "../components/MorningStrategy.vue"
import promptBacktest from "../components/PromptBacktest.vue"
import recommendBacktestStats from "../components/RecommendBacktestStats.vue"
// 精简版：首页/基金/K线分析/合约/Agent/关于/定时任务/MCP 页面已移除（见 ../slim-config.js），不再被打包
import {DEFAULT_ROUTE} from "../slim-config"

const routes = [
    { path: '/', redirect: DEFAULT_ROUTE},
    { path: '/stock', component: stockView,name: 'stock'},
    { path: '/settings', component: settingsView,name: 'settings' },
    { path: '/market', component: marketView,name: 'market' },
    { path: '/research', component: research,name: 'research' },
    { path: '/ai-configs', component: aiConfigManager,name: 'aiConfigs' },
    { path: '/user-profile', component: userProfile,name: 'userProfile' },
    { path: '/daily-review', component: dailyReview,name: 'dailyReview' },
    { path: '/morning-strategy', component: morningStrategy,name: 'morningStrategy' },
    { path: '/prompt-backtest', component: promptBacktest,name: 'promptBacktest' },
    { path: '/recommend-backtest-stats', component: recommendBacktestStats,name: 'recommendBacktestStats' },

    // 已移除或未知的路由统一回到默认页
    { path: '/:pathMatch(.*)*', redirect: DEFAULT_ROUTE },
]

const router = createRouter({
    //history: createWebHistory(),
    history: createWebHashHistory(),
    routes,
})

export default router