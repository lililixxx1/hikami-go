// web/src/components/ui/HConfirm.ts
// 轻量确认/提示/输入对话框(替代 element-plus ElMessageBox.confirm/alert/prompt)。
// 用法:
//   - HConfirm(message, opts): Promise<boolean>  true=确认 false=取消
//     替代 ElMessageBox.confirm(msg, title, {...}).then(confirm).catch(cancel)
//   - HAlert(message, opts): Promise<void>  单按钮「知道了」
//     替代 ElMessageBox.alert(msg, title, {...})
//   - HPrompt(message, opts): Promise<string | null>  返回输入值,取消返回 null
//     替代 ElMessageBox.prompt(msg, title, {...})
// ConfirmHost.vue 读取本模块响应式 state 渲染 HDialog;App.vue 挂载一次即全局可用。

import { ref } from 'vue'

export type ConfirmType = 'info' | 'warning' | 'danger'

export interface ConfirmOptions {
  title?: string
  confirmText?: string
  cancelText?: string
  type?: ConfirmType // 影响确认按钮颜色:danger→danger 变体
}

export interface PromptOptions extends ConfirmOptions {
  placeholder?: string
  inputType?: 'text' | 'password'
  defaultValue?: string
}

// ── 确认/提示对话框共享状态 ──
type DialogKind = 'confirm' | 'alert' | 'prompt'

interface ConfirmState {
  visible: boolean
  kind: DialogKind
  message: string
  options: ConfirmOptions
}

export const confirmState = ref<ConfirmState>({
  visible: false,
  kind: 'confirm',
  message: '',
  options: {},
})

// ── prompt 专用状态(确认/提示复用 confirmState,prompt 额外需要 input) ──
export const promptValue = ref('')
export const promptOptions = ref<PromptOptions>({})

// 当前待 resolve 的回调(任一时刻最多一个对话框)
type Resolver<T> = (v: T) => void
let confirmResolver: Resolver<boolean> | null = null
let promptResolver: Resolver<string | null> | null = null

function dismissConfirm(value: boolean): void {
  confirmState.value = { ...confirmState.value, visible: false }
  const r = confirmResolver
  confirmResolver = null
  if (r) r(value)
}

/**
 * 确认对话框。返回 true=用户确认,false=用户取消。
 * 替代 ElMessageBox.confirm(msg, title, opts).then(()=>确认逻辑) —— 直接 `if (await HConfirm(...)) { ... }`。
 */
export function HConfirm(message: string, opts: ConfirmOptions = {}): Promise<boolean> {
  // 若有待处理的确认,先按「取消」解决(避免挂起;与 EP 单例语义一致)
  if (confirmResolver) dismissConfirm(false)
  if (promptResolver) dismissPrompt(null)
  return new Promise<boolean>((resolve) => {
    confirmResolver = resolve
    confirmState.value = {
      visible: true,
      kind: 'confirm',
      message,
      options: opts,
    }
  })
}

/** 提示对话框(单按钮)。替代 ElMessageBox.alert。 */
export function HAlert(message: string, opts: ConfirmOptions = {}): Promise<void> {
  return HConfirm(message, { confirmText: opts.confirmText ?? '知道了', cancelText: opts.cancelText ?? '', ...opts }).then(
    () => undefined,
  )
}

/** 输入对话框。返回输入值(取消返回 null)。替代 ElMessageBox.prompt。 */
export function HPrompt(message: string, opts: PromptOptions = {}): Promise<string | null> {
  if (confirmResolver) dismissConfirm(false)
  if (promptResolver) dismissPrompt(null)
  promptValue.value = opts.defaultValue ?? ''
  promptOptions.value = opts
  return new Promise<string | null>((resolve) => {
    promptResolver = resolve
    confirmState.value = {
      visible: true,
      kind: 'prompt',
      message,
      options: opts,
    }
  })
}

// ── ConfirmHost.vue 调用的解决函数 ──

/** ConfirmHost 在用户点击「确认」/「取消」/关闭时调用 */
export function resolveConfirm(value: boolean): void {
  // prompt 模式下走 prompt 解决路径
  if (promptResolver) {
    dismissPrompt(value ? promptValue.value.trim() : null)
    return
  }
  dismissConfirm(value)
}

// 同步更新 prompt 输入值(ConfirmHost v-model 绑定)
export function setPromptValue(v: string): void {
  promptValue.value = v
}

function dismissPrompt(value: string | null): void {
  confirmState.value = { ...confirmState.value, visible: false }
  const r = promptResolver
  promptResolver = null
  if (r) r(value)
}
