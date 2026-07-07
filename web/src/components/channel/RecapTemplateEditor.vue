<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { HButton, HInput, HTextarea, HSelect, HSwitch, HDialog, HDescriptions, HCollapse, HCollapseItem } from '@/components/ui'
import { useRecapTemplateEditor } from '@/features/channel/useRecapTemplateEditor'

const props = defineProps<{
  scope: 'global' | 'channel'
  channelId?: string
}>()

// 业务逻辑全在 composable(四字段表单 + 双 scope 分发 + 预设);组件保留纯 UI 状态
const {
  loading,
  saving,
  importing,
  presetLoading,
  systemPrompt,
  userFormat,
  fanName,
  extraVars,
  resolvedTemplate,
  useCustom,
  presets,
  builtinDefaultPreset,
  usingBuiltinSystemPrompt,
  usingBuiltinUserFormat,
  loadData,
  loadPresets,
  applyPreset,
  save,
  resetToDefault,
  removeChannelTemplate,
  exportTemplate,
  importTemplateFile,
} = useRecapTemplateEditor({
  scope: () => props.scope,
  channelId: () => props.channelId || '',
})

// 纯 UI 状态(留组件:下拉绑定/弹窗开关)
const selectedPresetName = ref('')
const defaultPreviewVisible = ref(false)
const collapseOpen = ref<string[]>([])

const presetOptions = computed(() =>
  presets.value.map((p) => ({ label: p.name, value: p.name })),
)

// 变量参考(纯展示常量)
const templateVariables = [
  { name: '{{channel_name}}', desc: '主播名称' },
  { name: '{{channel_id}}', desc: '主播 ID' },
  { name: '{{date}}', desc: '直播日期 (YYYY.MM.DD)' },
  { name: '{{title}}', desc: '直播标题' },
  { name: '{{duration}}', desc: '时长 (如 2小时30分钟)' },
  { name: '{{fan_name}}', desc: '粉丝称呼' },
  { name: '{{danmaku_count}}', desc: '弹幕总数' },
  { name: '{{unique_users}}', desc: '独立弹幕用户数' },
  { name: '{{avg_per_min}}', desc: '平均每分钟弹幕数' },
  { name: '{{slug}}', desc: 'URL slug' },
]

// 应用预设:成功则清空下拉,取消(composable 返回 false)也清空
async function handleApplyPreset(name: string) {
  await applyPreset(name)
  selectedPresetName.value = ''
}

// channel 模式切换自定义模板
function handleToggleUseCustom(val: boolean): void {
  useCustom.value = val
  if (!val) removeChannelTemplate()
}

// 导入:读 file.raw.text 后交给 composable
async function handleImportFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  input.value = ''
  const content = await file.text()
  await importTemplateFile(content)
}

onMounted(async () => {
  await Promise.all([loadData(), loadPresets()])
})
watch(() => props.channelId, loadData)
</script>

<template>
  <div v-if="loading" class="form-hint">加载中…</div>
  <div v-else>
    <div class="template-toolbar">
      <HSelect
        :model-value="selectedPresetName"
        :options="presetOptions"
        :disabled="presetLoading"
        placeholder="选择模板预设"
        class="preset-select"
        @update:model-value="handleApplyPreset"
      />
      <label class="upload-btn btn btn-secondary btn-sm">
        <input type="file" accept=".json,application/json" @change="handleImportFile">
        <span v-if="importing" class="btn-spinner" />
        导入模板
      </label>
      <HButton variant="ghost" size="sm" @click="exportTemplate">导出模板</HButton>
    </div>

    <!-- 变量参考折叠面板 -->
    <HCollapse v-model="collapseOpen">
      <HCollapseItem name="vars" title="模板变量参考">
        <table class="var-table">
          <thead>
            <tr>
              <th>变量</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="v in templateVariables" :key="v.name">
              <td><code v-pre>{{ v.name }}</code></td>
              <td>{{ v.desc }}</td>
            </tr>
          </tbody>
        </table>
      </HCollapseItem>
    </HCollapse>

    <!-- Channel 模式: 使用自定义模板开关 -->
    <div v-if="scope === 'channel'" class="custom-toggle">
      <HSwitch :model-value="useCustom" @update:model-value="handleToggleUseCustom">
        {{ useCustom ? '使用自定义模板' : '跟随全局模板' }}
      </HSwitch>
    </div>

    <!-- 全局模式 或 channel 模式开启自定义 -->
    <template v-if="scope === 'global' || useCustom">
      <!-- System Prompt -->
      <HTextarea v-model="systemPrompt" :rows="10" placeholder="留空使用内置默认 System Prompt">
        <template #label>System Prompt</template>
      </HTextarea>
      <div class="form-hint">
        定义 AI 的角色和行为规范。留空使用内置默认。
        <HButton v-if="usingBuiltinSystemPrompt && builtinDefaultPreset" variant="ghost" size="xs" @click="defaultPreviewVisible = true">查看内置默认</HButton>
      </div>

      <!-- 输出格式要求 -->
      <HTextarea v-model="userFormat" :rows="10" placeholder="留空使用内置默认输出格式">
        <template #label>输出格式要求</template>
      </HTextarea>
      <div class="form-hint">
        定义回顾文档的章节结构。留空使用内置默认。
        <HButton v-if="usingBuiltinUserFormat && builtinDefaultPreset" variant="ghost" size="xs" @click="defaultPreviewVisible = true">查看内置默认</HButton>
      </div>

      <!-- 粉丝称呼 -->
      <HInput v-model="fanName" placeholder="如：小橘子、绿冻">
        <template #label>粉丝称呼</template>
      </HInput>
      <div class="form-hint">用于模板变量 <code v-pre>{{fan_name}}</code>，可个性化回顾结尾。</div>

      <!-- 自定义变量 -->
      <HTextarea v-model="extraVars" :rows="3" placeholder='{"key": "value"}'>
        <template #label>自定义变量 (JSON)</template>
      </HTextarea>

      <!-- 操作按钮 -->
      <div class="form-actions">
        <HButton variant="primary" @click="save" :loading="saving">保存</HButton>
        <HButton variant="secondary" @click="resetToDefault">重置为内置默认</HButton>
        <HButton v-if="scope === 'channel'" variant="danger" @click="removeChannelTemplate">删除主播模板</HButton>
      </div>
    </template>

    <!-- Channel 模式未开启自定义: 显示全局模板预览 -->
    <template v-if="scope === 'channel' && !useCustom && resolvedTemplate">
      <HDescriptions
        :column="1"
        :items="[
          { label: 'System Prompt', value: resolvedTemplate.system_prompt },
          { label: '输出格式', value: resolvedTemplate.user_format },
          { label: '粉丝称呼', value: resolvedTemplate.fan_name || '(未设置)' },
        ]"
      />
      <div class="descriptions-title">当前生效模板（来自全局）</div>
    </template>

    <HDialog v-model:visible="defaultPreviewVisible" title="内置默认模板" width="780px">
      <template v-if="builtinDefaultPreset">
        <h4>System Prompt</h4>
        <pre class="preset-preview">{{ builtinDefaultPreset.system_prompt }}</pre>
        <h4>输出格式要求</h4>
        <pre class="preset-preview">{{ builtinDefaultPreset.user_format }}</pre>
      </template>
    </HDialog>
  </div>
</template>

<style scoped>
.form-hint {
  font-size: 12px;
  color: var(--text-secondary);
  margin: 4px 0 12px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.template-toolbar {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.preset-select {
  width: 260px;
  margin-right: auto;
}

.upload-btn {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  position: relative;
}

.upload-btn input[type="file"] {
  position: absolute;
  width: 0;
  height: 0;
  opacity: 0;
  pointer-events: none;
}

.var-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.var-table th {
  padding: 6px 8px;
  text-align: left;
  font-weight: 600;
  font-size: 11.5px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}

.var-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--border-light);
  color: var(--text);
}

.var-table code {
  font-family: var(--font-mono, monospace);
  background: var(--surface);
  padding: 1px 5px;
  border-radius: 3px;
  font-size: 12px;
}

.custom-toggle {
  margin: 16px 0;
}

.form-actions {
  display: flex;
  gap: 8px;
  margin-top: 16px;
}

.descriptions-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  margin: 8px 0 12px;
}

.preset-preview {
  max-height: 260px;
  overflow: auto;
  white-space: pre-wrap;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: 10px;
  font-size: 12px;
  background: var(--surface);
}
</style>
