package biliutil

import (
	"strings"
	"testing"
)

func TestReplayDateFromTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
		ok    bool
	}{
		{name: "compact archive date", title: "【随一Suiii】首播歌回全程录播20210509", want: "2021-05-09", ok: true},
		{name: "surrounded by text", title: "录播 20231231 完整版", want: "2023-12-31", ok: true},
		{name: "official replay with hour", title: "【直播回放】来接下班咯 2026年08月04日18点场", want: "2026-08-04T18:00:00", ok: true},
		{name: "official replay without hour", title: "【直播回放】八月了啧 2026年08月01日", want: "2026-08-01T00:00:00", ok: true},
		{name: "hyphenated date", title: "随一录播 2026-08-04", want: "2026-08-04T00:00:00", ok: true},
		{name: "invalid calendar date", title: "录播20210230", ok: false},
		{name: "embedded in longer number", title: "编号1202105099", ok: false},
		{name: "no date", title: "晚上好", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ReplayDateFromTitle(tt.title)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (date=%v)", ok, tt.ok, got)
			}
			format := "2006-01-02"
			if strings.Contains(tt.want, "T") {
				format = "2006-01-02T15:04:05"
			}
			if ok && got.Format(format) != tt.want {
				t.Fatalf("date = %s, want %s", got.Format(format), tt.want)
			}
		})
	}
}
