package worker

import (
	"fmt"
	"time"
)

// DeferredError 请求 worker pool 把 running 任务退回 pending，并在 Delay 后重新
// 入队。与普通 handler 错误不同，延期不会增加 attempt、把 session 标为失败或发送
// 失败通知。
//
// 它适用于下载限速等本地调度约束：任务本身尚未失败，如果阻塞 worker goroutine
// 直到约束解除，会让无关任务类型得不到执行机会。
type DeferredError struct {
	Delay   time.Duration
	Message string
}

func (e *DeferredError) Error() string {
	if e == nil {
		return "task deferred"
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("task deferred for %s", e.Delay)
}

// Defer 返回一个用于安排非失败延期的 handler 错误。
func Defer(delay time.Duration, message string) error {
	if delay <= 0 {
		delay = time.Second
	}
	return &DeferredError{Delay: delay, Message: message}
}
