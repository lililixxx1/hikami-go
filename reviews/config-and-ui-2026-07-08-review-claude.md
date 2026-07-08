# 配置默认值 + 设置页 UI 三处修复 — Claude CLI 审核

> 分支:`fix/config-and-ui-2026-07-08`
> 审核对象:5 commits(`2172e84`..`45e8e09`),即 `git diff main..HEAD -- internal/ web/src/`
> 审核方式:Claude Code 2.1.202,--print 模式,允许 Read/Grep
> 审核轮次:计划审核 v1(NEEDS_CHANGES)→ v2(APPROVED);执行后审核(APPROVED)

## 计划审核(2 轮)

### v1:NEEDS_CHANGES(2 必改 + 2 建议)
1. **必改**:问题 3 措辞——"ffmpeg/ffprobe 是硬依赖"的论断需修正(实际 `probeTool` 对所有工具降级,但 `StartupError` 对 required=true 的 ffmpeg/ffprobe 缺失确实 fatal)。措辞从"硬依赖"改为"风险控制"(改错路径 → 重启 fatal → web 不可达无法纠正)。
2. **必改**:问题 4 `@click.stop` 改 `@click.self`——`@click.stop` 只拦截 dialog 根元素,dialog 内子元素(body/footer)点击仍冒泡;`.self` 修饰符精确匹配 `event.target === currentTarget` 更健壮。
3. 建议:问题 1 grep 范围补 `web/`。
4. 建议:问题 3 测试补 presence-aware(nil 字段) + 空字符串 case。

### v2:APPROVED
4 个修订点全部落实。

## 执行后审核:APPROVED ✅

### ✅ 问题 1:output_root 默认值改名
`config.go:744` / `config.full.example.yaml:1` / `docs/DESIGN.md` 三处一致改为 `hikami-go`。

### ✅ 问题 2:设置页 sidebar sticky
`settings-v10.css` 新增 `position: sticky; top: 0; max-height: calc(100vh - var(--topbar-h))`。

### ✅ 问题 3:tools runtimeconfig 段(后端)
- **ApplyOverrides 正确性**:`config.go:706-714` presence-aware 逻辑正确(nil 保留基线,`*dto.YTDLP` 覆盖)。
- **DB v35 迁移安全**:`migrate.go:208-216` 标准表重建模式无损(临时表→INSERT SELECT→DROP→RENAME),旧 6 段数据全量保留。
- **CHECK 约束已加 tools**:`migrate.go:209` 确认白名单 7 段。
- **refreshRuntimeStatus**:`server.go:3069` 正确调用,保存后重新 Probe。
- **测试覆盖**:config +3(覆盖/presence/空串清空)、handler +1(roundtrip 含持久化断言),全过。

### ✅ 问题 3:tools 段(前端)
- **ToolsCardV10**:上半表单(onMounted fetch + save emit)+ 下半只读探测表。
- **settings.ts**:手写 ToolsConfig 类型 + get/put 封装(注释标注待 OpenAPI 重新生成)。
- **契约**:emit('saved') → 壳重拉 runtime → props.tools 刷新,闭环正确。

### ✅ 问题 4:HDialog 居中
- **嵌套结构**:overlay 包 dialog。
- **@click.self**:仅 overlay 自身点击触发 close,dialog 内点击不冒泡(测试覆盖)。
- **flex 居中**:overlay 原有 flex,嵌套后生效。
- **测试**:+2(点击内容不关闭 + 嵌套结构验证),全过。

### ✅ 无遗漏回归
前端类型检查通过、HDialog 6 测试全过、config/handler 测试全过、未发现引用旧结构的残留。

---

**总结论**:4 个修复均符合预期,无必改项。APPROVED。
