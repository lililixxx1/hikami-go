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
