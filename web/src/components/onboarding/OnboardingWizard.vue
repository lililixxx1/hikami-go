<script setup lang="ts">
import { onMounted } from 'vue'
import { useOnboardingWizard } from '@/features/onboarding/useOnboardingWizard'
import { HCard, HButton, HInput, HPill } from '@/components/ui'

// 业务逻辑全在 composable(step 状态机 + 三步 API);组件纯展示
const {
  needed,
  loading,
  step,
  runtimeData,
  dashScopeKey,
  aiKey,
  channelInput,
  init,
  prevStep,
  nextStep,
  finish,
  dismiss,
} = useOnboardingWizard()

const steps = ['环境检查', '配置密钥', '添加主播', '开始使用']

onMounted(init)
</script>

<template>
  <div v-if="needed" class="onboarding-overlay">
    <HCard class="wizard-card">
      <template #header>
        <div class="wizard-header">
          <h2>欢迎使用 Hikami-Go</h2>
          <p>按照以下步骤完成初始配置</p>
        </div>
      </template>

      <div class="wizard-body">
        <!-- 简易步骤指示器(替代 el-steps) -->
        <div class="wizard-steps">
          <div
            v-for="(label, idx) in steps"
            :key="idx"
            class="step-item"
            :class="{ active: idx === step, done: idx < step }"
          >
            <span class="step-dot">{{ idx + 1 }}</span>
            <span class="step-label">{{ label }}</span>
          </div>
        </div>

        <div class="step-content">
          <div v-if="step === 0">
            <h3>环境检查</h3>
            <div class="check-list">
              <div v-for="(tool, name) in runtimeData?.tools" :key="name" class="check-item">
                <HPill :variant="tool.available ? 'success' : (tool.required ? 'danger' : 'neutral')">
                  {{ tool.available ? 'OK' : '缺失' }}
                </HPill>
                <span class="check-name">{{ tool.name }}</span>
                <span v-if="tool.required" class="check-required">必需</span>
                <span v-if="!tool.available && tool.install_hint" class="check-hint">{{ tool.install_hint }}</span>
              </div>
            </div>
          </div>

          <div v-if="step === 1">
            <h3>API 密钥配置</h3>
            <div class="form-stack">
              <div class="field">
                <label class="field-label">DashScope ASR 密钥</label>
                <HInput v-model="dashScopeKey" placeholder="sk-..." />
                <div class="field-hint">用于语音转写（阿里云 DashScope）</div>
              </div>
              <div class="field">
                <label class="field-label">AI 回顾生成密钥</label>
                <HInput v-model="aiKey" placeholder="sk-..." />
                <div class="field-hint">用于 AI 生成直播回顾</div>
              </div>
            </div>
          </div>

          <div v-if="step === 2">
            <h3>添加第一个主播</h3>
            <div class="field">
              <label class="field-label">主播 ID 或 URL</label>
              <HInput v-model="channelInput" placeholder="输入 B 站 UID、直播间 URL 或主页 URL" />
              <div class="field-hint">支持格式：UID 数字、直播间接链接、空间主页链接</div>
            </div>
          </div>

          <div v-if="step === 3">
            <h3>配置完成！</h3>
            <p>你已准备就绪。现在可以开始录制直播、生成回顾了。</p>
          </div>
        </div>

        <div class="wizard-actions">
          <HButton v-if="step > 0" variant="secondary" @click="prevStep">上一步</HButton>
          <HButton v-if="step < 3" variant="primary" :loading="loading" @click="nextStep">下一步</HButton>
          <HButton v-if="step === 3" variant="primary" @click="finish">开始使用</HButton>
          <HButton variant="ghost" @click="dismiss">跳过引导</HButton>
        </div>
      </div>
    </HCard>
  </div>
</template>

<style scoped>
.onboarding-overlay {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 9999;
}
.wizard-card {
  width: 560px;
  max-height: 90vh;
  overflow-y: auto;
}
.wizard-header {
  text-align: center;
}
.wizard-header h2 {
  margin: 0 0 4px;
}
.wizard-header p {
  color: var(--text-secondary);
  margin: 0;
}
.wizard-body {
  padding: 16px 0;
}
.wizard-steps {
  display: flex;
  gap: 4px;
  margin-bottom: 20px;
}
.step-item {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border-bottom: 2px solid var(--border-light);
  font-size: 12px;
  color: var(--text-muted);
}
.step-item.active {
  color: var(--accent);
  border-bottom-color: var(--accent);
  font-weight: 500;
}
.step-item.done {
  color: var(--success);
  border-bottom-color: var(--success);
}
.step-dot {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--surface);
  border: 1px solid var(--border);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  flex-shrink: 0;
}
.step-item.active .step-dot {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.step-item.done .step-dot {
  background: var(--success);
  border-color: var(--success);
  color: #fff;
}
.step-content {
  min-height: 200px;
  padding: 8px 0;
}
.step-content h3 {
  margin: 0 0 12px;
}
.form-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.field-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
  min-width: 120px;
}
.field-hint {
  color: var(--text-muted);
  font-size: 12px;
  margin-top: 2px;
}
.check-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.check-item {
  display: flex;
  align-items: center;
  gap: 8px;
}
.check-name {
  font-weight: 500;
  min-width: 60px;
}
.check-required {
  color: var(--danger, #f56c6c);
  font-size: 12px;
}
.check-hint {
  color: var(--text-muted);
  font-size: 12px;
}
.wizard-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 16px;
  border-top: 1px solid var(--border-light);
}
</style>
