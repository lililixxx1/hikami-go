# HTML 模板(V10)vs 后端 API 差异分析

> 为 Vue 3 前端全页面重写服务。逐页对照 V10 模板(`~/文档/V10 Hikami-Go 全页面重设计.html`)与 `docs/api/openapi.yaml`(121 个端点),标注差异与处理决策。
>
> 生成时间:2026-07-07。基于 openapi.yaml tag 分布:场次 22 / 术语表 28 / 配置 15 / B站账号 14 / 运行时 10 / 回顾模板 10 / 频道 8 / 任务 7 / 系统 4 / 密钥 2 / WebSocket 1。

## 状态图例

| 标记 | 含义 |
|------|------|
| ✅ | 后端有对应端点,字段匹配 |
| ❌ | 后端无(需新增端点或砍掉模板元素) |
| ⚠️ | 后端有但模板没用(多余端点) |
| 📝 | 字段名/类型差异(需前端映射或后端补字段) |

---

## 页 1:首页(page-home)

模板区间:行 1057-1319。

| 模板元素 | 数据需求 | 对应端点 | 状态 | 决策 |
|---|---|---|---|---|
| 直播状态卡(title/badge/duration) | 全频道直播状态 | GET /api/live/status | ✅ | 字段齐全(title/live/recording/started_at) |
| 直播卡 duration 计时 | 录制已时长 | GET /api/live/status.started_at | 📝 | 后端无 elapsed 字段,**前端用 now - started_at 计算** |
| 开始/停止录制按钮 | 手动启停 | POST /api/live/{id}/record/start\|stop | ✅ | — |
| disk-indicator 磁盘占用 | 输出目录磁盘 | GET /api/health/runtime.disk_usage | ✅ | used_percent/free_gb 一致 |
| 需要注意·录制失败计数 | 失败任务数 | GET /api/stats/overview.task_summary.failed | ✅ | — |
| 需要注意·Cookie 过期告警 | cookie 告警 | GET /api/health/runtime.cookie_warnings | ✅ | channel_name/days_left/is_expired 一致 |
| 最近回顾卡 | 最近场次 | GET /api/sessions | ✅ | 前端按 published_at 排序截断 |
| 回顾卡 pill 状态 | 场次状态中文 | GET /api/sessions.status | 📝 | 后端英文枚举,**前端做中文映射表** |
| 回顾卡·主播名 | 主播显示名 | GET /api/sessions(仅 channel_id) | 📝 | **Session 无 channel_name 字段**,见下方"核心缺口" |
| 运行中任务表 | 运行中任务 | GET /api/tasks | ✅ | type/channel_id/progress 一致 |
| 任务取消按钮 | 取消任务 | POST /api/tasks/{id}/cancel | ✅ | — |
| 统计·本月录制场次 | 当月场次 | GET /api/stats/dashboard.sessions_by_month | 📝 | overview.total_sessions 是全量;**用 dashboard.sessions_by_month 取最近月** |
| 统计·关注主播总数 | 频道总数 | GET /api/channels.items.length | 📝 | 无专用 count 字段,**前端数 list 长度** |
| 统计·已发布回顾 | 发布计数 | GET /api/stats/dashboard.publish_count | ✅ | — |
| 统计·录制成功率 | 成功率 | GET /api/stats/overview.task_summary | 📝 | **无现成 rate 字段,前端算 succeeded/(succeeded+failed)** |
| 月度场次趋势图 | 按月场次 | GET /api/stats/dashboard.sessions_by_month | ✅ | — |
| 主播录制排名 | 按频道排名 | GET /api/stats/dashboard.sessions_by_channel | ⚠️ | DashboardChannel 有 session_count/recap_count/publish_count,**无"总时长"字段**(模板列"38.5h"无数据源) |
| 费用趋势表 | 月度成本 | GET /api/stats/dashboard.cost_trend | ✅ | asr_hours/asr_cost 一致(ai_cost/total_cost 多余可选展示) |

## 页 2:我的主播(page-streamers)

模板区间:行 1321-1429(列表)+ 2350-2360(抽屉)+ 2467-2594(详情面板 JS)。

| 模板元素 | 数据需求 | 对应端点 | 状态 | 决策 |
|---|---|---|---|---|
| 主播卡片网格 | 频道列表 | GET /api/channels | ✅ | auto_* 字段映射 pills |
| 添加主播(识别 + 保存) | identify + create | POST /api/channels/identify + identify/save | 📝 | 模板缺识别表单 UI,需补 input + 预览 |
| 自动录制/转写/回顾/发布 toggle | auto_* 四态 | PUT /api/channels/{id} | ✅ | 注意 auto_recap 是 `*bool` 三态 |
| Cookie 状态点 | cookie 有效性 | GET /api/cookies/status | ✅ | — |
| 扫码登录按钮 | QR 三步 | POST /api/bili/login/qrcode + poll + save | ✅ | save 传 channel_id + usage |
| 删除 Cookie 按钮 | 清除频道 cookie | PUT /api/channels/{id}(cookie_file 置空) | 📝 | **无专用 DELETE cookie 端点**,用 PUT 置空 |
| 术语表 textarea | 主播级术语 | GET/POST /api/channels/{id}/glossary/entries | ✅ | textarea 需与结构化 entries 互转(或用 import/markdown) |
| 回顾模板 textarea | 主播级模板 | GET/PUT /api/channels/{id}/recap-template | 📝 | 模板是结构化(system_prompt/user_format/...),textarea 简化了模型 |
| 下载账号绑定 | download_account_id | GET /api/cookie-accounts + PUT /api/channels/{id} | ✅ | 详情面板缺绑定下拉,需补 UI |
| 复制配置按钮 | 跨频道复制 | POST /api/channels/{id}/copy-config | ❌ | **模板无此按钮**,后端已实现,建议加 |
| 最近场次列表(详情面板) | 按频道的场次 | GET /api/sessions?channel_id= | ❌ | **listSessions 不支持 channel_id 过滤**,见下方"核心缺口" |
| 高级配置(码率/分片/回顾长度) | 频道级编码参数 | 无 | ❌ | Channel schema 无这些字段,**移除 UI 或扩展 schema** |

## 页 3:回顾(page-reviews)

模板区间:行 1431-1542(表格)+ 2338-2447(抽屉)。

| 模板元素 | 数据需求 | 对应端点 | 状态 | 决策 |
|---|---|---|---|---|
| 发现回放(一步) | 一步式发现 | POST /api/sessions/discover | ✅ | — |
| 两步式 preview/execute | 预览+勾选+执行 | POST discover/preview + discover/execute | 📝 | **端点就绪,模板缺勾选预览 UI** |
| 导入文件 | multipart 导入 | POST /api/sessions/import | ✅ | — |
| 下载链接 | 按 URL 建场次 | POST /api/sessions/download-by-url | ✅ | — |
| 清空失败 | 删失败场次 | DELETE /api/sessions/failed | ✅ | — |
| 子标签·录播/回放 | 按 source 筛选 | GET /api/sessions | 📝 | **无 source 参数**,前端筛选或后端补参 |
| 搜索框 | 按标题搜索 | 无 | ❌ | **后端无 search 参数**,前端全量过滤 |
| 状态筛选 | 按 status 筛选 | GET /api/sessions?status= | ✅ | — |
| 主播筛选 | 按 channel 筛选 | 无 | ❌ | **后端无 channel_id 参数** |
| 场次列表 | 场次列表 | GET /api/sessions | ✅ | — |
| 进度列实时刷新 | 任务进度 | GET /api/tasks + WebSocket /ws | 📝 | **模板无 WS 连接代码**,进度列静态,需接入 /ws |
| 取消/重试/提交ASR/生成回顾 | 任务操作 | POST /api/tasks/{id}/cancel\|retry + asr/submit + recap/generate | ✅ | — |
| 阅读回顾(抽屉) | 场次详情+回顾 | GET /api/sessions/{sid} + /api/sessions/{sid}/recap | ✅ | — |
| 编辑回顾内容 | 覆盖 md | PUT /api/sessions/{sid}/recap/content | ⚠️ | 端点就绪,模板抽屉只读,**可补编辑 UI** |
| 自定义时间段回顾 | 局部生成 | POST /api/sessions/{sid}/recap-partial | ✅ | 时间输入换算成秒 |
| 重新生成回顾 | 覆盖重生成 | POST /api/sessions/{sid}/recap/regenerate | ✅ | — |
| 发布到 B站专栏 | 发布 | POST /api/sessions/{sid}/publish | ✅ | — |
| 建议术语 pills + 添加 | 候选展示+审批 | GET /api/sessions/{sid}/recap.suggested_terms + POST glossary/discover | 📝 | **候选审批在 global/channel 级**,session 级 discover 仅返回 {ok},候选读取链路需走 channel 级 |
| 上传/fetch/归档按钮 | WebDAV 操作 | POST upload/fetch/archive | ❌ | **端点就绪,模板抽屉无这 3 个按钮**,需补 |
| 导出 Markdown | 导出回顾 md | 无 | ❌ | **后端无导出端点**,前端用 GET recap 内容自行下载 |
| 预览片段 | 片段预览 | 无 | ❌ | **后端无片段预览端点**(recap-partial 是生成非预览) |

## 页 4:设置(page-settings)

模板区间:行 1545-2336。

### 4a. 配置表单(6 组)

| 配置组 | 模板字段 | 对应端点 | 状态 | 决策 |
|---|---|---|---|---|
| DashScope | 模型/语言/密钥三态/分离/人数/词表ID/ASR URL/Tasks URL | GET/PUT /api/config/dashscope | ✅ | 字段全覆盖 |
| ASR S3 | endpoint/bucket/region/access_key 三态/公网URL/Path Style | GET/PUT /api/config/asr-s3 | ✅ | — |
| Recap AI | enabled/base_url/model/密钥三态/provider/续写/超时 | GET/PUT /api/config/recap | 📝 | **recap 密钥缺"清除 checkbox"**(其余 3 组都有),需补 UI |
| WebDAV | url/username/密码三态/base_path/remote/env | GET/PUT /api/config/webdav | ✅ | — |
| Publish | enabled/可见范围/封面/声明/评论/定时/标签/分区/话题/文集 | GET/PUT /api/config/publish | 📝 | 话题/文集需额外端点(见下) |
| Archive | 自动归档/清理范围 | GET/PUT /api/config/archive | ✅ | — |

### 4b. 账号与备份

| 模板元素 | 数据需求 | 对应端点 | 状态 | 决策 |
|---|---|---|---|---|
| B站账号列表 + 扫码登录 | cookie-accounts + QR | GET /api/cookie-accounts + POST /api/bili/login/qrcode* | ✅ | — |
| 默认下载/发布 pill | 排他设默认 | POST /api/cookie-accounts/{id}/default-* | ✅ | — |
| 删除账号 | 删除 | DELETE /api/cookie-accounts/{id} | ✅ | — |
| 导出配置 | 全量备份 | GET /api/config/export | ✅ | — |
| 导入配置 | 导入 | POST /api/config/import?strategy= | ✅ | — |

### 4c. 术语表 / 模板(本页全局级)

| 模板元素 | 数据需求 | 对应端点 | 状态 | 决策 |
|---|---|---|---|---|
| 全局术语条目列表 + 增删改 | 全局 glossary CRUD | GET/POST /api/glossary/entries + DELETE {eid} | ✅ | 模板仅静态展示,需补编辑交互 |
| 术语批量/导入导出/备注/候选审批 | 高级操作 | batch-*/import/export/note/candidates | ⚠️ | **端点就绪,模板无对应 UI**,可按需补 |
| 全局回顾模板 textarea | 模板编辑 | GET/PUT /api/recap/templates | ✅ | — |
| 模板预设选择器 | 预设列表 | GET /api/recap/presets | ⚠️ | **端点就绪,模板无 preset 选择 UI** |
| 模板导入导出 | 备份 | /api/recap/templates/export\|import | ⚠️ | **端点就绪,模板无按钮** |

### 4d. 其他

| 模板元素 | 数据需求 | 对应端点 | 状态 | 决策 |
|---|---|---|---|---|
| Pipeline 状态条(4 段) | 配置健康汇总 | 无专用端点 | 📝 | 复用 /api/health/runtime 派生 |
| 外部工具表(ffmpeg/yt-dlp/rclone) | 工具状态 | GET /api/health/runtime.tools | ✅ | tools map 含这 4 个工具的 available/path |
| B站话题搜索(publish 话题 select) | 话题列表 | GET /api/bili/topics/search | ✅ | **路由存在**(server.go:367),agent 误判 |
| B站文集列表(publish 文集 select) | 文集列表 | GET /api/bili/series/list | ✅ | **路由存在**(server.go:368),agent 误判 |

---

## 汇总

### ❌ 缺失端点/字段(需后端补或砍模板)

| 缺口 | 影响页 | 优先级 | 建议 |
|---|---|---|---|
| **Session/Task 无 channel_name 字段** | 首页/回顾 | **P0** | list 响应 join 返回 channel_name,或前端维护 channel 映射表(避免 N 次请求) |
| **listSessions 不支持 channel_id/source/search 过滤** | 回顾/主播 | P1 | 后端补查询参数 |
| **DashboardChannel 无"总时长"字段** | 首页排名 | P2 | 后端补 duration_hours 或砍模板列 |
| **导出回顾 Markdown 端点** | 回顾抽屉 | P2 | 前端用 GET recap 内容自行下载(无需后端) |
| **回顾片段预览端点** | 回顾抽屉 | P3 | 砍模板元素(语义与 recap-partial 重复) |
| **频道级高级录制参数(码率/分片/回顾长度)** | 主播详情 | P3 | 砍模板元素或扩展 Channel schema |
| **删除频道 cookie 专用端点** | 主播详情 | P3 | 用 PUT /api/channels/{id} 置空 cookie_file |

### ⚠️ 多余端点(后端有但模板没用到,前端重写时可按需启用)

- 术语表:batch-delete/toggle、import/export、note、candidates 审批
- 回顾模板:presets 选择器、import/export
- 场次:upload/fetch/archive 按钮(模板抽屉漏了,前端重写务必补上)
- recap/content 编辑(模板只读,可补编辑 UI)

### 📝 字段映射差异(前端处理)

| 差异 | 处理 |
|---|---|
| Session/Task status 英文枚举 → 中文 | 前端维护 status→中文映射表 |
| Task type 英文 → 中文 | 同上(download→下载,asr→转写,recap→回顾...) |
| 直播卡 duration | 前端用 now - started_at 计算 |
| 统计·本月场次 | 用 dashboard.sessions_by_month 取最近月(非 overview.total) |
| 统计·录制成功率 | 前端算 succeeded/(succeeded+failed) |
| Cookie expires_at 非标准格式 | 用 new Date() 容错解析 |
| **ResolvedTemplate 字段 PascalCase** | 前端访问用 SystemPrompt/UserFormat/FanName/ExtraVars(非 snake_case) |
| recap/auto_recap 三态 | 字段缺席=nil=false;显式传 true/false 设置 |

### 🔌 WebSocket 接入(模板缺失,前端重写必须补)

模板 JS **无 WebSocket 连接代码**,进度列为静态假数据。前端重写必须:
1. 连接 `ws://127.0.0.1:6334/ws`(无 token、无 /api 前缀)
2. 监听 `task_progress` 事件(TaskProgressEvent schema)
3. 按 task_id 匹配更新进度条
4. WS 断开后降级轮询 GET /api/tasks

---

## 结论

V10 模板与后端 API **整体契合度高**:6 组配置表单、频道/场次/任务 CRUD、QR 登录、术语/模板管理、统计 Dashboard 等核心功能端点齐全。

**前端重写的关键阻塞点**(P0/P1):
1. **Session/Task 需补 channel_name**(否则首页/回顾页每张卡都要二次查频道列表)
2. **listSessions 需支持 channel_id/source/search 过滤**(否则回顾页筛选无法实现)
3. **WebSocket 必须接入**(模板完全缺失,进度刷新依赖它)

其余差异均为前端映射工作(英文枚举→中文、字段计算)或可选启用(多余端点),不阻塞重写。
