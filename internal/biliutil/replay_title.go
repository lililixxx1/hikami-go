package biliutil

import (
	"regexp"
	"strings"
)

// replayTitlePrefix 仅命中 B 站官方录播的「【直播回放】」「【录播】」「【回放】」「【直播录像】」前缀，
// 不误删普通方括号标题（如「【原神】版本前瞻」「【公告】停播通知」）。
// 前缀内允许任意空白，关键词后可跟冒号或直接收尾。codex r18 [P1]：旧实现 ^【[^】]*】 会误删所有方括号。
// 注意：中文右书名号 】 是普通字符不需转义（写 \】 反而会触发 invalid escape sequence）。
var replayTitlePrefix = regexp.MustCompile(`^【\s*(?:直播回放|直播录像|录播|回放)\s*[:：]?】\s*`)

// replayDateOrSession 匹配「纯日期」或「纯场次」或「日期+场次」组成的串，
// 用于判断剥除前缀后的剩余部分是否其实没有主题（只有时间信息）。
// 例：2026年06月29日22点场 / 2026-06-29 / 20260629 / 22点场 / 第3场。
var replayDateOrSession = regexp.MustCompile(`^(\d{4}[-年]?\d{1,2}[-月]?\d{1,2}日?)?(\d{1,2}点场?|第\d+场|[晚早午下午夜]+场|场)?$`)

// replayTitleSuffix 命中尾部的「时间/场次后缀」。B 站官方录播的主题与日期之间总有空白分隔，
// 因此后缀要求前导 \s+，避免误删主题末尾。例：『 2026年06月29日22点场』『 22点场』『 2026-06-29』。
var replayTitleSuffix = regexp.MustCompile(`\s+` +
	`(?:\d{4}[-年]?\d{1,2}[-月]?\d{1,2}日?(?:\d{1,2}点场?|第\d+场|[晚早午下午夜]+场|场)?` +
	`|\d{1,2}点场?` +
	`|第\d+场` +
	`|[晚早午下午夜]+场` +
	`)\s*$`)

// CleanReplayTitle 清洗 B 站官方录播视频标题，去掉「【直播回放】」等前缀和尾部时间/场次后缀，
// 保留主播设定的直播主题。例如：
//
//	【直播回放】晚上好 2026年06月29日22点场 -> 晚上好
//	【录播】杂谈 2026-06-29              -> 杂谈
//
// 仅当标题命中录播前缀（直播回放/录播/回放/直播录像）时才执行清洗；
// 普通视频标题（如「【原神】版本前瞻」「晚安 22点场」）不命中前缀则原样返回，
// 不越权改写。清洗后为空（前缀+后缀无主题）时同样回原标题。codex r18 [P1]。
func CleanReplayTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return title
	}
	// 必须命中录播前缀才清洗；普通方括号/无前缀标题原样返回。
	loc := replayTitlePrefix.FindStringIndex(title)
	if loc == nil {
		return title
	}
	withoutPrefix := strings.TrimSpace(title[loc[1]:])

	// 剥完前缀后若剩余部分整体就是日期/场次（无主题），视为无可提取主题，回原标题。
	if replayDateOrSession.MatchString(withoutPrefix) {
		return title
	}

	cleaned := strings.TrimSpace(replayTitleSuffix.ReplaceAllString(withoutPrefix, ""))
	if cleaned == "" {
		return title
	}
	return cleaned
}
