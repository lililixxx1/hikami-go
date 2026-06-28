package biliutil

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestVideoClientFetch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/web-interface/view" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("bvid"); got != "BV1xx411c7mD" {
			t.Fatalf("bvid = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "SESSDATA=sess" {
			t.Fatalf("Cookie = %q", got)
		}
		if got := r.Header.Get("Referer"); got != biliReferer {
			t.Fatalf("Referer = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != BrowserUA {
			t.Fatalf("User-Agent = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"aid": 123,
				"bvid": "BV1xx411c7mD",
				"title": "测试视频",
				"pages": [{"cid": 456, "part": "P1", "page": 1}]
			}
		}`))
	})

	info, err := (VideoClient{HTTPClient: mockHTTPDoer(handler), BaseURL: "https://api.test"}).Fetch(context.Background(), "BV1xx411c7mD", "SESSDATA=sess")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.AID != 123 || info.BVID != "BV1xx411c7mD" || info.Title != "测试视频" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(info.Pages) != 1 || info.Pages[0].CID != 456 {
		t.Fatalf("unexpected pages: %+v", info.Pages)
	}
}

func TestVideoClientFetchHTTPError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fail", http.StatusBadGateway)
	})
	_, err := (VideoClient{HTTPClient: mockHTTPDoer(handler), BaseURL: "https://api.test"}).Fetch(context.Background(), "BV1xx411c7mD", "")
	if err == nil || !strings.Contains(err.Error(), "view http status 502") {
		t.Fatalf("err = %v, want http status error", err)
	}
}

func TestVideoClientFetchAPICodeError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-404,"message":"not found"}`))
	})
	_, err := (VideoClient{HTTPClient: mockHTTPDoer(handler), BaseURL: "https://api.test"}).Fetch(context.Background(), "BV1xx411c7mD", "")
	if err == nil || !strings.Contains(err.Error(), "view api code -404") {
		t.Fatalf("err = %v, want api code error", err)
	}
}
