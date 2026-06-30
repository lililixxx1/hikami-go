package biliutil

import "testing"

func TestCleanReplayTitle(t *testing.T) {
	cases := map[string]string{
		// 本次目标视频：29 号晚上官方录播
		"【直播回放】晚上好 2026年06月29日22点场": "晚上好",
		// 前缀变体
		"【录播】杂谈 2026-06-29":  "杂谈",
		"【回放】歌回":             "歌回",
		"【直播录像】打游戏 20260629": "打游戏",
		// 仅日期后缀、无场次
		"【直播回放】闲聊 2026年6月9日": "闲聊",
		// codex r18 [P1]：无录播前缀的标题一律原样返回，不越权剥后缀。
		"晚安 22点场": "晚安 22点场",
		// 无前缀无后缀的主题，应原样返回
		"晚上好":    "晚上好",
		"一些普通标题": "一些普通标题",
		// BV 号（无前缀后缀可清洗），原样返回——下游负责用它兜底
		"BV1UFKX6yEN2": "BV1UFKX6yEN2",
		// 前后空白
		"  【直播回放】唱歌 2026-06-29  ": "唱歌",
		// 只有前缀和后缀、无主题 → 返回原标题，避免清空
		"【直播回放】2026年06月29日22点场": "【直播回放】2026年06月29日22点场",
		// 空字符串
		"": "",
		// codex r18 [P1] 回归：普通方括号标题【不是】回放前缀，前缀不得误删。
		"【原神】版本前瞻 2026-06-29": "【原神】版本前瞻 2026-06-29",
		"【公告】停播通知":            "【公告】停播通知",
		// codex r18 [P1] 回归：无回放前缀但含日期后缀 → 原样返回（不越权剥后缀）。
		"普通杂谈 2026-06-29": "普通杂谈 2026-06-29",
		// codex r18 [P1] 回归：带冒号的回放前缀变体。
		"【直播回放:】闲聊 2026-06-29": "闲聊",
	}
	for input, want := range cases {
		got := CleanReplayTitle(input)
		if got != want {
			t.Errorf("CleanReplayTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

// codex r18 [P1] 回归：清洗必须是幂等的（对已清洗结果再清洗不变）。
func TestCleanReplayTitle_Idempotent(t *testing.T) {
	for _, input := range []string{
		"【直播回放】晚上好 2026年06月29日22点场",
		"【原神】版本前瞻 2026-06-29",
		"晚上好",
	} {
		once := CleanReplayTitle(input)
		twice := CleanReplayTitle(once)
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", input, once, twice)
		}
	}
}
