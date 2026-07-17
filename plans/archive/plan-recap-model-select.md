# 修复计划:回顾 AI 模型支持手动输入 + 精简预设

> 分支:`fix/recap-model-manual-input`
> 创建日期:2026-07-15

## 一、问题背景(用户反馈)

用户反馈设置页面"回顾 AI"存在两个问题:

1. **不能自己填模型**:当前模型选择是原生 `<select>` 下拉框,**只能从预设里选、无法手动输入**模型名称。
2. **预设太多**:当前后端返回 8 个预设(DeepSeek 2 + OpenAI 2 + 其他 4),用户希望精简到**只保留 DeepSeek 的 2 个**。

## 二、现状调查(已核实)

### 2.1 后端:硬编码 8 个预设

`internal/handler/server.go:2762-2771` —— `recommendedRecapModels` 全局变量,返回给前端:

```go
var recommendedRecapModels = []RecapModelOption{
    {Value: "deepseek-v4-flash", Label: "deepseek-v4-flash（快速）", Group: "DeepSeek"},
    {Value: "deepseek-v4-pro", Label: "deepseek-v4-pro（默认）", Group: "DeepSeek"},
    {Value: "gpt-4o", Label: "gpt-4o", Group: "OpenAI"},          // ← 删
    {Value: "gpt-4o-mini", Label: "gpt-4o-mini", Group: "OpenAI"}, // ← 删
    {Value: "qwen-plus", Label: "qwen-plus", Group: "其他"},       // ← 删
    {Value: "qwen-turbo", Label: "qwen-turbo", Group: "其他"},     // ← 删
    {Value: "qwen-max", Label: "qwen-max", Group: "其他"},         // ← 删
    {Value: "claude-sonnet-4-20250514", ..., Group: "其他"},       // ← 删
}
```

端点:`GET /api/config/recap/models`(`server.go:343`),handler `getRecapModels`(`server.go:2774`)只读返回此列表。

### 2.2 前端:HSelect 是原生 `<select>`,无法手输(根因)

`web/src/components/ui/HSelect.vue` —— 是 `<select>`,**这是"不能自己填模型"的根本原因**:

```vue
<select class="select" :value="modelValue" @change="onChange">
  <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
</select>
```

原生 `<select>` 只能选 options 里的值,**无法接受任意文本输入**。即便后端返回列表,用户也只能在选项间切换。

### 2.3 两个使用点

| 位置 | 文件 | 当前实现 |
|------|------|---------|
| 设置页全局回顾 AI | `web/src/features/settings/components-v10/RecapCardV10.vue:112` | `<HSelect v-model="config.model" :options="modelOptions" />` |
| 主播级回顾模型 | `web/src/features/streamers/components/StreamerDrawer.vue:197-203` | `<HSelect :model-value="recapModelDraft" :options="recapOptions">` |

两处均经 `useRecapModels.ts` 拉取后端列表,扁平化为 HSelect options。

### 2.4 默认值

`internal/config/config.go:165`:`DefaultRecapModel = "deepseek-v4-pro"`(保留不动)。

## 三、方案设计

### 3.1 总体思路

**新增一个可输入的组合框组件 `HCombobox`**(input + 下拉),替代回顾模型处的 HSelect。既能从精简预设(DeepSeek 2 个)快捷选择,又能手动输入任意模型名称。HSelect 本身不改(其他地方仍用)。

> **为什么不改 HSelect**:HSelect 被其他字段(provider 选择等)正常使用,改它会影响所有 select。新增专用组件隔离风险,符合 V10 组件库"职责单一"的设计。

### 3.2 改动清单

#### 后端(1 文件)

**`internal/handler/server.go`** —— 精简 `recommendedRecapModels` 到只留 DeepSeek 2 个:

```go
var recommendedRecapModels = []RecapModelOption{
    {Value: "deepseek-v4-flash", Label: "deepseek-v4-flash（快速）", Group: "DeepSeek"},
    {Value: "deepseek-v4-pro", Label: "deepseek-v4-pro（默认）", Group: "DeepSeek"},
}
```

`RecapModelOption` 结构体、`getRecapModels` handler、路由 `GET /api/config/recap/models` **全部不动**(只删数组元素)。

#### 前端(4 文件:1 新增 + 3 修改)

**① 新增 `web/src/components/ui/HCombobox.vue`** —— 可输入组合框

核心行为:
- 渲染一个 `<input>`(可自由输入)+ 一个下拉列表(`<datalist>` 或自建浮层)。
- `v-model` 双向绑定 string,与 HInput/HSelect 接口一致(`modelValue: string`、`update:modelValue`)。
- props:`modelValue: string`、`options: { label: string; value: string }[]`、`placeholder?: string`。
- **实现选择原生 `<input list>` + `<datalist>`**:浏览器原生组合框,无需复杂浮层逻辑,自动支持输入过滤、键盘选择,样式可控。

```vue
<script setup lang="ts">
withDefaults(defineProps<{
  modelValue?: string
  options: { label: string; value: string }[]
  placeholder?: string
  disabled?: boolean
}>(), { modelValue: '', placeholder: '' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
// datalist id 需页面唯一,用 useId 或随机
import { useId } from 'vue'
const listId = useId()
function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLInputElement).value)
}
</script>

<template>
  <label class="form-field">
    <span v-if="$slots.label" class="form-label"><slot name="label" /></span>
    <input
      class="input"
      :value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      :list="listId"
      @input="onInput"
    >
    <datalist :id="listId">
      <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
    </datalist>
  </label>
</template>
```

> **为什么用 `<datalist>` 而非自建浮层**:① 原生组合框行为(输入过滤、键盘上下选、回车确认、点击选择)零 JS 实现;② 可访问性(aria)浏览器内置;③ 代码极简、无外部依赖、无浮层定位/ z-index / 点击外部关闭等复杂逻辑;④ 与 HInput 样式一致(同一个 `.input` class)。`<datalist>` 在所有现代浏览器(Chrome/Edge/Firefox/Safari)支持完善。

**② `web/src/components/ui/index.ts`** —— 导出 HCombobox

在现有导出列表中加一行 `export { default as HCombobox } from './HCombobox.vue'`。

**③ `web/src/features/settings/components-v10/RecapCardV10.vue`** —— 全局回顾模型改用 HCombobox

改动点(import + 模板 2 处):
- import 从 ui 加 `HCombobox`。
- 模型版本行:`<HSelect>` → `<HCombobox>`,options 传扁平化的 `modelOptions`(去掉 group 前缀拼接,直接用 value/label)。
- 保留 hint 文案"支持输入任意 OpenAI 兼容模型名称"。

```vue
<!-- 改前 -->
<HSelect v-model="config.model" :options="modelOptions" />
<!-- 改后 -->
<HCombobox v-model="config.model" :options="modelOptions" placeholder="deepseek-v4-pro" />
```

`modelOptions` computed 简化(不再拼 group 前缀,因为 combobox 下拉更简洁):

```ts
const modelOptions = computed(() => {
  const opts: { label: string; value: string }[] = []
  for (const g of recapModelGroups.value) {
    for (const m of g.models) opts.push({ label: m.label, value: m.value })
  }
  return opts
})
```

**④ `web/src/features/streamers/components/StreamerDrawer.vue`** —— 主播级模型改用 HCombobox

改动点(import + 模板 1 处):
- import 加 `HCombobox`。
- 回顾模型行:`<HSelect>` → `<HCombobox>`,options 仍含"跟随全局"(value='')。
- `recapOptions` computed 不变(已含"跟随全局" + 各模型)。

```vue
<!-- 改前 -->
<HSelect :model-value="recapModelDraft" :options="recapOptions" @update:model-value="recapModelDraft = $event">
  <template #label>回顾模型</template>
</HSelect>
<!-- 改后 -->
<HCombobox :model-value="recapModelDraft" :options="recapOptions" placeholder="留空跟随全局" @update:model-value="recapModelDraft = $event">
  <template #label>回顾模型</template>
</HCombobox>
```

### 3.3 不改动的东西

- `HSelect.vue` 本身不改(provider 选择、其他下拉仍用)。
- `useRecapModels.ts` 不改(group 聚合逻辑保留,combobox 也能用)。
- `RecapModelOption` 结构体、路由、handler 逻辑不改(只删数组元素)。
- OpenAPI spec 的 `GET /api/config/recap/models` 不改(响应 schema 不变,只是元素变少)。
- `DefaultRecapModel = "deepseek-v4-pro"` 不改。

## 四、数据流(改后)

```
后端 recommendedRecapModels (2个)
  → GET /api/config/recap/models
  → useRecapModels.load() → groups
  → RecapCardV10 / StreamerDrawer 扁平化为 options
  → HCombobox <datalist> 下拉(2个快捷选项 + 可手输任意值)
  → v-model 回写 config.model / recapModelDraft
  → PUT /api/config/recap (或 channel update) 保存
```

## 五、测试计划

### 5.1 后端测试

**修改 `internal/handler/server_test.go` 的 `TestGetRecapModels`**(现 `server_test.go:252-282`):

- 现有断言 `deepseek-v4-pro` 存在 + Group==DeepSeek → **保留**。
- 现有断言 `qwen-max` 存在 → **删除**(已从列表移除,再断言会失败)。
- **新增**断言:列表长度 == 2(防止未来误加回多余预设)。
- **新增**断言:`gpt-4o`、`qwen-plus`、`claude-sonnet-4-20250514` 均**不存在**(确认精简生效)。
- **新增**断言:两个元素的 Group 都是 "DeepSeek"。

### 5.2 前端测试

**新增 `web/src/components/ui/__tests__/HCombobox.test.ts`**(参照 HInput/HSelect 现有测试风格):

- 渲染:渲染出 `<input>` + `<datalist>` + 正确数量 `<option>`。
- `v-model`:输入触发 `update:modelValue`,props.modelValue 回显。
- options 映射:`<option>` 的 value/label 与 props.options 一致。
- placeholder 透传。

**无单测的组件**(RecapCardV10/StreamerDrawer):这两个组件本身无单测(注释写明"L3 视觉验证"),本次改动仅替换标签名 + import,属于低风险 UI 替换,不强制补组件级单测。

### 5.3 手动验证(交付前)

- 设置页回顾 AI:下拉能看到 2 个 DeepSeek 预设,也能手动输入任意模型名(如 `gpt-4o`、`my-custom-model`),保存后回读正确。
- 主播抽屉回顾模型:下拉含"跟随全局" + 2 个 DeepSeek,能手输,应用后生效。
- `make test` 全过、`npm run type-check` 通过、`npx vitest run` 全过、`npm run build` 通过。

## 六、验证清单

- [ ] 后端 `go test ./internal/handler/...` 通过(含修改后的 TestGetRecapModels)
- [ ] 全量 `go test ./...` 通过
- [ ] 前端 `npm run type-check` 通过
- [ ] 前端 `npx vitest run` 全过(含新增 HCombobox 测试)
- [ ] 前端 `npm run build` 通过
- [ ] `gofmt` 通过
- [ ] 后端预设只剩 2 个 DeepSeek
- [ ] 回顾模型处可手动输入任意模型名

## 七、风险评估

- **低风险**:改动面小(后端删 6 个数组元素,前端 1 新组件 + 3 文件标签替换)。
- **`<datalist>` 兼容性**:所有目标浏览器(Chrome/Edge/Firefox/Safari 现代版)原生支持,无 polyfill 需要。
- **向后兼容**:已保存配置中的 `model: "gpt-4o"` 等旧值仍能正常回显(combobox input 直接显示当前值,不在 options 里也不影响)。
- **OpenAPI 契约**:响应 schema 不变(仍是 models 数组),仅元素减少,不破坏前端类型。
