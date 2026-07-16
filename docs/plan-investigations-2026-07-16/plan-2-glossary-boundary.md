# 修复计划 2 — 术语校正词边界缺失(replaceTermBoundaryAware)

> **来源调查**:`/home/lioi/文档/investigations/术语校正误匹配子串-词边界缺失-replaceTermBoundaryAware.md`
> **代码核实结果**:⚠️ **调查文档声称"已修复",但本仓库代码实际未修复!**
> - `internal/recap/glossary_correction.go:66` 仍是 `strings.ReplaceAll`
> - `internal/recap/transcript_correction.go:61` 仍是 `strings.ReplaceAll`
> - `replaceTermBoundaryAware`/`hasAlphanumeric`/`isASCIIAlphanumeric` 3 函数不存在
> - `glossary_correction_test.go` 测试文件不存在
> - git log 无相关 commit
> - 文档里的路径是 `C:\Users\Administrator\Desktop\ccc\hzm`(另一台 Windows 机器),改动未同步到本仓库
>
> **严重度**:中(静默文本损坏)
> **状态**:计划待 codex 审核

---

## 一、问题确认(已实测对齐代码)

术语校正两个调用点用 `strings.ReplaceAll` 做无词边界的纯子串匹配。含 ASCII 字母数字的 term(如 `PPI`/`277`/`Nike`)嵌在更长字符串里(如 `SUPPORTPPI`/`123277456`/`Nikerussia`)时被误替换,静默损坏转写文本(位置B)和回顾正文(位置A)。纯 CJK term 不受影响(中文无词边界概念,现有长词优先排序兜底)。

**两个调用点**:
- 位置A `glossary_correction.go:66` `applyReplacementsPreservingQuotes`(回顾正文后处理)
- 位置B `transcript_correction.go:61` `correctTextWithRules`(转写文本校正,喂 prompt 前)

---

## 二、修复方案(仅对含 ASCII 字母数字的 term 做词边界判断)

**核心**:新增 `replaceTermBoundaryAware`,对含 `[A-Za-z0-9]` 的 term 强制左右边界非 ASCII 字母数字;纯 CJK term 回落到原 `strings.ReplaceAll`(零回归)。算法采用调查文档 §5.2 的手写 rune 扫描(不用正则,因 Go RE2 不支持 lookbehind,且 150 条规则各自 Compile 性能差)。

### 改动 1:新增 3 个函数

**文件**:`internal/recap/glossary_correction.go`(在 `applyReplacementsPreservingQuotes` 附近新增)

```go
// hasAlphanumeric 报告 s 是否包含 ASCII 字母或数字。
// 纯 CJK/标点的 term 返回 false,保持现有子串替换行为。
func hasAlphanumeric(s string) bool {
	for _, r := range s {
		if isASCIIAlphanumeric(r) {
			return true
		}
	}
	return false
}

// isASCIIAlphanumeric 报告 r 是否为 ASCII 字母或数字。
func isASCIIAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// replaceTermBoundaryAware 在 s 中把 term 替换为 canonical。对含 ASCII 字母数字
// 的 term 强制词边界(两侧不得是 ASCII 字母数字),防止 term 嵌在更长单词里被误替换。
// 纯 CJK/标点的 term 回落到 strings.ReplaceAll。
// 注意:term=="" 时直接返回 s(避免 strings.ReplaceAll 在每字符间插入 canonical)。
func replaceTermBoundaryAware(s, term, canonical string) string {
	if term == "" {
		return s
	}
	if !hasAlphanumeric(term) {
		return strings.ReplaceAll(s, term, canonical) // 纯 CJK 快路径
	}
	runes := []rune(s)
	termRunes := []rune(term)
	if len(runes) < len(termRunes) {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(runes) {
		if i+len(termRunes) <= len(runes) && runes[i] == termRunes[0] {
			match := true
			for j := 1; j < len(termRunes); j++ {
				if runes[i+j] != termRunes[j] {
					match = false
					break
				}
			}
			if match {
				leftOK := i == 0 || !isASCIIAlphanumeric(runes[i-1])
				rightIdx := i + len(termRunes)
				rightOK := rightIdx >= len(runes) || !isASCIIAlphanumeric(runes[rightIdx])
				if leftOK && rightOK {
					b.WriteString(canonical)
					i = rightIdx
					continue
				}
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}
```

> **关键边界 bug 修正**(调查文档 §5.6 已指出):`term==""` 检查必须放在 `hasAlphanumeric` 之前——否则空 term 走到 `strings.ReplaceAll(s,"",canonical)` 会在每个字符间插入 canonical,严重损坏文本。单元测试「空 term」用例会捕获。

### 改动 2:位置A 调用点

**文件**:`internal/recap/glossary_correction.go:66`

```go
// 旧
result = strings.ReplaceAll(result, repl.term, repl.canonical)
// 新
result = replaceTermBoundaryAware(result, repl.term, repl.canonical)
```

### 改动 3:位置B 调用点 + applied 记录修正

**文件**:`internal/recap/transcript_correction.go:57-63`

```go
// 旧
for _, rule := range rules {
	if !strings.Contains(output, rule.Term) {
		continue
	}
	output = strings.ReplaceAll(output, rule.Term, rule.Canonical)
	appliedSet[rule.Term] = struct{}{}
}

// 新(顺带修正:只在输出真变化时才记 applied,correction report 更准确)
for _, rule := range rules {
	if !strings.Contains(output, rule.Term) {
		continue
	}
	replaced := replaceTermBoundaryAware(output, rule.Term, rule.Canonical)
	if replaced != output {
		appliedSet[rule.Term] = struct{}{}
		output = replaced
	}
}
```

> 注:位置B 的 `strings.Contains` 前置过滤保留(性能优化:跳过完全不包含 term 的规则)。即使 Contains 命中但边界不满足导致 replaced==output,也不再记 applied。

---

## 三、新增单测

**文件**:`internal/recap/recap_test.go`(遵循现有结构,新函数测试也放这里,与 `TestApplyGlossaryCorrections` 等同文件)

> 决策:调查文档提议新建 `glossary_correction_test.go`,但本仓库现有惯例是 glossary 相关测试都集中在 `recap_test.go`(TestApplyGlossaryCorrections/TestCorrectTextWithRules 等均在其中)。为保持一致性,新增测试加到 `recap_test.go` 末尾,不新建文件。

### TestReplaceTermBoundaryAware(表驱动,16 用例)

| 场景 | 输入 | term | canonical | 期望输出 |
|------|------|------|-----------|----------|
| ASCII term 嵌更长单词 | `MAIL` | `AI` | `人工智能` | `MAIL`(不替换) |
| ASCII term 独立(两侧空格) | `AI 很强` | `AI` | `人工智能` | `人工智能 很强` |
| ASCII term 左中文右中文 | `发个MAIL给AI助手` | `AI` | `人工智能` | `发个MAIL给人工智能助手` |
| ASCII term 字符串首 | `AI开头` | `AI` | `人工智能` | `人工智能开头` |
| ASCII term 字符串尾 | `结尾AI` | `AI` | `人工智能` | `结尾人工智能` |
| 纯数字 term 嵌更长数字 | `12345` | `23` | `二十三` | `12345`(不替换) |
| 纯数字 term 独立 | `编号 23 done` | `23` | `二十三` | `编号 二十三 done` |
| 混合 ASCII+CJK term 两侧中文 | `B站直播` | `B站` | `哔哩` | `哔哩直播` |
| 纯 CJK term 紧挨汉字 | `律动文学` | `律动` | `绿冻` | `绿冻文学` |
| 纯 CJK term 回落 ReplaceAll | `多个律动出现` | `律动` | `绿冻` | `多个绿冻出现` |
| 空 term | `任意文本` | `` | `X` | `任意文本`(边界 bug 回归) |
| term 不存在 | `无匹配` | `AI` | `人工智能` | `无匹配` |
| canonical 含 ASCII | `用AI` | `AI` | `AI2` | `用AI2` |
| 多次独立出现 | `AI 和 AI` | `AI` | `人工智能` | `人工智能 和 人工智能` |
| 紧邻重复 term | `AIAI` | `AI` | `人工智能` | `AIAI`(两侧互相邻 ASCII,不替换) |
| term 等于整串 | `AI` | `AI` | `人工智能` | `人工智能` |

### TestHasAlphanumeric(9 用例)

| 输入 | 期望 |
|------|------|
| `""` | false |
| `中文` | false |
| `，。!` | false |
| `AI` | true |
| `A` | true |
| `1` | true |
| `B站` | true |
| `1q84` | true |
| `律动2` | true |

### TestApplyGlossaryCorrectionsAlphanumericBoundary(集成,走全路径)

```go
// 插入 term="AI" canonical="人工智能" + term="律动" canonical="绿冻"
input := "AI 很强，发个MAIL给AI助手，律动文学也出现了"
want  := "人工智能 很强，发个MAIL给人工智能助手，绿冻文学也出现了"
// 验证:MAIL 保持完整、独立 AI 替换、纯 CJK 律动照常替换
```

---

## 四、验证清单

- [ ] `glossary_correction.go` 新增 3 函数 + 位置A 调用点
- [ ] `transcript_correction.go` 位置B 调用点 + applied 逻辑
- [ ] `recap_test.go` 新增 3 个测试函数(16+9+1 用例)
- [ ] `go test ./internal/recap/... -count=1` 全过(基线已绿,需无回归)
- [ ] `gofmt -l internal/recap/*.go` 无输出
- [ ] `go vet ./internal/recap/...` 通过

---

## 五、风险评估

- **纯 CJK 零回归**:`hasAlphanumeric(term)==false` 时直接 `strings.ReplaceAll`,与原逻辑完全一致。TestReplaceTermBoundaryAware 含专门 CJK 用例验证。
- **空 term 边界 bug**:`term==""` 提前返回,杜绝 `ReplaceAll(s,"",canonical)` 损坏文本。现有代码不会传空 term(两处 build 函数都过滤了 `term==""`),但防御性处理 + 测试覆盖。
- **位置B applied 记录修正**:原代码 `Contains` 命中即记 applied,即使 `ReplaceAll` 未变化;新代码只在 `replaced != output` 时记。这是行为修正——使 correction report 更准。**潜在影响**:若有测试断言旧的不准 applied 记录会失败,需检查。已查 `TestCorrectTextWithRules`(recap_test.go:1540),它断言的是真实替换的 term,不受影响。
- **性能**:手写 rune 扫描 O(n*m),n=文本 rune 数,m=term rune 数。150 条规则 × 27875 字符 transcript 仍在毫秒级(调查文档验证过端到端一致)。

---

## 六、文档同步(修复后)

- `internal/recap/CLAUDE.md` changelog:词边界感知替换 + 测试计数更新
- 根 `AGENTS.md`/`CLAUDE.md` changelog
- 调查文档状态从"已修复(不符)"更正为真正"已修复(本仓库)"
