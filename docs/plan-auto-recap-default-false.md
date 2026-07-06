# 计划：把新建主播的 `auto_recap` 默认值改为 false

> 状态：**已审批，待执行**（用户要求暂不执行）
> 创建：2026-07-06
> 触发：用户反馈"添加主播后默认开启回顾"不符合预期，要求永久改默认为关闭。

## 背景

`auto_recap`（per-channel 自动回顾开关）采用三态指针 `*bool`，调用方未提供（`nil`）时走 fallback。当前 Create/Bootstrap 的 fallback 写死为 `true`，导致"新建主播默认自动回顾"。其他布尔字段（`auto_record`/`auto_asr`/`auto_publish`）走普通零值机制（不传即 false），唯独 `auto_recap` 被特殊处理为默认 true —— 这是历史承诺（v32 schema 迁移 `DEFAULT 1`），现在用户要求反转为默认 false。

## 目标

把 Create/Bootstrap 的 fallback 从 `true` 改为 `false`。三态语义（`nil`/显式 true/显式 false）和"Update 时 nil 保留现有值"的行为**完全保留**。

## 改动清单（6 个文件）

### 1. 应用层 fallback（核心行为变更）

**`internal/channel/channel.go`** — 2 处一行改：
- `:178` `boolToInt(resolveAutoRecap(input.AutoRecap, true))` → `false`（Create 路径）
- `:257` `boolToInt(resolveAutoRecap(input.AutoRecap, true))` → `false`（Bootstrap 路径）

`resolveAutoRecap` 函数本身（`:546`）**不动** —— 它是通用三态解析器，fallback 由调用方传入。

### 2. DB schema DEFAULT 一致性

**`internal/db/migrate.go:187`**：
- `auto_recap INTEGER NOT NULL DEFAULT 1` → `DEFAULT 0`

> 仅影响全新部署建表那一刻；已存在的库列 DEFAULT 不会回溯改，但应用层总显式插值，所以老库行为完全由代码层决定，改后立刻生效。

### 3. 测试断言同步（改完才不会挂）

**`internal/channel/channel_test.go`** — 2 处：

**3.1 `TestAutoRecapRoundTrip`（`:1164`）第 1 步：**
- `:1168` 注释 `// 1. Create 不提供 auto_recap → 默认 true` → `→ 默认 false`
- `:1177-1179` 断言：
  ```go
  // 旧
  if !ch.AutoRecap {
      t.Fatalf("AutoRecap = false on create (omitted), want true (default)")
  }
  // 新
  if ch.AutoRecap {
      t.Fatalf("AutoRecap = true on create (omitted), want false (default)")
  }
  ```
- 其余 4 步（显式 false、nil 保留 false、显式 true、nil 保留 true）**不动** —— 它们验证的是显式值或 DB 持久值，与 fallback 无关。

**3.2 `TestBootstrapAutoRecapDefault`（`:1240`）：**
- `:1237-1239` 函数注释：把"频道默认开启自动回顾（对齐 v32 迁移 DEFAULT 1 与历史「ASR 后自动回顾」行为）"改成"频道默认关闭自动回顾（2026-07-06 反转默认，对齐 DEFAULT 0）"
- `:1246` 行内注释 `// AutoRecap=nil → 默认 true` → `→ 默认 false`
- `:1257-1259` 断言：
  ```go
  // 旧
  if !omit.AutoRecap {
      t.Fatalf("ch_omit AutoRecap = false, want true (default when omitted)")
  }
  // 新
  if omit.AutoRecap {
      t.Fatalf("ch_omit AutoRecap = true, want false (default when omitted)")
  }
  ```
- `ch_off`（显式 false，`:1265-1267`）**不动**。

### 4. 文档同步（措辞修正，非必须但建议）

- **`internal/channel/CLAUDE.md`** — `:67`「默认 true」→「默认 false」、`:113` 测试描述同步
- **`internal/db/CLAUDE.md`** — `:32`、`:108` 把"v32 默认 1"措辞更新为"默认 0（2026-07-06 反转）"
- **`cmd/hikami/CLAUDE.md`** — 若 `:23/27` 提到"默认自动回顾"也同步
- **`channel.go:540-545` `resolveAutoRecap` 注释** — "唯独 auto_recap 因承诺「默认 true」需三态"这句要更新为"默认 false"

## 不改的部分（明确边界）

- `resolveAutoRecap` 函数体 — 通用解析器
- Update 路径（`:341`，`fallback=existingAutoRecap`）— 本来就是保留现有值，正确
- `SaveIdentified`（`:434-435`）— 识别保存时复用 existing 值，正确
- 前端 `StreamersView.vue` / `api/types.ts` — `auto_recap` 是普通 `boolean` 字段，不传时 Go 侧反序列化为 nil 走新 fallback false，UI 开关会正确显示"关"。**无需改前端**。
- 运行时消费方 `cmd/hikami/main.go:250`（`if !ch.AutoRecap { return }`）— 只读字段，行为自然跟随，无需改。

## 验证步骤

1. `gofmt -w internal/channel/channel.go internal/channel/channel_test.go internal/db/migrate.go`
2. `go build ./...` 确认编译通过
3. `go test ./internal/channel/... ./internal/db/...` — 重点跑这两个包，确认 `TestAutoRecapRoundTrip` / `TestBootstrapAutoRecapDefault` 通过
4. `go test ./...` — 全量回归
5. （可选部署）`make build` 重编 `./hikami` + `systemctl restart hikami` 让新行为生效

## 行为影响说明

改完后：
- **所有新建主播**（手动添加 / 引导识别保存 / bootstrap 配置）**默认不会自动生成回顾**，需用户在主播详情页手动打开"回顾"开关。
- **已有主播的设置完全不受影响**（Update 走 existing 值，不读 fallback）。
- 老数据库无需迁移 —— 应用层 Create 总是显式插值，DB DEFAULT 只在全新建表那刻用一次。

## 执行入口

用户下次说"执行 / 继续 auto-recap 计划"时，按上面清单 1→2→3→4 顺序执行即可。
