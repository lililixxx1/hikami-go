package live_record

import (
	"context"
	"time"
)

const TaskType = "live_record"

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
