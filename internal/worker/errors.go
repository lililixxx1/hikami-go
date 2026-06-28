package worker

import (
	"regexp"
)

// FriendlyError 友好错误信息
type FriendlyError struct {
	Message    string `json:"message"`    // 中文友好消息
	Suggestion string `json:"suggestion"` // 建议操作
}

// errorMapping 错误模式映射
type errorMapping struct {
	pattern    *regexp.Regexp
	message    string
	suggestion string
}

// friendlyErrorMappings 错误映射表
var friendlyErrorMappings = []errorMapping{
	// ffmpeg 相关
	{compilePattern(`ffmpeg exited with code 1`), "音频处理失败", "请检查源文件是否损坏，或重试任务"},
	{compilePattern(`ffmpeg.*not found`), "音频处理工具未安装", "请安装 ffmpeg（运行 ffmpeg -h 检查）"},
	{compilePattern(`no such file or directory`), "文件不存在", "请检查源文件路径是否正确"},
	{compilePattern(`permission denied`), "权限不足", "请检查文件和目录权限"},

	// 网络相关
	{compilePattern(`dial tcp.*connection refused`), "无法连接到服务器", "请检查网络连接是否正常"},
	{compilePattern(`connection reset by peer`), "网络连接被重置", "请检查网络稳定性，稍后重试"},
	{compilePattern(`i/o timeout`), "网络请求超时", "请检查网络连接，或稍后重试"},
	{compilePattern(`TLS handshake timeout`), "安全连接超时", "请检查网络连接"},
	{compilePattern(`no such host`), "DNS 解析失败", "请检查网络连接和 DNS 设置"},

	// 超时相关
	{compilePattern(`context deadline exceeded`), "操作超时", "任务处理时间过长，请稍后重试"},
	{compilePattern(`context canceled`), "任务被取消", "任务已取消执行"},

	// Cookie/认证相关
	{compilePattern(`cookie.*expired`), "Cookie 已过期", "请到主播设置更新 Cookie"},
	{compilePattern(`SESSDATA.*invalid`), "登录状态无效", "请重新登录获取 Cookie"},
	{compilePattern(`not login`), "未登录", "请先完成 B 站登录"},

	// API 限流相关
	{compilePattern(`rate limit`), "请求过于频繁", "服务端限流中，请稍后重试"},
	{compilePattern(`too many requests`), "请求过多", "请稍后重试"},
	{compilePattern(`429`), "请求频率超限", "请等待片刻后重试"},

	// ASR/DashScope 相关
	{compilePattern(`insufficient quota`), "API 配额不足", "请检查 ASR 服务余额"},
	{compilePattern(`InvalidApiKey`), "API Key 无效", "请检查 API Key 配置是否正确"},
	{compilePattern(`UserNotActivated`), "服务未开通", "请先开通 DashScope 服务"},
	{compilePattern(`file too large`), "音频文件过大", "请检查音频文件大小限制"},

	// 发布相关
	{compilePattern(`内容审核.*拒绝`), "内容审核未通过", "请修改回顾内容后重新发布"},
	{compilePattern(`稿件.*重复`), "稿件已存在", "该内容可能已经发布过"},
	{compilePattern(`发布频率限制`), "发布过于频繁", "请稍后再发布"},

	// yt-dlp 相关
	{compilePattern(`yt-dlp.*not found`), "下载工具未安装", "请安装 yt-dlp"},
	{compilePattern(`Video unavailable`), "视频不可用", "该回放可能已被删除或设为私密"},
	{compilePattern(`Sign in to confirm`), "需要登录才能下载", "请配置下载用 Cookie"},
	{compilePattern(`HTTP Error 403`), "访问被拒绝", "请检查 Cookie 是否有效"},

	// rclone 相关
	{compilePattern(`rclone.*not found`), "上传工具未安装", "请安装 rclone"},
	{compilePattern(`transfer failed`), "文件传输失败", "请检查 WebDAV 配置和网络"},

	// 通用
	{compilePattern(`disk.*full|no space left`), "磁盘空间不足", "请清理磁盘空间或调整输出目录"},
	{compilePattern(`signal: killed`), "进程被终止", "可能是内存不足导致，请检查系统资源"},
	{compilePattern(`exit status 1`), "命令执行失败", "请查看详细错误日志"},
}

func compilePattern(pattern string) *regexp.Regexp {
	return regexp.MustCompile("(?i)" + pattern)
}

// GetFriendlyError 根据原始错误返回友好错误信息
func GetFriendlyError(taskType string, rawError string) FriendlyError {
	for _, mapping := range friendlyErrorMappings {
		if mapping.pattern.MatchString(rawError) {
			return FriendlyError{
				Message:    mapping.message,
				Suggestion: mapping.suggestion,
			}
		}
	}

	// 默认友好消息，按任务类型分类
	switch taskType {
	case "download":
		return FriendlyError{Message: "下载失败", Suggestion: "请检查网络连接和 Cookie 配置，稍后重试"}
	case "normalize":
		return FriendlyError{Message: "音频标准化失败", Suggestion: "请检查源文件是否完整"}
	case "asr":
		return FriendlyError{Message: "语音转写失败", Suggestion: "请检查 ASR 服务状态和余额"}
	case "asr_poll":
		return FriendlyError{Message: "转写结果获取失败", Suggestion: "请稍后重试"}
	case "recap":
		return FriendlyError{Message: "回顾生成失败", Suggestion: "请检查 AI 服务状态和配置"}
	case "upload":
		return FriendlyError{Message: "上传失败", Suggestion: "请检查 WebDAV 配置和网络"}
	case "publish":
		return FriendlyError{Message: "发布失败", Suggestion: "请检查 Cookie 和发布配置"}
	case "live_record":
		return FriendlyError{Message: "直播录制失败", Suggestion: "请检查直播状态和网络连接"}
	default:
		return FriendlyError{Message: "任务执行失败", Suggestion: "请查看错误详情，或重试任务"}
	}
}
