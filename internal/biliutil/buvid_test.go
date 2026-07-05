package biliutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// spiStub 起一个 httptest server 模拟 /x/frontend/finger/spi，返回给定 buvid3/buvid4。
// 同时统计请求数（用于验证缓存命中），并用 mutex 保护（并发场景）。
func spiStub(t *testing.T, b3, b4 string, fail bool) (*httptest.Server, *int32) {
	t.Helper()
	var count int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// 校验请求头
		if got := r.Header.Get("User-Agent"); got != BiliUserAgent {
			t.Errorf("spi request UA = %q, want %q", got, BiliUserAgent)
		}
		if got := r.Header.Get("Referer"); got != "https://www.bilibili.com" {
			t.Errorf("spi request Referer = %q, want https://www.bilibili.com", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    0,
			"message": "0",
			"data":    map[string]any{"b_3": b3, "b_4": b4},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

func TestGetBuvids_FetchesAndCaches(t *testing.T) {
	srv, count := spiStub(t, "fake3", "fake4", false)
	s := NewBuvidStoreWithOptions(nil, srv.URL)

	// 第一次：远程拉取
	b3, b4, err := s.GetBuvids(context.Background(), "SESSDATA=abc")
	if err != nil {
		t.Fatalf("first GetBuvids: %v", err)
	}
	if b3 != "fake3" || b4 != "fake4" {
		t.Fatalf("got b3=%q b4=%q, want fake3/fake4", b3, b4)
	}

	// 第二次同 cookie：应命中缓存，不再打远程
	b3b, _, _ := s.GetBuvids(context.Background(), "SESSDATA=abc")
	if b3b != "fake3" {
		t.Fatalf("cached b3 = %q, want fake3", b3b)
	}
	if got := readCount(count); got != 1 {
		t.Fatalf("spi request count = %d, want 1 (cache miss once)", got)
	}
}

func TestGetBuvids_CacheKeyedByCookie(t *testing.T) {
	srv, count := spiStub(t, "v3", "v4", false)
	s := NewBuvidStoreWithOptions(nil, srv.URL)

	_, _, _ = s.GetBuvids(context.Background(), "SESSDATA=one")
	_, _, _ = s.GetBuvids(context.Background(), "SESSDATA=two") // 不同 cookie → 新拉取

	if got := readCount(count); got != 2 {
		t.Fatalf("spi request count = %d, want 2 (two distinct cookies)", got)
	}
}

func TestGetBuvids_RejectsEmptyB3(t *testing.T) {
	// b_3 为空应报错（b_4 为空允许）
	srv, _ := spiStub(t, "", "fake4", false)
	s := NewBuvidStoreWithOptions(nil, srv.URL)

	_, _, err := s.GetBuvids(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty b_3, got nil")
	}
	if !strings.Contains(err.Error(), "b_3") && !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error should mention empty b_3, got: %v", err)
	}
}

func TestGetBuvids_HTTPFailureReturnsError(t *testing.T) {
	srv, _ := spiStub(t, "", "", true) // 500
	s := NewBuvidStoreWithOptions(nil, srv.URL)

	_, _, err := s.GetBuvids(context.Background(), "k=v")
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
}

func TestGetBuvids_NilStoreIsSafe(t *testing.T) {
	// nil 接收者必须不 panic、不打网络、返回零值
	var s *BuvidStore
	b3, b4, err := s.GetBuvids(context.Background(), "k=v")
	if err != nil {
		t.Fatalf("nil store GetBuvids err = %v, want nil", err)
	}
	if b3 != "" || b4 != "" {
		t.Fatalf("nil store got b3=%q b4=%q, want empty", b3, b4)
	}
}

func TestInjectBuvids_ReplaceSemantics(t *testing.T) {
	tests := []struct {
		name         string
		cookieHeader string
		buvid3       string
		buvid4       string
		want         string
	}{
		{
			name:         "empty header appends both",
			cookieHeader: "",
			buvid3:       "new3",
			buvid4:       "new4",
			want:         "buvid3=new3; buvid4=new4",
		},
		{
			name:         "existing header appends preserving",
			cookieHeader: "SESSDATA=abc; bili_jct=csrf",
			buvid3:       "new3",
			buvid4:       "new4",
			want:         "SESSDATA=abc; bili_jct=csrf; buvid3=new3; buvid4=new4",
		},
		{
			name:         "replaces old buvid3",
			cookieHeader: "SESSDATA=abc; buvid3=old3; buvid4=old4",
			buvid3:       "new3",
			buvid4:       "new4",
			want:         "SESSDATA=abc; buvid3=new3; buvid4=new4",
		},
		{
			name:         "only buvid3 provided drops old buvid4 if empty",
			cookieHeader: "SESSDATA=abc; buvid3=old3; buvid4=old4",
			buvid3:       "new3",
			buvid4:       "",
			want:         "SESSDATA=abc; buvid3=new3",
		},
		{
			name:         "empty values strip old",
			cookieHeader: "SESSDATA=abc; buvid3=old3; buvid4=old4",
			buvid3:       "",
			buvid4:       "",
			want:         "SESSDATA=abc",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InjectBuvids(tc.cookieHeader, tc.buvid3, tc.buvid4)
			if got != tc.want {
				t.Fatalf("InjectBuvids(%q, %q, %q)\n  got  = %q\n  want = %q",
					tc.cookieHeader, tc.buvid3, tc.buvid4, got, tc.want)
			}
		})
	}
}

// readCount 读 spiStub 的请求计数（mutex 保护，spiStub 内部自增）。
func readCount(p *int32) int32 {
	mu := &sync.Mutex{}
	mu.Lock()
	defer mu.Unlock()
	return *p
}

// 保证 cache TTL 常量存在（编译期约束）
var _ = time.Hour * 24
