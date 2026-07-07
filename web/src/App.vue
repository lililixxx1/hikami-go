<script setup lang="ts">
import AppLayout from './components/layout/AppLayout.vue'
import HToast from './components/ui/HToast.vue'
import ConfirmHost from './components/ui/ConfirmHost.vue'
</script>

<template>
  <AppLayout />
  <!-- 全局 toast + 确认/提示/输入对话框宿主(替代 ElMessage/ElMessageBox) -->
  <HToast />
  <ConfirmHost />
</template>
