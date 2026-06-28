<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { Download, Upload } from '@element-plus/icons-vue'
import { useRecapTemplateEditor } from '@/features/channel/useRecapTemplateEditor'
import type { UploadFile } from 'element-plus'

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

// 变量参考(纯展示常量)
const templateVariables = [
  { name: '{{channel_name}}', desc: '主播名称' },
  { name: '{{channel_id}}', desc: '主播 ID' },
  { name: '{{date}}', desc: '直播日期 (YYYY.MM.DD)' },
  { name: '{{title}}', desc: '直播标题' },
  { name: '{{duration}}', desc: '时长 (如 2小时30分钟)' },
  { name: '<code v-pre>{{fan_name}}</code>', desc: '粉丝称呼' },
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

// 导入:读 file.raw.text 后交给 composable
async function handleImportFile(file: UploadFile) {
  const raw = file.raw
  if (!raw) return
  const content = await raw.text()
  await importTemplateFile(content)
}

onMounted(async () => {
  await Promise.all([loadData(), loadPresets()])
})
watch(() => props.channelId, loadData)
</script>

<template>
  <div v-loading="loading">
    <div class="template-toolbar">
      <el-select
        v-model="selectedPresetName"
        filterable
        clearable
        placeholder="选择模板预设"
        :loading="presetLoading"
        class="preset-select"
        @change="handleApplyPreset"
      >
        <el-option
          v-for="preset in presets"
          :key="preset.name"
          :label="preset.name"
          :value="preset.name"
        >
          <div class="preset-option">
            <span>{{ preset.name }}</span>
            <small>{{ preset.description }}</small>
          </div>
        </el-option>
      </el-select>
      <el-button type="success" plain @click="exportTemplate">
        <el-icon><Download /></el-icon>
        导出模板
      </el-button>
      <el-upload
        :auto-upload="false"
        :show-file-list="false"
        accept=".json,application/json"
        :on-change="handleImportFile"
      >
        <el-button :loading="importing">
          <el-icon><Upload /></el-icon>
          导入模板
        </el-button>
      </el-upload>
    </div>

    <!-- 变量参考折叠面板 -->
    <el-collapse>
      <el-collapse-item title="模板变量参考">
        <el-table :data="templateVariables" size="small" border>
          <el-table-column prop="name" label="变量" width="200" />
          <el-table-column prop="desc" label="说明" />
        </el-table>
      </el-collapse-item>
    </el-collapse>

    <!-- Channel 模式: 使用自定义模板开关 -->
    <div v-if="scope === 'channel'" style="margin: 16px 0">
      <el-switch v-model="useCustom" active-text="使用自定义模板" inactive-text="跟随全局模板" @change="(val: boolean) => { if (!val) removeChannelTemplate() }" />
    </div>

    <!-- 全局模式 或 channel 模式开启自定义 -->
    <template v-if="scope === 'global' || useCustom">
      <!-- System Prompt -->
      <el-form label-position="top">
        <el-form-item label="System Prompt">
          <el-input v-model="systemPrompt" type="textarea" :rows="10" placeholder="留空使用内置默认 System Prompt" />
          <div class="form-hint">
            定义 AI 的角色和行为规范。留空使用内置默认。
            <el-button v-if="usingBuiltinSystemPrompt && builtinDefaultPreset" type="primary" link @click="defaultPreviewVisible = true">查看内置默认</el-button>
          </div>
        </el-form-item>

        <!-- 输出格式要求 -->
        <el-form-item label="输出格式要求">
          <el-input v-model="userFormat" type="textarea" :rows="10" placeholder="留空使用内置默认输出格式" />
          <div class="form-hint">
            定义回顾文档的章节结构。留空使用内置默认。
            <el-button v-if="usingBuiltinUserFormat && builtinDefaultPreset" type="primary" link @click="defaultPreviewVisible = true">查看内置默认</el-button>
          </div>
        </el-form-item>

        <!-- 粉丝称呼 -->
        <el-form-item label="粉丝称呼">
          <el-input v-model="fanName" placeholder="如：小橘子、绿冻" />
          <div class="form-hint">用于模板变量 <code v-pre>{{fan_name}}</code>，可个性化回顾结尾。</div>
        </el-form-item>

        <!-- 自定义变量 -->
        <el-form-item label="自定义变量 (JSON)">
          <el-input v-model="extraVars" type="textarea" :rows="3" placeholder='{"key": "value"}' />
        </el-form-item>

        <!-- 操作按钮 -->
        <el-form-item>
          <el-button type="primary" @click="save" :loading="saving">保存</el-button>
          <el-button @click="resetToDefault">重置为内置默认</el-button>
          <el-button v-if="scope === 'channel'" type="danger" @click="removeChannelTemplate">删除主播模板</el-button>
        </el-form-item>
      </el-form>
    </template>

    <!-- Channel 模式未开启自定义: 显示全局模板预览 -->
    <template v-if="scope === 'channel' && !useCustom && resolvedTemplate">
      <el-descriptions title="当前生效模板（来自全局）" :column="1" border>
        <el-descriptions-item label="System Prompt">
          <pre style="max-height: 200px; overflow: auto; white-space: pre-wrap">{{ resolvedTemplate.system_prompt }}</pre>
        </el-descriptions-item>
        <el-descriptions-item label="输出格式">
          <pre style="max-height: 200px; overflow: auto; white-space: pre-wrap">{{ resolvedTemplate.user_format }}</pre>
        </el-descriptions-item>
        <el-descriptions-item label="粉丝称呼">{{ resolvedTemplate.fan_name || '(未设置)' }}</el-descriptions-item>
      </el-descriptions>
    </template>

    <el-dialog v-model="defaultPreviewVisible" title="内置默认模板" width="780px">
      <template v-if="builtinDefaultPreset">
        <h4>System Prompt</h4>
        <pre class="preset-preview">{{ builtinDefaultPreset.system_prompt }}</pre>
        <h4>输出格式要求</h4>
        <pre class="preset-preview">{{ builtinDefaultPreset.user_format }}</pre>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
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

.preset-option {
  display: flex;
  flex-direction: column;
  line-height: 1.4;
}

.preset-option small {
  color: var(--el-text-color-secondary);
}

.form-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 4px;
}

.preset-preview {
  max-height: 260px;
  overflow: auto;
  white-space: pre-wrap;
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  padding: 10px;
}
</style>
