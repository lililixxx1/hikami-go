<!--
  HCombobox.vue — 可输入的组合框（input + 原生 datalist）。
  既能从 options 快捷选择，又能自由输入任意文本（替代回顾模型处原 HSelect 无法手输的痛点）。
  - 渐进增强：现代浏览器渲染下拉建议；旧浏览器/读屏不支持 datalist 时自动降级为普通 input，输入功能不受损。
  - clearable：有值时显示清空按钮，点击 emit 空串（用于主播级"留空跟随全局"的清空回路）。
-->
<script setup lang="ts">
import { computed, useId } from 'vue'

const props = withDefaults(defineProps<{
  modelValue?: string
  options: { label: string; value: string }[]
  placeholder?: string
  disabled?: boolean
  size?: 'sm' | 'md'
  /** 有值时显示清空按钮，点击 emit 空串 */
  clearable?: boolean
}>(), { modelValue: '', placeholder: '', size: 'md', clearable: false })

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

// datalist 的 id 需页面唯一，useId 保证同页多个 HCombobox 不冲突
const listId = useId()

const inputClasses = computed(() => [
  'input',
  'combobox-input',
  ...(props.size === 'sm' ? ['input-sm'] : []),
])

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLInputElement).value)
}
function clear() {
  emit('update:modelValue', '')
}
</script>

<template>
  <label class="form-field combobox-field">
    <span v-if="$slots.label" class="form-label"><slot name="label" /></span>
    <div class="combobox-control">
      <input
        :class="inputClasses"
        :value="modelValue"
        :placeholder="placeholder"
        :disabled="disabled"
        :list="listId"
        autocomplete="off"
        @input="onInput"
      >
      <button
        v-if="clearable && modelValue && !disabled"
        type="button"
        class="combobox-clear"
        title="清空"
        @click="clear"
      >×</button>
      <datalist :id="listId">
        <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
      </datalist>
    </div>
  </label>
</template>

<style scoped>
.combobox-field { position: relative; }
.combobox-control { position: relative; width: 100%; }
.combobox-control .input { width: 100%; }
.combobox-input { padding-right: 30px; }
.combobox-clear {
  position: absolute; right: 6px; top: 50%; transform: translateY(-50%);
  border: none; background: transparent; cursor: pointer;
  font-size: 16px; line-height: 1; color: var(--text-muted);
  padding: 2px 4px; border-radius: var(--radius-sm);
}
.combobox-clear:hover { color: var(--text); background: var(--surface); }
</style>
