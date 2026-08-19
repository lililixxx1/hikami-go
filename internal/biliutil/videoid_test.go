package biliutil

import (
	"testing"
)

func TestExtractVideoID_BVFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.bilibili.com/video/BV1xx411c7mD":                          "BV1xx411c7mD",
		"https://www.bilibili.com/video/BV1xx411c7mD/?spm_id_from=333.999.0.0": "BV1xx411c7mD",
		"https://b23.tv/BV1xx411c7mD":                                          "BV1xx411c7mD",
		"BV1xx411c7mD":                                                         "BV1xx411c7mD",
		"  BV1xx411c7mD  ":                                                     "BV1xx411c7mD",
		"http://m.bilibili.com/video/BV1ab2c3d4e5?p=2":                         "BV1ab2c3d4e5",
	}
	for input, want := range cases {
		got := ExtractVideoID(input)
		if got != want {
			t.Errorf("ExtractVideoID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExtractVideoID_FallbackHash(t *testing.T) {
	// 非 B 站链接走兜底 hash：同一归一化 URL 得到相同 ID
	id1 := ExtractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	id2 := ExtractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ&utm_source=share")
	if id1 == "" {
		t.Fatal("expected non-empty fallback ID")
	}
	if id1 != id2 {
		t.Errorf("fallback ID not stable for equivalent URLs: %q vs %q", id1, id2)
	}
}

func TestExtractVideoID_Empty(t *testing.T) {
	if got := ExtractVideoID("   "); got != "" {
		t.Errorf("ExtractVideoID(empty) = %q, want empty", got)
	}
}

func TestExtractVideoPart(t *testing.T) {
	tests := []struct {
		url  string
		part int
		ok   bool
	}{
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=2", 2, true},
		{"//www.bilibili.com/video/BV1xx411c7mD?p=19", 19, true},
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=0", 0, false},
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=abc", 0, false},
		{"https://www.bilibili.com/video/BV1xx411c7mD", 0, false},
	}
	for _, tt := range tests {
		part, ok := ExtractVideoPart(tt.url)
		if part != tt.part || ok != tt.ok {
			t.Errorf("ExtractVideoPart(%q) = (%d, %v), want (%d, %v)", tt.url, part, ok, tt.part, tt.ok)
		}
	}
}

func TestExtractVideoSourceID(t *testing.T) {
	tests := map[string]string{
		"https://www.bilibili.com/video/BV1xx411c7mD":      "BV1xx411c7mD",
		"https://www.bilibili.com/video/BV1xx411c7mD?p=1":  "BV1xx411c7mD_p001",
		"https://www.bilibili.com/video/BV1xx411c7mD?p=19": "BV1xx411c7mD_p019",
	}
	for input, want := range tests {
		if got := ExtractVideoSourceID(input); got != want {
			t.Errorf("ExtractVideoSourceID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSourceIDWithPart(t *testing.T) {
	tests := []struct {
		name    string
		videoID string
		url     string
		want    string
	}{
		// yt-dlp 对多 P 合集的 entry.ID 剥掉 BV 前缀(实测 2026-08-19:BV1SW411P7Du
		// 的 entry.id 为 "1SW411P7Du")。BV-less ID + BV URL 必须锚定 URL 的 BV,
		// 与 download-by-url 的 ExtractVideoSourceID 去重键对齐,否则同一分 P
		// 经 discover 与 download-by-url 两条路径各建一场、重复下载。
		{"yt-dlp bv-less id with part anchors url bv", "1SW411P7Du", "https://www.bilibili.com/video/BV1SW411P7Du?p=3", "BV1SW411P7Du_p003"},
		{"yt-dlp bv-less id no part anchors url bv", "1SW411P7Du", "https://www.bilibili.com/video/BV1SW411P7Du", "BV1SW411P7Du"},
		// 列表器自定义 ID 且 URL 无 BV(ep/ss、非 B 站列表)保持原样,
		// 与历史 session 去重连续(L14 原语义)。
		{"custom id non-bv url stays as-is with part", "ep123456", "https://www.bilibili.com/bangumi/ep123456?p=2", "ep123456_p002"},
		{"custom id non-bv url stays as-is", "ep123456", "https://www.bilibili.com/bangumi/ep123456", "ep123456"},
		// 不完整 BV 前缀形态(不匹配 BV 正则)同样锚定 URL 的 BV。
		{"malformed bv prefix anchors url bv", "BV1", "https://www.bilibili.com/video/BV1xx411c7mD?p=2", "BV1xx411c7mD_p002"},
		{"valid bv with part matches ExtractVideoSourceID", "BV1xx411c7mD", "https://www.bilibili.com/video/BV1xx411c7mD?p=1", "BV1xx411c7mD_p001"},
		{"empty id falls back to url", "", "https://www.bilibili.com/video/BV1xx411c7mD?p=3", "BV1xx411c7mD_p003"},
		{"invalid p ignored", "BV1xx411c7mD", "https://www.bilibili.com/video/BV1xx411c7mD?p=abc", "BV1xx411c7mD"},
	}
	for _, tt := range tests {
		if got := SourceIDWithPart(tt.videoID, tt.url); got != tt.want {
			t.Errorf("%s: SourceIDWithPart(%q, %q) = %q, want %q", tt.name, tt.videoID, tt.url, got, tt.want)
		}
	}
}

func TestNormalizeSourceURL_StripsTracking(t *testing.T) {
	in := "https://www.bilibili.com/video/BV1xx411c7mD/?spm_id_from=333.999.0.0&vd_source=abc&p=2#t=10"
	got := NormalizeSourceURL(in)
	if want := "https://www.bilibili.com/video/BV1xx411c7mD/?p=2"; got != want {
		t.Errorf("NormalizeSourceURL =\n %q\nwant\n %q", got, want)
	}
}

func TestNormalizeSourceURL_Idempotent(t *testing.T) {
	in := "https://www.bilibili.com/video/BV1xx411c7mD"
	once := NormalizeSourceURL(in)
	twice := NormalizeSourceURL(once)
	if once != twice {
		t.Errorf("normalize not idempotent: %q vs %q", once, twice)
	}
}

func TestNormalizeSourceURL_Empty(t *testing.T) {
	if got := NormalizeSourceURL("   "); got != "" {
		t.Errorf("NormalizeSourceURL(empty) = %q, want empty", got)
	}
}

func TestBiliCookie_NetscapeBytes(t *testing.T) {
	c := &BiliCookie{SESSDATA: "s1", BiliJct: "j1", DedeUserID: "u1"}
	out := string(c.NetscapeBytes())
	if !contains(out, "# Netscape HTTP Cookie File") {
		t.Errorf("missing Netscape header:\n%s", out)
	}
	if !contains(out, "\tSESSDATA\ts1") {
		t.Errorf("missing SESSDATA line:\n%s", out)
	}
	if !contains(out, "\tbili_jct\tj1") {
		t.Errorf("missing bili_jct line:\n%s", out)
	}
	if !contains(out, "\tDedeUserID\tu1") {
		t.Errorf("missing DedeUserID line:\n%s", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
