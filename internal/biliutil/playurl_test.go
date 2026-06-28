package biliutil

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type fakeURLSigner struct {
	called bool
}

func (s *fakeURLSigner) SignURL(rawURL string) (string, error) {
	s.called = true
	return rawURL + "&signed=1", nil
}

func TestPlayURLClientFetchAndSelectBest(t *testing.T) {
	signer := &fakeURLSigner{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/player/wbi/playurl" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("avid") != "123" || query.Get("cid") != "456" || query.Get("bvid") != "BV1xx411c7mD" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if query.Get("qn") != "127" || query.Get("fnval") != "16" || query.Get("signed") != "1" {
			t.Fatalf("missing playurl query params: %s", r.URL.RawQuery)
		}
		if got := r.Header.Get("Cookie"); got != "SESSDATA=sess" {
			t.Fatalf("Cookie = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"dash": {
					"audio": [
						{"id": 30216, "baseUrl": "http://example.test/low.m4a", "bandwidth": 64000, "mimeType": "audio/mp4", "codecs": "mp4a.40.2"},
						{"id": 30280, "baseUrl": "http://example.test/high.m4a", "backupUrl": ["http://example.test/high-backup.m4a"], "bandwidth": 128000, "mimeType": "audio/mp4", "codecs": "mp4a.40.2"}
					]
				}
			}
		}`))
	})

	streams, err := (PlayURLClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
		Signer:     signer,
	}).Fetch(context.Background(), 123, 456, "BV1xx411c7mD", "SESSDATA=sess")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !signer.called {
		t.Fatal("SignURL was not called")
	}

	best, err := SelectBestAudioStream(streams)
	if err != nil {
		t.Fatalf("SelectBestAudioStream: %v", err)
	}
	if best.ID != 30280 || best.Bandwidth != 128000 {
		t.Fatalf("best stream = %+v", best)
	}
	urls := best.URLs()
	if len(urls) != 2 || urls[0] != "http://example.test/high.m4a" || urls[1] != "http://example.test/high-backup.m4a" {
		t.Fatalf("urls = %#v", urls)
	}
}

func TestPlayURLClientNoAudioStream(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"dash":{"audio":[]}}}`))
	})

	_, err := (PlayURLClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
		Signer:     &fakeURLSigner{},
	}).Fetch(context.Background(), 123, 456, "BV1xx411c7mD", "")
	if !errors.Is(err, ErrNoAudioStream) {
		t.Fatalf("err = %v, want ErrNoAudioStream", err)
	}
}

func TestPlayURLClientHTTPError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fail", http.StatusBadGateway)
	})

	_, err := (PlayURLClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
		Signer:     &fakeURLSigner{},
	}).Fetch(context.Background(), 123, 456, "BV1xx411c7mD", "")
	if !errors.Is(err, ErrPlayURLFailed) {
		t.Fatalf("err = %v, want ErrPlayURLFailed", err)
	}
}

func TestPlayURLClientAPICodeError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-352,"message":"risk control"}`))
	})

	_, err := (PlayURLClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
		Signer:     &fakeURLSigner{},
	}).Fetch(context.Background(), 123, 456, "BV1xx411c7mD", "")
	if !errors.Is(err, ErrPlayURLFailed) {
		t.Fatalf("err = %v, want ErrPlayURLFailed", err)
	}
}
