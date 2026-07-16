# 修复计划 1 — TemplateCardV10「添加变量」无效(kvRows setter 丢弃空 key 行)

> **来源调查**:`/home/lioi/文档/investigations/回顾模板添加变量无反应-kvRows空key被setter丢弃.md`
> **代码核实结果**:✅ 与实际代码完全吻合(`TemplateCardV10.vue:35-52` writable computed、`44-51` setter `if(k)` 过滤、`62-64` addKvRow、`151 :key="i"`)
> **严重度**:中(全局模板「额外变量」无法新增)
> **状态**:计划待 codex 审核

---

## 一、问题确认(已实测对齐代码)

`kvRows` 是 writable computed,getter 依赖 `extraVars`(JSON 字符串),setter 又写 `extraVars`。setter 在序列化时 `if (k)` 丢弃空 key 行 → 点击「+ 添加变量」追加 `{key:'',value:''}` → setter 写回 `'{}'` → getter 重算返回 `[]` → **新输入框立即被销毁**。次级问题:`:key="i"` 用数组索引。

---

## 二、修复方案(解耦编辑态与序列化)

**核心**:用独立 `ref<KVRow[]>` 管理编辑态(保留空 key 中间态),仅在保存时 flush 成 JSON。消除读写环。

### 改动 1:`kvRows` 从 computed 改为 ref + id 字段

**文件**:`web/src/features/settings/components-v10/TemplateCardV10.vue`

```ts
// 替换第 33-52 行整个 kvRows computed
interface KVRow { id: number; key: string; value: string }
const kvRows = ref<KVRow[]>([])
let kvIdSeq = 0

// 从 extraVars(JSON 字符串)解析填充到 kvRows
function syncKvRowsFromExtraVars() {
  try {
    const obj = JSON.parse(extraVars.value || '{}') as Record<string, string>
    kvRows.value = Object.entries(obj).map(([key, value]) => ({
      id: ++kvIdSeq, key, value: String(value ?? ''),
    }))
  } catch {
    kvRows.value = []
  }
}

// 把 kvRows 序列化回 extraVars(丢弃空 key,仅在保存前调用)
function flushKvRowsToExtraVars() {
  const obj: Record<string, string> = {}
  for (const r of kvRows.value) {
    const k = r.key.trim()
    if (k) obj[k] = r.value
  }
  extraVars.value = JSON.stringify(obj)
}
```

### 改动 2:增删改函数直接操作 ref(不再触发 setter)

```ts
function updateKvKey(i: number, key: string) {
  kvRows.value = kvRows.value.map((r, idx) => idx === i ? { ...r, key } : r)
}
function updateKvValue(i: number, value: string) {
  kvRows.value = kvRows.value.map((r, idx) => idx === i ? { ...r, value } : r)
}
function addKvRow() {
  kvRows.value = [...kvRows.value, { id: ++kvIdSeq, key: '', value: '' }]
}
function removeKvRow(i: number) {
  kvRows.value = kvRows.value.filter((_, idx) => idx !== i)
}
```

### 改动 3:保存前 flush

```ts
async function handleSave() {
  flushKvRowsToExtraVars()  // ← 保存时才丢弃空 key、写回 extraVars
  await save()
  emit('saved')
}
```

### 改动 4:loadData/applyPreset 后同步

修改 `onMounted`(第 108-110 行)与 `handleApplyPreset`(第 69-73 行),完成后调用 `syncKvRowsFromExtraVars()`:

```ts
onMounted(async () => {
  await Promise.all([loadData(), loadPresets()])
  syncKvRowsFromExtraVars()   // ← 后端 extra_vars 反映到编辑态行
})

async function handleApplyPreset(name: string) {
  selectedPresetName.value = ''
  if (!name) return
  await applyPreset(name)
  syncKvRowsFromExtraVars()   // ← 预设可能改了 extra_vars
}
```

### 改动 5(次级):`:key` 改用稳定 id

模板第 151 行:`<div v-for="(row, i) in kvRows" :key="i" ...>` → `:key="row.id"`。

---

## 三、新增单测

**文件**:`web/src/features/settings/components-v10/__tests__/TemplateCardV10.spec.ts`(新建)

`TemplateCardV10.vue` 此前无单测(头注释标"L3 视觉验证,无单测"),本次补上。用例:

1. **添加变量后行数 +1 且不消失**(核心 bug 回归测试):mount → 初始 `.kv-row` 为 0 → 点「+ 添加变量」→ `nextTick` 后 `.kv-row` 为 1 → 再 `nextTick` 仍为 1(验证不闪退)
2. **空 key 行保存时被丢弃**:加一行空 key → mock `save()` → 调用 handleSave → 断言传给 API 的 extra_vars 为 `'{}'`
3. **删除中间行内容不串行**:3 行 key=`a/b/c` → 删除中间 b 行 → 剩余两行 key 仍为 `a`/`c`(验证稳定 id 修复)

> mock 策略:`useRecapTemplateEditor` 通过 props/参数注入,测试用 vi.mock 替换 `loadData`/`save` 等,或直接 mount 时依赖真实 composable + mock `@/api/recap-templates`。

---

## 四、验证清单

- [ ] 应用 5 处改动
- [ ] 新增 `TemplateCardV10.spec.ts`(≥3 用例)
- [ ] `cd web && npx vitest run`(当前基线:25 文件 172 测试,需全过)
- [ ] `cd web && npm run type-check` 通过
- [ ] `cd web && npm run build` 通过
- [ ] 浏览器手动验证:添加变量 → 输入 key/value → 保存 → 刷新确认持久化

---

## 五、风险评估

- **数据安全**:无风险。改动只在编辑态;保存时才 flush,与原行为等价(都丢空 key)。已保存的 extra_vars 不受影响。
- **回归面**:仅 `TemplateCardV10.vue` 一个文件 + 新测试文件。主播级 `RecapTemplateEditor.vue` 用 JSON 文本域,不受影响。
- **兼容性**:`useRecapTemplateEditor` composable 的 `extraVars` ref 接口不变,`save()` 调用不变,只是 flush 时机从「每次 setter」后移到「保存时」。

---

## 六、文档同步(修复后)

- `web/CLAUDE.md` changelog:TemplateCardV10 kvRows 解耦 + 新增单测
- 根 `AGENTS.md`/`CLAUDE.md` changelog 条目
