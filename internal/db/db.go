package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// WAL mode: 允许读写并发，避免 SQLITE_BUSY
	if _, err := database.Exec("PRAGMA journal_mode = WAL"); err != nil {
		database.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}
	// busy_timeout: 写冲突时等待最多 5 秒而非立即报错
	if _, err := database.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		database.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}
	if _, err := database.Exec("PRAGMA foreign_keys = ON"); err != nil {
		database.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	// SQLite 只允许一个写入连接，连接池设为 1 避免并发写入冲突
	database.SetMaxOpenConns(1)

	// 收紧 DB 主文件及 WAL/SHM 辅助文件权限为 0600，防止同机其他用户读取明文 secrets（ISS-7）。
	// 上方 PRAGMA journal_mode=WAL 已触发 -wal/-shm 创建，此处一并收紧。
	chmodDBFiles(path)
	return database, nil
}

// chmodDBFiles 将 DB 主文件及 WAL/SHM 辅助文件权限收紧为 0600。
// 辅助文件可能尚未创建，缺失时静默跳过；其余错误降级忽略，不阻断启动（依赖部署层目录权限兜底）。
func chmodDBFiles(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			// 权限收紧失败降级处理，不阻断数据库可用性
		}
	}
}
