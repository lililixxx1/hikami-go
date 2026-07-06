package live_record

import (
	"context"
	"errors"
	"time"
)

const TaskType = "live_record"

// ErrRiskControl352 表示 B 站返回 -352 风控(且刷新密钥/buvid 后重试一次仍 -352)。
// checkOne 用 errors.Is 识别它,触发频道级冷却(阶梯 5/10/20m),避免调度器继续高频打被风控的端点。
// 非 -352 的错误(网络抖动、其它业务码)不会包装成本错误,不会触发冷却。
var ErrRiskControl352 = errors.New("bilibili -352 risk control")

// ErrHTTPRiskControl 表示 B 站在 HTTP 网关层返回风控状态码(412/403/429)(异常 P2),
// 区别于业务码 -352(200 OK body 内 code 字段)。两者共用同一套频道级冷却(见 isRiskControlError)。
// checkOne/Check/Start/preflight/decideAfterRecord/selectStream 调用方用 errors.Is 识别它。
var ErrHTTPRiskControl = errors.New("bilibili http-layer risk control")

// ErrZeroByteStalled 表示录制文件持续 0 字节(ffmpeg 起来但 selectStream 拿不到有效流),
// 健康检测判定为僵尸录制(异常 #11)。触发取消并走**失败**路径(不送 normalize,避免空音频污染回顾)。
var ErrZeroByteStalled = errors.New("recording stalled: zero-byte output")

// ErrRecordingNotGrowing 表示录制文件曾增长后连续停滞(failCount>=3),健康检测判定为僵尸(异常 #11)。
// 仅用于 checkOneChannelHealth 触发取消 + HandleTask 收尾分支判断(有已录音频走成功收尾保留)。
// **不**进 isRiskControlError,不触发频道冷却(与 ErrRiskControl352/ErrHTTPRiskControl 严格区分)。
var ErrRecordingNotGrowing = errors.New("recording unhealthy: file not growing")

type Status struct {
	ChannelID string    `json:"channel_id"`
	RoomID    int64     `json:"room_id"`
	Live      bool      `json:"live"`
	Title     string    `json:"title"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Recording bool      `json:"recording"`
	SessionID string    `json:"session_id,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type LiveInfo struct {
	RoomID    int64
	Live      bool
	Title     string
	Cover     string // 直播间封面 URL（room_info.cover），用作专栏封面
	StartedAt time.Time
}

type StreamInfo struct {
	URL       string
	AudioOnly bool
	Headers   map[string]string
}

type BiliClient interface {
	CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error)
	GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error)
}

type AudioRecorder interface {
	Record(ctx context.Context, stream StreamInfo, outputPath string) error
}

type DanmakuRecorder interface {
	Record(ctx context.Context, roomID int64, outputPath string, cookieHeader string, uid int64) error
}
