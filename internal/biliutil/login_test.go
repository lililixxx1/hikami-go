package biliutil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestQRLoginSessionStoreCreateAndPollSucceeded(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	client := NewQRLoginClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			return jsonResponse(200, nil, `{"code":0,"message":"0","data":{"url":"https://passport.bilibili.com/scan","qrcode_key":"key_1"}}`), nil
		case "/x/passport-login/web/qrcode/poll":
			if got := r.URL.Query().Get("qrcode_key"); got != "key_1" {
				t.Fatalf("qrcode_key = %q", got)
			}
			headers := http.Header{}
			headers.Add("Set-Cookie", "SESSDATA=sess; Domain=.bilibili.com; Path=/; HttpOnly")
			headers.Add("Set-Cookie", "bili_jct=csrf; Domain=.bilibili.com; Path=/")
			headers.Add("Set-Cookie", "DedeUserID=42; Domain=.bilibili.com; Path=/")
			return jsonResponse(200, headers, `{"code":0,"message":"0","data":{"url":"https://www.bilibili.com/","refresh_token":"refresh","code":0,"message":"登录成功"}}`), nil
		default:
			return jsonResponse(404, nil, `{}`), nil
		}
	})})
	client.BaseURL = "https://passport.test"
	client.Now = func() time.Time { return now }
	store := NewQRLoginSessionStore(client, 180*time.Second)

	created, err := store.Create(context.Background())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.URL != "https://passport.bilibili.com/scan" || created.SessionID == "" {
		t.Fatalf("unexpected create result: %+v", created)
	}

	poll, err := store.Poll(context.Background(), created.SessionID)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if poll.Status != QRLoginSucceeded || poll.UID != 42 {
		t.Fatalf("unexpected poll result: %+v", poll)
	}

	session, err := store.GetSucceeded(created.SessionID)
	if err != nil {
		t.Fatalf("get succeeded: %v", err)
	}
	if session.RefreshToken != "refresh" || len(session.Cookies) != 3 {
		t.Fatalf("unexpected session: %+v", session)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestQRLoginSessionStoreMapsPollStatuses(t *testing.T) {
	cases := []struct {
		code int
		want QRLoginStatus
	}{
		{code: 86101, want: QRLoginPending},
		{code: 86090, want: QRLoginScanned},
		{code: 86038, want: QRLoginExpired},
		{code: 0, want: QRLoginSucceeded},
		{code: -1, want: QRLoginFailed},
	}
	for _, tc := range cases {
		if got := mapQRLoginStatus(tc.code); got != tc.want {
			t.Fatalf("mapQRLoginStatus(%d) = %s, want %s", tc.code, got, tc.want)
		}
	}
}

func TestQRLoginSessionStoreExpired(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	client := NewQRLoginClient(&http.Client{})
	client.Now = func() time.Time { return now }
	store := NewQRLoginSessionStore(client, time.Minute)
	store.sessions["session_1"] = &QRLoginSession{
		SessionID: "session_1",
		Status:    QRLoginPending,
		ExpiresAt: now.Add(-time.Second),
		CreatedAt: now.Add(-2 * time.Minute),
		UpdatedAt: now.Add(-2 * time.Minute),
	}

	_, err := store.Poll(context.Background(), "session_1")
	if err != ErrQRLoginSessionExpired {
		t.Fatalf("err = %v, want session expired", err)
	}
}
