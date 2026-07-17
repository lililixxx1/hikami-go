# 计划：ISSUE-001 ASR 成本估算单价修正

> **分支**：`fix/known-issues-2026-07-11`
> **创建日期**：2026-07-11
> **状态**：APPROVED (codex v2，已纳入反馈)

## 目标

修正 ASR 成本估算单价从错误的 ¥36/小时 改为实际阿里云百炼中国内地目录价 ¥0.792/小时（¥0.00022/秒），偏差从约 45 倍降至 0。

## 背景

### 问题

3 处硬编码 `36.0`（¥36/小时 = ¥0.01/秒），实际 fun-asr 中国内地目录价为 ¥0.00022/秒 ≈ ¥0.792/小时。

### Codex 调研结论

1. **方案 A 最安全**：仅改单价 + 提取常量，无 DB 迁移、无接口变更
2. **单价用 ¥0.792/小时**（¥0.00022/秒），不是 ¥0.90/小时。¥0.90 是从美元价格折算的近似值，¥0.792 是阿里云中国内地目录直定价
3. **无其他调用方依赖 36.0 值**：`asrCostPerHour` 是函数级 `const`，SQL 中的 `36.0` 是字面量
4. **测试**：现有测试只校验 JSON key 存在性，不校验金额数值，无需更新
5. **推荐方案 A**，方案 B（从 DashScope 返回读取真实计费时长）单独立项

### 涉及代码

| 文件 | 位置 | 当前值 | 目标值 |
|------|------|--------|--------|
| `internal/handler/server.go` | `:3940` `handleStatsOverview` | `const asrCostPerHour = 36.0` | `const asrCostPerHour = 0.792` |
| `internal/handler/server.go` | `:4078` `handleStatsCost` | `const asrCostPerHour = 36.0` | `const asrCostPerHour = 0.792` |
| `internal/session/session.go` | `:871,873` `GetDashboardStats` SQL | `asr_hours * 36.0` | 提取为 Go 层计算 |

### 影响分析

- **API 契约不变**：返回的 JSON key（`asr_cost_estimate`, `asr_cost`, `total_cost` 等）不变，仅数值改变
- **前端不需改动**：`DashboardSection.vue` 显示 `¥{{ row.asr_cost }}`，数值变化无影响
- **无 DB schema 变更**
- **无依赖链影响**

## 修改方案

### 步骤 1：提取共享 ASR 单价常量

在 `internal/handler/server.go` 顶部（import 块之后）新增包级常量：

```go
// asrCostPerHourCNY 是阿里云百炼 fun-asr 录音文件识别的中国内地目录价。
// 来源: https://help.aliyun.com/zh/model-studio/model-pricing
// ¥0.00022/秒 ≈ ¥0.792/小时。此为估算值，未扣除每月 36,000 秒免费额度。
const asrCostPerHourCNY = 0.792
```

### 步骤 2：`handleStatsOverview` 改用共享常量

`internal/handler/server.go:3939-3940`：
```go
// 删除:
// Cost estimate: DashScope ASR ~¥0.01/sec = ¥36/hour
// const asrCostPerHour = 36.0
// 改为:
// ASR 成本估算：fun-asr 中国内地目录价 ¥0.792/小时（¥0.00022/秒）
```

`asrCostPerHour` 引用全部改为 `asrCostPerHourCNY`。

### 步骤 3：`handleStatsCost` 改用共享常量

`internal/handler/server.go:4076-4078`：
```go
// 删除:
// Rough cost estimates
// DashScope ASR: ~¥0.01/second = ¥36/hour
// const asrCostPerHour = 36.0
// 改为:
// ASR 成本估算：fun-asr 中国内地目录价 ¥0.792/小时
```

`asrCostPerHour` 引用全部改为 `asrCostPerHourCNY`。

### 步骤 4：`GetDashboardStats` SQL 改为 Go 层计算

`internal/session/session.go:868-877` 的 SQL 目前在查询中直接乘 `36.0`。改为 SQL 只返回 `asr_hours` 和 `recap_count`，成本在 Go 层用常量计算。

```go
// SQL 改为：
SELECT
    month,
    asr_hours,
    recap_count
FROM monthly
ORDER BY month DESC
LIMIT 12

// Go 层计算：
const asrCostPerHourCNY = 0.792
for rows.Next() {
    var item DashboardCost
    if err := rows.Scan(&item.Month, &item.ASRHours, &item.RecapCount); err != nil {
        return nil, err
    }
    item.ASRCost = item.ASRHours * asrCostPerHourCNY
    item.AICost = float64(item.RecapCount) * 0.1
    item.TotalCost = item.ASRCost + item.AICost
    data.CostTrend = append(data.CostTrend, item)
}
```

**注意**：`DashboardCost` 结构体目前没有 `RecapCount` 字段。需要新增：
```go
type DashboardCost struct {
    Month      string  `json:"month"`
    ASRHours   float64 `json:"asr_hours"`
    ASRCost    float64 `json:"asr_cost"`
    AICost     float64 `json:"ai_cost"`
    TotalCost  float64 `json:"total_cost"`
    RecapCount int     `json:"-"` // 内部字段，不暴露给前端
}
```

用 `json:"-"` 标记避免改变前端 JSON 契约。

### 步骤 5：新增回归测试

在 `internal/handler/server_test.go` 新增 `TestStatsDashboardCostCalculation`，创建一条 `asr_done` 状态的 session（1 小时），断言 `asr_cost ≈ 0.792`（容差比较）。

```go
func TestStatsDashboardCostCalculation(t *testing.T) {
    server := newTestServer(t)

    // 创建频道 + 一条 asr_done 状态、1 小时时长的 session
    if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
        ID: "test", Name: "Test", UID: 1, Enabled: true,
    }); err != nil {
        t.Fatalf("create channel: %v", err)
    }
    if _, err := server.sessions.CreateLive(context.Background(), session.CreateLiveInput{
        ChannelID: "test", Title: "Test", RoomID: 1,
        StartedAt: time.Date(2026, 7, 11, 12, 0, 0, 0, time.Local),
    }); err != nil {
        t.Fatalf("create session: %v", err)
    }
    // 更新状态为 asr_done + ended_at（1 小时后）
    // ... 用 store 方法或直接 SQL

    // 请求 dashboard
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    req := httptest.NewRequest(http.MethodGet, "/api/stats/dashboard", nil).WithContext(ctx)
    rec := httptest.NewRecorder()
    server.Router().ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
    }

    var data session.DashboardData
    if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }

    if len(data.CostTrend) == 0 {
        t.Fatal("expected cost trend entries")
    }
    // asr_hours 应为 1.0，asr_cost 应为 0.792
    row := data.CostTrend[0]
    if math.Abs(row.ASRHours-1.0) > 0.01 {
        t.Errorf("asr_hours = %v, want ~1.0", row.ASRHours)
    }
    if math.Abs(row.ASRCost-0.792) > 0.01 {
        t.Errorf("asr_cost = %v, want ~0.792", row.ASRCost)
    }
    expectedTotal := row.ASRCost + row.AICost
    if math.Abs(row.TotalCost-expectedTotal) > 0.01 {
        t.Errorf("total_cost = %v, want ~%v", row.TotalCost, expectedTotal)
    }
}
```

## 测试验证

1. `go test ./internal/handler/...` — 现有测试 + 新增成本测试
2. `go test ./internal/session/...` — 现有测试（无新增，但验证 SQL 改动不破坏）
3. `go build -tags embedded_web -o ./hikami ./cmd/hikami` — 编译通过

## 风险评估

- **低风险**：仅改常量值 + 提取常量，无逻辑变更
- **API 兼容**：JSON key 不变，仅数值变化
- **回滚简单**：恢复 3 处 `36.0` 即可
