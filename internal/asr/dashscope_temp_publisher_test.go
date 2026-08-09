package asr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hikami-go/internal/session"
)

func TestDashScopeTempPublisherPublish(t *testing.T) {
	const audioContent = "small-audio-payload"
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/api/v1/uploads" || r.URL.Query().Get("action") != "getPolicy" || r.URL.Query().Get("model") != "fun-asr" {
				t.Errorf("unexpected policy request: %s", r.URL.String())
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Errorf("Authorization = %q, want Bearer test-key", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": "req-1",
				"data": map[string]any{
					"policy":                 "signed-policy",
					"signature":              "signed-value",
					"upload_dir":             "dashscope-instant/account/date/id",
					"upload_host":            server.URL,
					"max_file_size_mb":       "1024",
					"oss_access_key_id":      "temp-ak",
					"x_oss_object_acl":       "private",
					"x_oss_forbid_overwrite": "true",
				},
			})
		case http.MethodPost:
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			checks := map[string]string{
				"OSSAccessKeyId":         "temp-ak",
				"policy":                 "signed-policy",
				"Signature":              "signed-value",
				"key":                    "dashscope-instant/account/date/id/audio.asr.mp3",
				"x-oss-object-acl":       "private",
				"x-oss-forbid-overwrite": "true",
				"success_action_status":  "200",
			}
			for name, want := range checks {
				if got := r.FormValue(name); got != want {
					t.Errorf("form %s = %q, want %q", name, got, want)
				}
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("FormFile: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer file.Close()
			got, _ := io.ReadAll(file)
			if string(got) != audioContent {
				t.Errorf("uploaded file = %q, want %q", got, audioContent)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	t.Setenv("TEST_DASHSCOPE_KEY", "test-key")
	audioPath := filepath.Join(t.TempDir(), "audio.asr.mp3")
	if err := os.WriteFile(audioPath, []byte(audioContent), 0o600); err != nil {
		t.Fatal(err)
	}
	publisher := newDashScopeTempPublisher(server.Client(), server.URL+"/api/v1/uploads", "TEST_DASHSCOPE_KEY", "fun-asr")
	gotURL, remotePath, err := publisher.Publish(context.Background(), audioPath, session.Session{})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	wantURL := "oss://dashscope-instant/account/date/id/audio.asr.mp3"
	if gotURL != wantURL || remotePath != wantURL {
		t.Fatalf("Publish() = (%q, %q), want (%q, %q)", gotURL, remotePath, wantURL, wantURL)
	}
}

func TestUploadLimitBytes(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want int64
	}{
		{`1024`, 1024 * 1024 * 1024},
		{`"100"`, 100 * 1024 * 1024},
		{`null`, 0},
	} {
		t.Run(fmt.Sprintf("raw_%s", test.raw), func(t *testing.T) {
			got, err := uploadLimitBytes(json.RawMessage(test.raw))
			if err != nil {
				t.Fatalf("uploadLimitBytes(%s): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("uploadLimitBytes(%s) = %d, want %d", test.raw, got, test.want)
			}
		})
	}
}
