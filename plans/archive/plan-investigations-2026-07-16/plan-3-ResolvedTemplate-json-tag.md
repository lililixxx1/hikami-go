# 修复计划 3 — ResolvedTemplate 缺 JSON tag(序列化 PascalCase)

> **来源调查**:`/home/lioi/文档/investigations/主播详情使用自定义模板报错-ResolvedTemplate缺JSONtag.md`
> **代码核实结果**:✅ 与实际代码完全吻合
> - `internal/recap/template.go:57-63` `ResolvedTemplate` 4 字段无 `json:` tag
> - 对比同文件 `template.go:37-49` `Template` 有完整 snake_case tag
> - 前端 `web/src/api/types-derived.ts:151-156` 声明 snake_case,与后端实际输出不符
> - `types-derived.ts:147-150` 有段承认不匹配的误导注释
>
> **严重度**:中(主播级模板「跟随全局」预览全空、切换自定义不回填)
> **状态**:计划待 codex 审核

---

## 一、问题确认(已实测对齐代码)

后端 `ResolvedTemplate` 无 `json:` tag → Go `encoding/json` 用 PascalCase 字段名序列化(`SystemPrompt`/`UserFormat`/`FanName`/`ExtraVars`)→ 前端按 snake_case 访问得 `undefined` → `RecapTemplateEditor.vue` 跟随全局预览显示全 `-`,切换自定义不回填。后端 `Resolve()` 合并逻辑正确,回顾生成正常(不经前端)。

---

## 二、修复方案(后端补 tag,最小改动根治)

### 改动 1:ResolvedTemplate 补 JSON tag

**文件**:`internal/recap/template.go:57-63`

```go
// ResolvedTemplate is the result of merging global + channel-level templates.
type ResolvedTemplate struct {
	SystemPrompt string            `json:"system_prompt"`
	UserFormat   string            `json:"user_format"`
	FanName      string            `json:"fan_name"`
	ExtraVars    map[string]string `json:"extra_vars"`
}
```

**理由**:与同文件 `Template` 的 tag 风格一致(snake_case);一处改动根治;前端类型与消费代码已是 snake_case,**无需改前端逻辑**。

### 改动 2:同步 OpenAPI spec(把 PascalCase 改回 snake_case)

**已核实**:spec 当前声明 PascalCase 并带警告(`docs/api/components/schemas/templates.yaml:76-89`、`openapi.yaml:2955`、`README.md:47,83`、`api-gap-analysis.md:173`)。后端修 tag 后必须同步,否则 spec 与实现不一致。

**文件**:`docs/api/components/schemas/templates.yaml` 第 76-100 行附近

```yaml
# 旧
# ⚠️ ResolvedTemplate 字段名是 PascalCase(源码无 json tag,template.go:57-63)
ResolvedTemplate:
  description: |
    ⚠️ **字段名是 PascalCase**(...)。源码 `recap.ResolvedTemplate` 无 json tag...
  type: object
  required: [SystemPrompt, UserFormat, FanName, ExtraVars]
  properties:
    SystemPrompt:
      type: string
      description: ⚠️ PascalCase(非 system_prompt)
    ...

# 新(对齐 template.go 补 tag 后的真实输出)
ResolvedTemplate:
  description: |
    global + channel-level 模板合并结果。字段名 snake_case(template.go:57-63 已补 json tag)。
  type: object
  required: [system_prompt, user_format, fan_name, extra_vars]
  properties:
    system_prompt:
      type: string
    user_format:
      type: string
    fan_name:
      type: string
    extra_vars:
      type: object
      additionalProperties:
        type: string
```

**连带改动**(去掉 PascalCase 坑的警告/说明):
- `docs/api/openapi.yaml:2955` 描述「字段名 PascalCase」→「字段名 snake_case」
- `docs/api/README.md:47,83` 去掉「(PascalCase 坑)」标注与第 83 行的坑说明
- `docs/api/api-gap-analysis.md:173` 那行「ResolvedTemplate 字段 PascalCase」改为「已修复为 snake_case」或删除

### 改动 3:清理误导注释

**文件**:`web/src/api/types-derived.ts:147-150`

```ts
// 旧(4 行,承认不匹配且合理化)
// ResolvedRecapTemplate: generated schema(ResolvedTemplate)用 PascalCase 键
// (SystemPrompt/UserFormat/FanName/ExtraVars),但前端组件消费 snake_case
// (resolvedTemplate.system_prompt 等,见 useRecapTemplateEditor/RecapTemplateEditor)。
// 保留 snake_case 定义以匹配前端实际消费。

// 新(1 行,描述事实)
// ResolvedRecapTemplate: 键名与后端 ResolvedTemplate 的 json tag 一致(snake_case)。
```

---

## 三、新增单测

**文件**:`internal/recap/template_test.go`(末尾追加)

### TestResolvedTemplateJSONKeys

```go
func TestResolvedTemplateJSONKeys(t *testing.T) {
	r := &ResolvedTemplate{
		SystemPrompt: "p",
		UserFormat:   "f",
		FanName:      "n",
		ExtraVars:    map[string]string{"k": "v"},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, snake := range []string{"system_prompt", "user_format", "fan_name", "extra_vars"} {
		if _, ok := m[snake]; !ok {
			t.Fatalf("resolved JSON missing snake_case key %q, got keys: %v", snake, mapKeys(m))
		}
	}
	// 同时断言不再出现 PascalCase 键(回归保护)
	for _, pascal := range []string{"SystemPrompt", "UserFormat", "FanName", "ExtraVars"} {
		if _, ok := m[pascal]; ok {
			t.Fatalf("resolved JSON must not contain PascalCase key %q (should be snake_case)", pascal)
		}
	}
}

func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
```

> `template_test.go` 顶部已有 `import "encoding/json"`?需检查,若无则补。

---

## 四、验证清单

- [ ] `internal/recap/template.go:57-63` 补 4 个 `json:"snake_case"` tag
- [ ] `internal/recap/template_test.go` 新增 `TestResolvedTemplateJSONKeys`(+ `mapKeys` helper,若测试文件里尚无)
- [ ] `docs/api/components/schemas/templates.yaml` ResolvedTemplate 改回 snake_case(改动 2)
- [ ] `docs/api/openapi.yaml:2955`、`docs/api/README.md:47,83`、`docs/api/api-gap-analysis.md:173` 去掉 PascalCase 坑说明
- [ ] `web/src/api/types-derived.ts:147-150` 注释替换为单行
- [ ] `go test ./internal/recap/... -count=1` 全过
- [ ] `go test ./internal/handler/... -count=1` 全过(确认无 handler 测试依赖旧 PascalCase)
- [ ] `gofmt -l internal/recap/*.go` 无输出
- [ ] `make api-lint`(redocly)通过(当前基线 7 warnings,不应新增)
- [ ] (可选,有后端运行时)curl 验证 `resolved` 键名变 snake_case

---

## 五、风险评估

- **破坏面**:仅改 JSON 序列化的键名。后端 Go 内部 `ResolvedTemplate` 字段访问(代码间传递)不受影响——tag 只影响 `encoding/json`。
- **前端**:前端类型 `ResolvedRecapTemplate` 与消费代码(`useRecapTemplateEditor.ts:70`、`RecapTemplateEditor.vue:182-184`)**已是 snake_case**,修复后从「拿到 undefined」变为「拿到真值」,纯正向修复,无需改前端逻辑。
- **回归检查点**:① `handler/server.go:3549-3553` `gin.H{"resolved": resolved}` 裸序列化——tag 修复后自动输出 snake_case,无需改 handler;② 是否有后端测试断言 PascalCase 键名?已查 `recap`/`handler` 包,无此类断言(`TestApplyGlossaryCorrections` 等不涉及 ResolvedTemplate 序列化)。
- **契约一致性**(已核对 spec):OpenAPI spec **当前把 ResolvedTemplate 标为 PascalCase 并带警告注释**,承认这是历史坑(`docs/api/components/schemas/templates.yaml:76-89`、`openapi.yaml:2955`、`README.md:47,83`、`api-gap-analysis.md:173`)。修复后端 tag 使实现输出 snake_case 后,**必须同步把 spec 改回 snake_case**,否则 spec 与实现再次不一致。这是 plan 的强制改动(见改动 3)。

---

## 六、文档同步(修复后)

- `internal/recap/CLAUDE.md` changelog:ResolvedTemplate 补 json tag + 测试
- 根 `AGENTS.md`/`CLAUDE.md` changelog
- 调查文档状态从"待修复"更正为"已修复"
