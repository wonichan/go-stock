<script setup>
import {computed, h, onBeforeMount, onBeforeUnmount, onMounted,onUnmounted, ref,reactive} from 'vue'
import {GetAIResponseResultList} from "../../wailsjs/go/main/App";
import {NButton, NEllipsis, NText} from "naive-ui";
import ResearchReport from "./researchReport.vue";
import AiRecommendStocksList from "./aiRecommendStocksList.vue";
import PromptTemplateList from "./promptTemplateList.vue";
import AllStockList from "./allStockList.vue";
import AllStockInfoList from "./allStockInfoList.vue";
import TradingRecordManager from "./TradingRecordManager.vue";
import UplimitLadder from "./uplimitLadder.vue";
import SelectStock from "./SelectStock.vue";
import DailyOperationPlan from "./DailyOperationPlan.vue";
import {EventsOff, EventsOn} from "../../wailsjs/runtime";
import {useRoute} from 'vue-router'
// 精简版：定时任务/MCP服务/技能管理/知识库管理/异动监控/提示词广场/问答广场 标签页已移除（见 ../slim-config.js）
import {KEPT_RESEARCH_TABS} from "../slim-config";


const nowTab = ref("AI分析报告")
const route = useRoute()
onBeforeMount(() => {
  updateTab(route.query.name)
})

onBeforeUnmount(() => {
  EventsOff("changeResearchTab")
})

onUnmounted(() => {

});

EventsOn("changeResearchTab", async (msg) => {
  console.log("changeResearchTab", msg)
  updateTab(msg.name)
})
function updateTab(name) {
  // 未知或已移除的标签页回落到第一个保留标签
  nowTab.value = KEPT_RESEARCH_TABS.includes(name) ? name : KEPT_RESEARCH_TABS[0]
}
</script>

<template>
  <n-card>
    <n-tabs type="line" animated @update-value="updateTab" :value="nowTab" style="--wails-draggable:no-drag">
      <n-tab-pane name="AI分析报告">
        <ResearchReport/>
      </n-tab-pane>
      <n-tab-pane name="股票推荐记录">
        <AiRecommendStocksList/>
      </n-tab-pane>
      <n-tab-pane name="涨停梯队">
        <UplimitLadder/>
      </n-tab-pane>
      <n-tab-pane name="提示词模板">
        <PromptTemplateList/>
      </n-tab-pane>
      <n-tab-pane name="形态选股">
        <AllStockList/>
      </n-tab-pane>
      <n-tab-pane name="指标选股">
        <SelectStock/>
      </n-tab-pane>
      <n-tab-pane name="交易日志">
        <TradingRecordManager />
      </n-tab-pane>
      <n-tab-pane name="每日操作计划">
        <DailyOperationPlan/>
      </n-tab-pane>
<!--      <n-tab-pane name="全部股票信息">-->
<!--        <AllStockInfoList/>-->
<!--      </n-tab-pane>-->
    </n-tabs>
  </n-card>
</template>

<style scoped>
</style>
