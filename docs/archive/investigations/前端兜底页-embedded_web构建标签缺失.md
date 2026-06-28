# 故障记录：前端只显示兜底页（embedded_web 构建标签缺失）

> **状态**：已修复（2026-06-27）。本次修复一并发现并修掉了 CI 发布工作流的同根问题（见 §7.1）。
> **发现日期**：2026-06-26
> **影响范围**：Web 管理界面完全不可用（访问根路径仅返回纯文本兜底页），REST API 与直播录制功能不受影响。
>
> 注：本文档已对运行现场信息（公网 IP、进程 PID、session id、直播标题等）脱敏，保留时间线、现象与判断依据。

---

## 1. 现象

浏览器访问根路径（如 `http://<服务器IP>:6334/`）时，页面只显示：

> # Hikami-Go
> 服务已运行。请使用 REST API 管理直播录制、回放处理和归档任务。

没有 Vue 管理界面，也没有任何静态资源加载。

---

## 2. 根因结论（一句话）

**当前运行的二进制是用裸 `go build`（未带 `-tags embedded_web`）编译的，因此前端没有被嵌入二进制，服务端走降级逻辑返回了兜底页。**

前端产物文件本身完好（`web/dist/` 和 `cmd/hikami/webdist/` 都齐全且正确），问题纯粹出在"编译时没带构建标签"。

---

## 3. 证据链

### 3.1 兜底页来源

`internal/handler/server.go:456` 的 `index()` 方法是纯 API 模式的降级处理：

```go
func (s *Server) index(ctx *gin.Context) {
    ctx.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!doctype html>
<html lang="zh-CN">
<head>...<title>Hikami-Go</title></head>
<body><main><h1>Hikami-Go</h1><p>服务已运行。请使用 REST API 管理...</p></main></body>
</html>`))
}
```

它只在 `s.webFS == nil` 时被注册（`server.go:444` 的 `else` 分支）。也就是说，**只要走到了这个兜底页，就证明 `webFS` 是空的**。

### 3.2 webFS 为何为空

`cmd/hikami/main.go:356-364`：

```go
var webFS fs.FS
if _, statErr := fs.Stat(webDistFS, "webdist"); statErr == nil {
    sub, subErr := fs.Sub(webDistFS, "webdist")
    if subErr == nil {
        webFS = sub
        logger.Info("embedded web frontend loaded")
    }
}
```

`webDistFS` 来自两个按构建标签互斥的文件：

| 文件 | 构建标签 | webDistFS 的值 |
|---|---|---|
| `cmd/hikami/embed.go` | `//go:build embedded_web` | `//go:embed all:webdist`（真实嵌入） |
| `cmd/hikami/embed_none.go` | `//go:build !embedded_web` | 空 `embed.FS` |

**不带 `-tags embedded_web` 编译时，只有 `embed_none.go` 参与编译，`webDistFS` 是空的**，于是 `fs.Stat(webDistFS, "webdist")` 必然失败 → `webFS` 保持 `nil` → 走兜底页。

注意：这与 `cmd/hikami/webdist/` 目录是否存在**完全无关**——只要没带标签，`embed.go` 根本不参与编译，`//go:embed` 指令不会被求值。

### 3.3 二进制层面的铁证

对运行中的 `./hikami` 二进制（编译于 2026-06-26 14:45:07）做字符串检查：

```
strings ./hikami | grep -c 'webdist/'          →  0
strings ./hikami | grep -cE 'index-[A-Za-z0-9_-]{8}' →  0   (前端构建产物的文件名)
strings ./hikami | grep -c 'embedded_web'      →  0
```

如果是 `make build`（含 `-tags embedded_web`）的产物，`webdist/index.html`、`webdist/assets/*` 这些路径会作为 embed FS 的目录项被编进二进制。实测全是 0，**证明该二进制编译时没有带标签**。

### 3.4 前端产物是好的（排除前端构建失败）

```
web/dist/             ~1.9M  (index.html 正确引用 Vue 产物的 JS/CSS)
cmd/hikami/webdist/   ~1.9M  (web-build 已复制到位)
```

`cmd/hikami/webdist/index.html` 内容完整，引用了 Vue 产物，且产物文件都存在。所以问题不在前端，而在 Go 编译命令。

---

## 4. 为什么会发生（tag 丢失的环节）

项目里有多个能产出二进制的 Makefile 目标，标签使用并不一致：

| Makefile 目标 | 命令 | 是否嵌入前端 |
|---|---|---|
| `make build` | `web-build` + `go build -tags embedded_web` | ✅ |
| `make build-go` | `go build -tags embedded_web` | ✅ |
| `make build-go-api` | `go build`（无标签）→ `./hikami-api` | ❌（设计如此） |
| `make run` | `go run -tags embedded_web` | ✅ |
| 直接 `go build ./cmd/hikami` | 无标签 | ❌（**踩坑点**） |

`make build-go-api` 产出的是 `./hikami-api`（带 `-api` 后缀，刻意区分），所以运行中的 `./hikami` **不是** 来自 `build-go-api`。

最可能的来源是：**某次会话（agent / IDE / 手动）直接执行了 `go build ./cmd/hikami` 或 `go build -o ./hikami ./cmd/hikami`，没有带 `-tags embedded_web`，产物名恰好也是 `hikami`，随后被启动替换了旧进程。** 这种命令编译能通过、运行也"看起来正常"（服务起得来、API 通、日志没报错），因此极具迷惑性——直到有人去访问 Web UI 才会发现前端没了。

bash 历史中 `go build` 类直接命令仅以 `make build` 形式出现过两次（均在改名早期，行 413/416），14:45 那次编译对应的直接命令在交互历史中没有明确记录，与"由非交互式工具（agent 会话）触发编译"的判断一致。

### 4.1 放大风险的设计因素

1. **裸 `go build ./cmd/hikami` 能成功**——因为 `embed_none.go` 提供了空实现兜底（ISS-1 的设计初衷是让纯 API 模式可独立编译/测试）。这个设计本身合理，但副作用是"忘记带标签"不会导致编译失败，而是静默降级。
2. **没有运行时显眼告警**——`webFS == nil` 时只走降级，日志里没有醒目的 WARN（启动日志里也不会打印"前端未嵌入"）。
3. **产物名不区分**——`make build` 和裸 `go build` 都可能产出 `./hikami`，从文件名无法判断带没带标签。

---

## 5. 如何复现

```bash
# 裸编译（无标签）→ 会复现兜底页
HOME=/root GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod \
  go build -o ./hikami-bare ./cmd/hikami
./hikami-bare -config config.yaml
curl http://127.0.0.1:6334/   # 返回兜底文本页

# 正确编译（带标签）→ 返回 Vue 前端
HOME=/root GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod \
  go build -tags embedded_web -o ./hikami ./cmd/hikami
./hikami -config config.yaml
curl http://127.0.0.1:6334/   # 返回 <!doctype html>...<div id="app"></div>
```

---

## 6. 解决方案

### 6.1 立即修复（待直播录制结束后执行）

> ⚠️ **若当前有直播正在录制**：ffmpeg 录制子进程是 hikami 主进程的子进程，通过管道接收流数据。**重启会立刻中断录制**，必须等所有 `recording` 状态的 session 变为 `ended`/后续状态后再操作。

重新编译并重启（`cmd/hikami/webdist/` 已存在，无需重跑 `make web-build`，除非前端有改动）：

```bash
# 编译（带标签）
make build-go
# 等价：go build -tags embedded_web -o ./hikami ./cmd/hikami

# 停掉旧进程（确认已无录制进行后）
kill <旧 hikami PID>

# 启动新进程
nohup ./hikami -config config.yaml > hikami.log 2>&1 &
```

### 6.2 验证

```bash
# 1. 二进制应含 webdist 路径（轻量静态检查）
strings ./hikami | grep -c 'webdist/'   # 应 > 0

# 2. 启动日志应出现 "embedded web frontend loaded"（运行时 smoke test）

# 3. HTTP 根路径返回 Vue 页面（运行时 smoke test）
curl -s http://127.0.0.1:6334/ | grep -c '<div id="app">'   # 应为 1
```

> 第 1 项是轻量静态启发式检查（`strings` 匹配），能快速判断二进制是否带标签；第 2、3 项是更可靠的运行时 smoke test，应作为部署后的强制验证项。

---

## 7. 预防措施（已实施，2026-06-27）

> 调查中额外发现：CI 发布工作流 `.github/workflows/release.yml` 在构建全部 4 个 release 矩阵二进制时，同样**从未传 `-tags embedded_web`**（尽管前端已构建并拷到 `cmd/hikami/webdist/`）。即每个 tag release 发布的二进制前端都是空的，与本文记录的本地事故同根。详见 §7.1。

1. **让裸 `go build` 输出明显的运行时告警**（✅ 已实施，`cmd/hikami/main.go`）：当 `webFS == nil` 时，在启动日志中打印一条醒目的 WARN
   `embedded web frontend is NOT loaded (binary built without -tags embedded_web); serving API-only fallback page at /`
   这样即使再次踩坑，也能在日志里立刻发现，而不必等到有人访问 Web UI。

2. **加构建自检**（✅ 已实施，`Makefile` 的 `build-go` 目标）：在 `make build-go` 末尾加了一道 `strings` 静态检查，若 `./hikami` 不含 `webdist/` 立即报错退出。运行时 smoke test 仍建议作为部署后的强制验证项（见 §6.2）。

3. **统一入口**（⏳ 待办）：文档中强调"凡是部署用的二进制，一律走 `make build` / `make build-go`，禁止直接 `go build ./cmd/hikami`"。可在 README/AGENTS.md 里加一句构建须知。

4. **产物名区分**（⏳ 待办，可选）：考虑让裸 `go build ./cmd/hikami` 不产出到 `./hikami`，或让 `make build-go` 的产物带版本/标签后缀，从文件名即可辨识。此项改动较大，需评估对部署脚本的影响。

---

## 7.1 CI 发布工作流的同根问题（根因修复，2026-06-27）

调查本次故障修复时发现，`.github/workflows/release.yml` 的 Build binary 步骤（原行 110-122）在构建全部 4 个 release 矩阵二进制（linux-amd64 / linux-arm64 / windows-amd64 / windows-amd64-lite）时，`TAGS` 变量只会在 `embed_ffmpeg=true` 时叠加 `-tags embed_ffmpeg`，**从未包含 `embedded_web`**——尽管上一步已把前端拷到 `cmd/hikami/webdist/`。

也就是说：**此前每个 tag release 发布的二进制前端都是空的**，症状与本文记录的本地事故完全一致，只是发生在分发层面、影响所有用户。

修复：将 `TAGS` 初值从空改为 `-tags embedded_web`，并在 `embed_ffmpeg=true` 时改为 `-tags embed_ffmpeg,embedded_web`，保证无论 ffmpeg 是否嵌入，前端始终嵌入。

```yaml
# 修复前
TAGS=""
if [ "${{ matrix.embed_ffmpeg }}" = "true" ]; then
  TAGS="-tags embed_ffmpeg"          # 漏掉 embedded_web
fi

# 修复后
TAGS="-tags embedded_web"            # 始终嵌入前端
if [ "${{ matrix.embed_ffmpeg }}" = "true" ]; then
  TAGS="-tags embed_ffmpeg,embedded_web"
fi
```

> 注：本次修复为只读 git 历史核实，未推送。`make build-go-api`（产出 `./hikami-api`）刻意不嵌入前端，是 ISS-1 纯 API 模式的设计，保持不动。

---

## 8. 修复记录（2026-06-27）

| # | 文件 | 改动 |
|---|---|---|
| 1 | `.github/workflows/release.yml` | Build binary 步骤 `TAGS` 始终含 `embedded_web`（根因修复） |
| 2 | `cmd/hikami/main.go` | `webFS == nil` 时打印醒目 WARN（预防措施 1） |
| 3 | `Makefile` | `build-go` 目标追加 `strings` 静态自检（预防措施 2） |
| 4 | 本文档 | 更新状态/§7/补 §7.1/§8 |

---

## 9. 相关文件索引

- `cmd/hikami/embed.go` — `embedded_web` 标签下的真实嵌入
- `cmd/hikami/embed_none.go` — 无标签时的空 FS 兜底
- `cmd/hikami/main.go` — webFS 的运行时判定与降级（含本次新增的降级 WARN）
- `internal/handler/server.go` — SPA 静态服务 / 兜底 index 的路由分支、兜底页 HTML
- `Makefile` — build / build-go（含本次新增的 strings 自检）/ build-go-api 三个目标
- `.github/workflows/release.yml` — release 矩阵构建（本次修复 embedded_web 标签）
