// Package runtimeconfig 持久化全局运行时配置覆盖（per-section JSON）。
//
// 设计背景：config.yaml 降级为只读基线（启动由 viper 加载一次），UI 通过设置面板
// 改动的全局配置按「配置段」存入本表的 data 列（JSON）。启动时 handler 链路在
// db.Migrate 之后调用 Load，再由 config.ApplyOverrides 用本表覆盖基线，得到最终
// 生效配置。覆盖优先级：runtime_settings > config.yaml > viper SetDefault。
//
// 本包只做 raw JSON 存取（含事务版 SaveTx + WithTx helper），不感知任何 config
// 类型——DTO 与 Apply 逻辑放在 internal/config，避免 config 反向依赖 db。
//
// 事务支持：密钥类 handler 需要把 secrets 表写入与本表写入放进同一 *sql.Tx（原子），
// 故提供 SaveTx 与 WithTx；非密钥 handler 也走 WithTx 以保证一致性。
package runtimeconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// nowRFC3339 返回本地时区的 RFC3339 时间字符串，与 sessions/tasks 表的时间字段
// （time.Now().Format(time.RFC3339)）保持一致。避免 SQLite datetime('now') 返回
// UTC，导致前端展示与其它表时间字段相差一个时区。
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

// Store 读写 runtime_settings 表。
type Store struct {
	db *sql.DB
}

// NewStore 基于已迁移的 *sql.DB 构造 Store。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB 暴露底层 *sql.DB，供 handler 用 WithTx(ctx, db, fn) 把 secrets 与 runtime_settings
// 的写入绑定为同一事务（r11 [High] 原子性）。secrets.Store 与本 Store 共享同一个 DB 实例。
func (s *Store) DB() *sql.DB { return s.db }

// Load 读取所有 section，返回 map[section]json.RawMessage。空表返回空 map（调用方
// 按零值处理 = 不覆盖基线）。DB 级错误（非空表）应视为启动 fatal。
func (s *Store) Load(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT section, data FROM runtime_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]json.RawMessage)
	for rows.Next() {
		var section string
		var data json.RawMessage
		if err := rows.Scan(&section, &data); err != nil {
			return nil, err
		}
		out[section] = data
	}
	return out, rows.Err()
}

// Save 写入（或覆盖）单个 section 的 data。普通路径；事务路径见 SaveTx / WithTx。
func (s *Store) Save(ctx context.Context, section string, data []byte) error {
	_, err := s.db.ExecContext(ctx, saveSQL, section, data, nowRFC3339())
	return err
}

// SaveTx 是 Save 的事务版，与 secrets 的 GetTx/SetTx/DeleteTx 共用同一 *sql.Tx，
// 保证「密钥写入 + 配置段写入」原子提交。
func (s *Store) SaveTx(ctx context.Context, tx *sql.Tx, section string, data []byte) error {
	_, err := tx.ExecContext(ctx, saveSQL, section, data, nowRFC3339())
	return err
}

const saveSQL = `INSERT INTO runtime_settings (section, data, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(section) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`

// WithTx 在一个事务内执行 fn，fn 接收 *sql.Tx 以便调用各 Store 的 *Tx 方法。
// fn 返回 nil 则提交，返回错误则回滚。用于把 secrets 与 runtime_settings 的写入
// 绑定为原子操作：commit 成功后调用方才更新进程 env / 内存 cfg。
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) (err error) {
	tx, beginErr := db.BeginTx(ctx, nil)
	if beginErr != nil {
		return beginErr
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	if fnErr := fn(tx); fnErr != nil {
		err = fnErr
		return
	}
	return nil
}

// SavePayload 帮助把一个已序列化的 section 负载规范化（仅用于防御性提示，
// 真正的 JSON 有效性由 DB 的 CHECK(json_valid(data)) 兜底）。
func SavePayload(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("runtime_settings data must not be empty")
	}
	return nil
}
