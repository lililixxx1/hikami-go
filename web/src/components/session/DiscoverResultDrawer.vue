<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { DiscoverResult } from '@/api/types'

const props = defineProps<{
  visible: boolean
  loading: boolean
  result: DiscoverResult[] | null
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const router = useRouter()

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const items = computed(() => props.result ?? [])
const createdItems = computed(() => items.value.filter((item) => item.created && !item.error))
const skippedItems = computed(() => items.value.filter((item) => !item.created && !item.error))
const errorItems = computed(() => items.value.filter((item) => Boolean(item.error)))

function openSession(sessionId: string): void {
  if (!sessionId) return
  drawerVisible.value = false
  router.push(`/sessions/${sessionId}`)
}
</script>

<template>
  <el-drawer
    v-model="drawerVisible"
    title="发现回放结果"
    direction="rtl"
    size="520px"
  >
    <div v-loading="loading" class="discover-drawer">
      <div class="result-stats">
        <div class="stat-item success">
          <span>新建</span>
          <strong>{{ createdItems.length }}</strong>
        </div>
        <div class="stat-item">
          <span>跳过</span>
          <strong>{{ skippedItems.length }}</strong>
        </div>
        <div class="stat-item danger">
          <span>错误</span>
          <strong>{{ errorItems.length }}</strong>
        </div>
      </div>

      <el-empty v-if="!loading && items.length === 0" description="暂无发现结果" />

      <div v-else class="result-list">
        <div
          v-for="item in items"
          :key="`${item.channel_id}-${item.source_id}-${item.task_id}`"
          class="result-row"
          :class="{ 'is-error': item.error, 'is-created': item.created && !item.error }"
        >
          <div class="row-main">
            <div class="row-title">{{ item.title || item.source_id || '-' }}</div>
            <el-tag v-if="item.error" type="danger" size="small">错误</el-tag>
            <el-tag v-else-if="item.created" type="success" size="small">新建</el-tag>
            <el-tag v-else type="info" size="small">跳过</el-tag>
          </div>

          <div class="row-fields">
            <span>主播：{{ item.channel_id || '-' }}</span>
            <span>来源：{{ item.source_id || '-' }}</span>
            <span>
              场次：
              <el-button
                v-if="item.session_id"
                type="primary"
                link
                size="small"
                @click="openSession(item.session_id)"
              >
                {{ item.session_id }}
              </el-button>
              <template v-else>-</template>
            </span>
            <span>任务：{{ item.task_id || '-' }}</span>
          </div>

          <div v-if="item.error" class="row-error">{{ item.error }}</div>
        </div>
      </div>
    </div>
  </el-drawer>
</template>

<style scoped>
.discover-drawer {
  min-height: 240px;
}

.result-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 16px;
}

.stat-item {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  padding: 12px;
  background: #fff;
}

.stat-item span {
  display: block;
  color: #909399;
  font-size: 12px;
  margin-bottom: 6px;
}

.stat-item strong {
  color: #303133;
  font-size: 22px;
}

.stat-item.success strong {
  color: #67c23a;
}

.stat-item.danger strong {
  color: #f56c6c;
}

.result-list {
  display: grid;
  gap: 10px;
}

.result-row {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  padding: 12px;
  background: #fff;
}

.result-row.is-created {
  border-color: #d1edc4;
}

.result-row.is-error {
  border-color: #fcd3d3;
}

.row-main {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
}

.row-title {
  color: #303133;
  font-weight: 600;
  line-height: 1.4;
  word-break: break-word;
}

.row-fields {
  display: grid;
  gap: 4px;
  margin-top: 10px;
  color: #606266;
  font-size: 12px;
  line-height: 1.5;
}

.row-error {
  margin-top: 8px;
  color: #f56c6c;
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
}
</style>
