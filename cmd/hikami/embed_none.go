//go:build !embedded_web

package main

import "embed"

// webDistFS 在未启用 embedded_web 构建标签时为空 embed.FS。
// cmd/hikami/main.go 的 fs.Stat(webDistFS, "webdist") 降级逻辑会据此跳过前端嵌入，
// 使 `go build ./cmd/hikami`（不带 tag）可作为纯 API 服务独立编译（ISS-1）。
var webDistFS embed.FS
