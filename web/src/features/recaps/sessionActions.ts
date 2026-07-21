/**
 * 回顾页 UI 动作服务(重构方案 §4 状态机收敛)
 *
 * 这里把 RecapsView 两套 UI 入口的动作逻辑显式化:
 *  - 列表行(表A): recap_done→阅读(published→无动作或取回; failed→retry; 其余→主动作; local 不可用→独立取回)
 *  - 抽屉(表B): 仅主动作(recap_done→upload; uploaded→publish; published/failed 无动作)
 *    重新生成回顾是抽屉内的硬编码按钮(非主动作,不走状态机推进),见 RecapDrawer.vue。
 *
 * 关键:不把 utils/lifecycle.ts 当唯一真相——它给 published 返回 fetch,
 * 列表行让 fetch 透出为取回按钮(若 local 不可用),抽屉不显示。本服务复刻的是「UI 入口语义」,
 * lifecycle 只作为主动作能力判断的底层调用之一。
 *
 * 注意:这里的 UIActionName 不复用 lifecycle 的 SessionActionName——后者只含
 * stop_record/submit_asr/generate_recap/upload/fetch/publish 6 个,而 UI 层还有 retry。
 * 本类型只覆盖回顾页(列表行+抽屉)的「状态推进型」动作,不含 stop_record
 * (它属首页直播卡,不进回顾页两入口),故也不是 lifecycle 的超集。
 * 「重新生成回顾」属非推进型动作,不进 UIActionName,在 RecapDrawer 内硬编码。
 */
import type { Session, Task, Capabilities } from '@/api/types-derived'
import { getNextAction } from '@/utils/lifecycle'

/** 回放类来源(回放下载 / 手动导入)的回顾不发布B站,用于隐藏 publish/edit/remove 动作 */
function isReplaySource(s: Session): boolean {
  return s.source_type === 'download' || s.source_type === 'import'
}

/** 回顾页 UI 动作标识(覆盖列表行+抽屉全部动作) */
export type UIActionName =
  | 'submit_asr'
  | 'generate_recap'
  | 'upload'
  | 'publish' // 主动作(经 lifecycle)
  | 'fetch' // 取回(local_available)
  | 'retry' // 重试(failed,基于 current_task_id)
  | 'reset' // 重置到 media_ready(仅 ASR 失败场次,修复 2026-07-20 BUG #2)

/** UI 动作禁用文案(独立于 lifecycle.ACTION_DISABLED_REASON,后者只覆盖 6 个 lifecycle 动作) */
export const UI_ACTION_REASON: Record<UIActionName, string> = {
  submit_asr: 'ASR 能力不可用，请检查 DashScope API Key 与 ASR 配置',
  generate_recap: '回顾生成能力不可用，请检查 AI 回顾配置',
  upload: 'WebDAV 上传能力不可用，请检查 WebDAV 配置',
  publish: '发布能力不可用，请检查发布配置与 Cookie',
  fetch: '', // 取回无能力门槛
  retry: '无可重试任务',
  reset: '', // reset 无能力门槛,由 isASRFailure + local_available 控制
}

export interface SessionAction {
  name: UIActionName
  label: string
  disabled: boolean
  disabledReason: string
  confirmText: string
}

/** 主动作名(submit_asr/generate_recap/upload/publish)。用于收窄 runAction/executeAction 入参,
 *  避免误传 retry/edit_opus/remove_opus/fetch 后在 executeAction 静默 no-op。 */
export type PrimaryActionName = 'submit_asr' | 'generate_recap' | 'upload' | 'publish'

/** 主动作(name 收窄为 PrimaryActionName) */
export interface PrimaryAction extends SessionAction {
  name: PrimaryActionName
}

/** 列表行(表A)的动作集合 */
export interface RowActions {
  /** recap_done 时为 true,列表渲染「阅读回顾」(非动作,打开抽屉) */
  read?: boolean
  /** 主动作(media_ready→submit_asr / asr_done→generate_recap / uploaded→publish) */
  primary?: PrimaryAction
  /** failed 且有可重试任务时,重试 */
  retry?: SessionAction
  /** failed 且为 ASR 失败 + local_available=true 时,重置到 media_ready(修复 2026-07-20 BUG #2) */
  reset?: SessionAction
  /** local_available=false 时,独立取回(与其它动作并存,非互斥) */
  fetch?: SessionAction
}

/** 抽屉(表B)的动作集合 */
export interface DrawerActions {
  /** 主动作(recap_done→upload; uploaded→publish);published/failed/recap_done(阅读)无 */
  primary?: PrimaryAction
}

// ---------- 主动作构建(复用 lifecycle 的状态→动作映射与能力判断) ----------

/** 主动作名(lifecycle 给出的非 fetch 动作) */
const PRIMARY_ACTIONS: PrimaryActionName[] = ['submit_asr', 'generate_recap', 'upload', 'publish']

function isPrimaryActionName(name: string): name is PrimaryActionName {
  return (PRIMARY_ACTIONS as string[]).includes(name)
}

/**
 * 构建主动作。lifecycle 的 getNextAction 已含 status→action 映射 + 能力守卫 + 本地清理守卫,
 * 这里只做 UI 层的适配:把 NextAction 转成 SessionAction,并过滤掉 fetch(它不是「主动作」,
 * 在列表行作为独立的取回按钮)。
 */
function buildPrimaryAction(
  session: Session,
  capabilities: Capabilities | null,
): PrimaryAction | undefined {
  const next = getNextAction(session.status, capabilities, session.local_available)
  if (!next || !isPrimaryActionName(next.name)) return undefined
  // 回放类(回放下载/导入)不发布B站:uploaded 状态的 publish 主动作对回放类隐藏。
  // (归档 upload 不受影响;published 状态本就返回 fetch,已被 isPrimaryActionName 过滤掉。)
  if (next.name === 'publish' && isReplaySource(session)) return undefined
  return {
    name: next.name,
    label: next.label,
    disabled: next.disabled,
    disabledReason: next.disabledReason,
    confirmText: next.confirmText,
  }
}

// ---------- retry(吸收 codex 阶段1 建议:文案细化) ----------

export type RetryDecision = 'retryable' | 'no_task_id' | 'task_missing' | 'task_not_failed'

/**
 * retry 决策(§7.1)。currentTask 由调用方按 session.current_task_id 从 tasksStore 查得后传入。
 * 区分四种情况,供 UI 给不同文案(吸收 codex 建议:不再一律「无可重试任务」):
 *  - retryable: current_task_id 存在 + 任务为 failed → 显示重试按钮
 *  - no_task_id: 无 current_task_id
 *  - task_missing: 有 id 但任务不在 store(可能已过期清理)
 *  - task_not_failed: 任务在但状态非 failed(已成功/取消/运行中)
 */
export function decideRetry(session: Session, currentTask: Task | null | undefined): RetryDecision {
  if (!session.current_task_id) return 'no_task_id'
  if (!currentTask) return 'task_missing'
  if (currentTask.status !== 'failed') return 'task_not_failed'
  return 'retryable'
}

/** retry 可重试判断(列表行模板 v-if 用) */
export function isRetryable(session: Session, currentTask: Task | null | undefined): boolean {
  return decideRetry(session, currentTask) === 'retryable'
}

/**
 * 判断 failed session 是否由 ASR 任务失败引起(用于决定是否显示 reset 按钮)。
 * 修复 2026-07-20 BUG #2:状态机约束 media_ready 后只有 ASR 能跑,其他任务类型 reset 后无法走通。
 * 复用 currentTask(已由调用方从 tasksStore 查得);若 task 不在 store,保守返回 false。
 */
export function isASRFailure(session: Session, currentTask: Task | null | undefined): boolean {
  if (!session.current_task_id || !currentTask) return false
  return currentTask.type === 'asr' && currentTask.status === 'failed'
}

/** 构造 reset 动作(列表行 failed 分支使用) */
function buildResetAction(): SessionAction {
  return {
    name: 'reset',
    label: '重置',
    disabled: false,
    disabledReason: '',
    confirmText: '', // 确认文案统一在 RecapsView.handleReset 用 HConfirm
  }
}

/** failed 不可重试时的占位文案(列表行模板用,细化自 codex 建议) */
export function retryHint(decision: RetryDecision): string {
  switch (decision) {
    case 'no_task_id':
      return '无任务可重试'
    case 'task_missing':
      return '任务已过期'
    case 'task_not_failed':
      return '' // 任务已成功/取消,失败行本不应出现此任务,直接隐藏占位
    case 'retryable':
      return ''
  }
}

function buildRetryAction(): SessionAction {
  return {
    name: 'retry',
    label: '重试',
    disabled: false,
    disabledReason: '',
    confirmText: '确定重试该失败任务？',
  }
}

// ---------- fetch(取回,local_available=false 独立按钮) ----------

/** local_available=false 时提供取回入口(列表行独立按钮,无能力门槛) */
export function canFetchLocal(session: Session): boolean {
  return session.local_available === false
}

function buildFetchAction(): SessionAction {
  return {
    name: 'fetch',
    label: '取回',
    disabled: false,
    disabledReason: '',
    confirmText: '确定要从归档取回本场文件？',
  }
}

// ---------- 两套入口 ----------

/**
 * 列表行(表A)动作。复刻 RecapsView session-right 的 UI 语义(优先级见模块注释):
 *  - recap_done → read=true(阅读回顾,非动作)
 *  - published && publish_target → edit + remove
 *  - failed && retryable → retry
 *  - 其余主动作(media_ready→submit_asr / asr_done→generate_recap / uploaded→publish) → primary
 *  - local_available=false → fetch(独立并存,非互斥)
 *
 * retry 决策依赖 currentTask 状态,故需传入;其它动作只用 session+capabilities。
 */
export function getRowActions(
  session: Session,
  capabilities: Capabilities | null,
  currentTask?: Task | null,
): RowActions {
  const actions: RowActions = {}

  // recap_done:列表行只显示「阅读回顾」(打开抽屉),不显示 upload(与抽屉入口的差异点①)
  if (session.status === 'recap_done') {
    actions.read = true
    return withFetch(actions, session)
  }

  // failed:retry(可重试时)+ reset(仅 ASR 失败 + local_available=true)
  // 修复 2026-07-20 BUG #2:reset 允许用户从 media_ready 重新提交 ASR
  if (session.status === 'failed') {
    if (isRetryable(session, currentTask)) {
      actions.retry = buildRetryAction()
    }
    // reset 仅在 ASR 失败 + 本地产物可用时显示:
    //  - ASR 失败:状态机 media_ready 后只有 ASR 能跑,其他任务 reset 后无法走通
    //  - local_available:本地产物已清理时 reset 无意义(用 fetch 取回)
    if (session.local_available && isASRFailure(session, currentTask)) {
      actions.reset = buildResetAction()
    }
    return withFetch(actions, session)
  }

  // 主动作(media_ready→submit_asr / asr_done→generate_recap / uploaded→publish)
  // 注意:recap_done→upload 是主动作,但前面 recap_done 分支已拦截成 read,故这里不会到。
  // published 走到这里时 lifecycle 返回 fetch(被 isPrimaryActionName 过滤),列表行仅显示取回(若 local 不可用)；
  // 「重新生成回顾」入口在抽屉内(RecapDrawer),不进列表行——published 专栏只能手动去 B站管理。
  const primary = buildPrimaryAction(session, capabilities)
  if (primary) actions.primary = primary

  return withFetch(actions, session)
}

/**
 * 抽屉(表B)动作。复刻 RecapsView drawer-actions 的 UI 语义:
 *  - 仅主动作,且 recap_done→upload(差异点①:列表行 recap_done 是阅读,抽屉是 upload)
 *  - published 不显示任何动作(差异点②)
 *  - failed 不显示 retry,不显示 fetch
 */
export function getDrawerActions(session: Session, capabilities: Capabilities | null): DrawerActions {
  const primary = buildPrimaryAction(session, capabilities)
  return primary ? { primary } : {}
}

/** local_available=false 时给列表行附加独立取回按钮(与其它动作并存) */
function withFetch(actions: RowActions, session: Session): RowActions {
  if (canFetchLocal(session)) {
    actions.fetch = buildFetchAction()
  }
  return actions
}

// ---------- 供 RecapsView 模板用的渲染辅助类型映射(主动作按钮配色) ----------

/** 主动作按钮配色(与 RecapsView.actionType 一致) */
export function primaryActionType(name: UIActionName): 'primary' | 'success' | 'warning' {
  if (name === 'upload') return 'success'
  if (name === 'publish') return 'warning'
  return 'primary'
}
