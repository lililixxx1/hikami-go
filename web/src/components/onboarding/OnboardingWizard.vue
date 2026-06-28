<script setup lang="ts">
import { onMounted } from 'vue'
import { useOnboardingWizard } from '@/features/onboarding/useOnboardingWizard'

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

onMounted(init)
</script>

<template>
  <div v-if="needed" class="onboarding-overlay">
    <el-card class="wizard-card" shadow="always">
      <template #header>
        <div class="wizard-header">
          <h2>欢迎使用 Hikami-Go</h2>
          <p>按照以下步骤完成初始配置</p>
        </div>
      </template>

      <el-steps :active="step" finish-status="success" simple class="wizard-steps">
        <el-step title="环境检查" />
        <el-step title="配置密钥" />
        <el-step title="添加主播" />
        <el-step title="开始使用" />
      </el-steps>

      <div class="step-content">
        <div v-if="step === 0">
          <h3>环境检查</h3>
          <div class="check-list">
            <div v-for="(tool, name) in runtimeData?.tools" :key="name" class="check-item">
              <el-tag :type="tool.available ? 'success' : (tool.required ? 'danger' : 'info')" size="small">
                {{ tool.available ? 'OK' : '缺失' }}
              </el-tag>
              <span class="check-name">{{ tool.name }}</span>
              <span v-if="tool.required" class="check-required">必需</span>
              <span v-if="!tool.available && tool.install_hint" class="check-hint">{{ tool.install_hint }}</span>
            </div>
          </div>
        </div>

        <div v-if="step === 1">
          <h3>API 密钥配置</h3>
          <el-form label-width="160px">
            <el-form-item label="DashScope ASR 密钥">
              <el-input v-model="dashScopeKey" placeholder="sk-..." show-password />
              <div class="field-hint">用于语音转写（阿里云 DashScope）</div>
            </el-form-item>
            <el-form-item label="AI 回顾生成密钥">
              <el-input v-model="aiKey" placeholder="sk-..." show-password />
              <div class="field-hint">用于 AI 生成直播回顾</div>
            </el-form-item>
          </el-form>
        </div>

        <div v-if="step === 2">
          <h3>添加第一个主播</h3>
          <el-form label-width="120px">
            <el-form-item label="主播 ID 或 URL">
              <el-input v-model="channelInput" placeholder="输入 B 站 UID、直播间 URL 或主页 URL" />
              <div class="field-hint">支持格式：UID 数字、直播间接链接、空间主页链接</div>
            </el-form-item>
          </el-form>
        </div>

        <div v-if="step === 3">
          <h3>配置完成！</h3>
          <p>你已准备就绪。现在可以开始录制直播、生成回顾了。</p>
        </div>
      </div>

      <div class="wizard-actions">
        <el-button v-if="step > 0" @click="prevStep">上一步</el-button>
        <el-button v-if="step < 3" type="primary" @click="nextStep" :loading="loading">下一步</el-button>
        <el-button v-if="step === 3" type="success" @click="finish">开始使用</el-button>
        <el-button @click="dismiss" text>跳过引导</el-button>
      </div>
    </el-card>
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
  color: #909399;
  margin: 0;
}
.wizard-steps {
  margin: 16px 0;
}
.step-content {
  min-height: 200px;
  padding: 16px 0;
}
.step-content h3 {
  margin: 0 0 12px;
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
  color: #f56c6c;
  font-size: 12px;
}
.check-hint {
  color: #909399;
  font-size: 12px;
}
.field-hint {
  color: #909399;
  font-size: 12px;
  margin-top: 4px;
}
.wizard-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 16px;
  border-top: 1px solid #ebeef5;
}
</style>
