// cleanup-media-ready-stale:一次性运维脚本,清理「DB 标 media_ready 但本地音频缺失」的历史脏数据。
//
// 背景:2026-07-16 批量下载/normalize 任务标 succeeded 但本地无文件,导致 64 个 session
// 显示「音频已就绪」实际音频不存在。见 plans/plan-media-ready-consistency-2026-07-25.md Phase 3。
//
// 行为:扫描所有 status='media_ready' 的 session,对每个 os.Stat(audio.asr.mp3),
// 文件缺失的标记 last_error(保持 status=media_ready 不变,避免误导用户去 reset 但 raw 也没了)。
//
// 用法:
//   go run ./scripts/cleanup-media-ready-stale.go --db hikami.db --output-root ./hikami-go
//   go run ./scripts/cleanup-media-ready-stale.go --db hikami.db --output-root ./hikami-go --apply
//
// 默认 dry-run(只打印不写库);--apply 才真写。
// --output-root 为必填(脏数据创建于 2026-07-16 当时默认 hikami-go/,不做猜测)。
//
//go:build ignore

package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const audioMissingNote = "audio file missing on disk (historical data corruption 2026-07-16)"

func main() {
	dbPath := flag.String("db", "hikami.db", "SQLite 数据库路径")
	outputRoot := flag.String("output-root", "", "output_root 路径(必填,脏数据创建时默认为 hikami-go/)")
	apply := flag.Bool("apply", false, "真写库(默认 dry-run 只打印)")
	flag.Parse()

	if *outputRoot == "" {
		fmt.Fprintln(os.Stderr, "错误:--output-root 为必填参数。")
		fmt.Fprintln(os.Stderr, "脏数据创建于 2026-07-16(当时默认 hikami-go/),不做猜测。")
		fmt.Fprintln(os.Stderr, "示例:--output-root ./hikami-go 或 --output-root ./data")
		os.Exit(2)
	}

	info, err := os.Stat(*outputRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误:output-root 不存在: %v\n", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "错误:output-root 不是目录: %s\n", *outputRoot)
		os.Exit(1)
	}

	// DSN 拼接:dbPath 不得含 URI 特殊字符(? # 空格),否则解析异常。
	// 典型路径(hikami.db / ./hikami.db)无此问题,不做 url escape 以保持可读性。
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", *dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开数据库失败: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, slug, channel_id, title FROM sessions WHERE status = 'media_ready' ORDER BY started_at DESC`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "查询 media_ready session 失败: %v\n", err)
		os.Exit(1)
	}

	type stale struct {
		id, slug, channel, title, audioPath string
	}
	var total, exists, missing int
	var stales []stale

	for rows.Next() {
		var id, slug, channel, title string
		if err := rows.Scan(&id, &slug, &channel, &title); err != nil {
			fmt.Fprintf(os.Stderr, "扫描行失败: %v\n", err)
			os.Exit(1)
		}
		total++
		audioPath := filepath.Join(*outputRoot, channel, slug, "asr", "audio.asr.mp3")
		if _, err := os.Stat(audioPath); err == nil {
			exists++
			continue
		}
		missing++
		stales = append(stales, stale{id, slug, channel, title, audioPath})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "迭代 session 行失败(可能得到不完整结果): %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== 扫描结果 ===\n")
	fmt.Printf("  output-root:       %s\n", *outputRoot)
	fmt.Printf("  media_ready 总数:  %d\n", total)
	fmt.Printf("  音频文件存在:      %d\n", exists)
	fmt.Printf("  音频文件缺失:      %d\n", missing)
	fmt.Println()

	if missing == 0 {
		fmt.Println("无需清理(所有 media_ready session 音频文件均存在)。")
		return
	}

	mode := "DRY-RUN(只打印不写库,加 --apply 真写)"
	if *apply {
		mode = "APPLY(将写入 last_error)"
	}
	fmt.Printf("模式: %s\n\n", mode)

	fmt.Println("缺失音频的 session:")
	for _, s := range stales {
		fmt.Printf("  - %s\n", s.id)
		fmt.Printf("      slug=%s channel=%s\n", s.slug, s.channel)
		fmt.Printf("      title=%s\n", s.title)
		fmt.Printf("      audio(expected)=%s\n", s.audioPath)
	}

	if !*apply {
		fmt.Println("\n这是 dry-run。确认无误后加 --apply 重新运行以写入 last_error。")
		return
	}

	now := time.Now().Format(time.RFC3339)
	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "开启事务失败: %v\n", err)
		os.Exit(1)
	}
	marked := 0
	for _, s := range stales {
		res, err := tx.Exec(
			`UPDATE sessions SET last_error = ?, updated_at = ? WHERE id = ? AND status = 'media_ready'`,
			audioMissingNote, now, s.id,
		)
		if err != nil {
			tx.Rollback()
			fmt.Fprintf(os.Stderr, "UPDATE %s 失败: %v\n", s.id, err)
			os.Exit(1)
		}
		n, _ := res.RowsAffected()
		marked += int(n)
	}
	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "提交事务失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n已标记 %d 个 session 的 last_error(保持 status=media_ready 不变)。\n", marked)
	fmt.Println("前端对这些 session 会显示 last_error 警告(若有 UI 展示路径)。")
}
