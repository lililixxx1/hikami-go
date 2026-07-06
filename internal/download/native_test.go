package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hikami-go/internal/biliutil"
)

type nativeFakeSigner struct{}

func (nativeFakeSigner) SignURL(rawURL string) (string, error) {
	return rawURL + "&signed=1", nil
}

// nativeHandleAntiRisk 处理 view 端点 -352 风控对抗的副请求（spi/nav）。
// 2026-07-06 VideoClient 加 buvid/WBI 注入后，Fetch 会先打 spi/nav。
// 测试 mock 需放行这两个路径并返回让对抗组件降级的响应：
//   - spi: 返回空 buvid（b_3 空）→ GetBuvids 报错 → 调用方降级不改 cookie
//   - nav: 返回 502 → WBISigner.ensureKeys 失败 → SignURL 降级不签名
//
// 已处理返回 true，调用方应直接 return。
func nativeHandleAntiRisk(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/x/frontend/finger/spi":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"b_3":"","b_4":""}}`))
		return true
	case "/x/web-interface/nav":
		http.Error(w, "nav unavailable in test", http.StatusBadGateway)
		return true
	}
	return false
}

type nativeRoundTripFunc func(req *http.Request) *httptest.ResponseRecorder

func (f nativeRoundTripFunc) Do(req *http.Request) (*http.Response, error) {
	recorder := f(req)
	return recorder.Result(), nil
}

func TestNativeDownloaderDownload(t *testing.T) {
	const (
		bvid      = "BV1xx411c7mD"
		audioBody = "backup-audio"
		xmlBody   = `<i><d p="1,1,25,16777215,1,0,hash,1">hello</d></i>`
	)

	var sawBaseAudio bool
	const baseURL = "https://bili.test"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeHandleAntiRisk(w, r) {
			return
		}
		if r.Header.Get("Cookie") != "SESSDATA=sess; bili_jct=jct; DedeUserID=100" {
			t.Fatalf("Cookie = %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("User-Agent") != biliutil.BrowserUA {
			t.Fatalf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Referer") != "https://www.bilibili.com" {
			t.Fatalf("Referer = %q", r.Header.Get("Referer"))
		}

		switch r.URL.Path {
		case "/x/web-interface/view":
			if r.URL.Query().Get("bvid") != bvid {
				t.Fatalf("view bvid = %q", r.URL.Query().Get("bvid"))
			}
			_, _ = w.Write([]byte(`{
				"code": 0,
				"data": {
					"aid": 123,
					"bvid": "` + bvid + `",
					"title": "测试回放",
					"pages": [{"cid": 456, "part": "P1", "page": 1}]
				}
			}`))
		case "/x/player/wbi/playurl":
			query := r.URL.Query()
			if query.Get("avid") != "123" || query.Get("cid") != "456" || query.Get("signed") != "1" {
				t.Fatalf("playurl query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"code": 0,
				"data": {
					"dash": {
						"audio": [
							{"id": 1, "baseUrl": "%[1]s/audio/low", "bandwidth": 64000},
							{"id": 2, "baseUrl": "%[1]s/audio/base", "backupUrl": ["%[1]s/audio/backup"], "bandwidth": 128000, "mimeType": "audio/mp4", "codecs": "mp4a.40.2"}
						]
					}
				}
			}`, baseURL)))
		case "/audio/base":
			sawBaseAudio = true
			http.Error(w, "fail", http.StatusServiceUnavailable)
		case "/audio/backup":
			_, _ = w.Write([]byte(audioBody))
		case "/x/v2/dm/web/seg.so":
			http.NotFound(w, r)
		case "/456.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(xmlBody))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	httpClient := nativeRoundTripFunc(func(req *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	})

	cookiePath := writeNativeCookieFile(t)
	rawDir := t.TempDir()
	err := (NativeDownloader{
		HTTPClient:  httpClient,
		ViewBaseURL: baseURL,
		APIBaseURL:  baseURL,
		CommentURL:  baseURL,
		Signer:      nativeFakeSigner{},
	}).Download(context.Background(), "https://www.bilibili.com/video/"+bvid, rawDir, cookiePath)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !sawBaseAudio {
		t.Fatal("base audio url was not tried")
	}
	assertFileContent(t, filepath.Join(rawDir, "audio.m4a"), audioBody)
	assertFileContent(t, filepath.Join(rawDir, "danmaku.xml"), xmlBody)

	metadataData, err := os.ReadFile(filepath.Join(rawDir, "metadata.ytdlp.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var metadata struct {
		Title string `json:"title"`
		BVID  string `json:"bvid"`
		CID   int64  `json:"cid"`
	}
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		t.Fatalf("parse metadata: %v", err)
	}
	if metadata.Title != "测试回放" || metadata.BVID != bvid || metadata.CID != 456 {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestNativeDownloaderCookieMissing(t *testing.T) {
	err := (NativeDownloader{}).Download(context.Background(), "https://www.bilibili.com/video/BV1xx411c7mD", t.TempDir(), "")
	if !errors.Is(err, ErrNativeCookieMissing) {
		t.Fatalf("err = %v, want ErrNativeCookieMissing", err)
	}
}

func TestNativeDownloaderUnsupportedSource(t *testing.T) {
	err := (NativeDownloader{
		Cookie: &biliutil.BiliCookie{SESSDATA: "sess", BiliJct: "jct", DedeUserID: "100"},
	}).Download(context.Background(), "https://example.com/not-bv", t.TempDir(), "")
	if !errors.Is(err, ErrNativeUnsupported) {
		t.Fatalf("err = %v, want ErrNativeUnsupported", err)
	}
}

func TestNativeDownloaderDownloadMultiP(t *testing.T) {
	const (
		bvid    = "BV1xx411c7mD"
		baseURL = "https://bili.test"
	)
	audioByPath := map[string]string{
		"/audio/p1": "audio-p1",
		"/audio/p2": "audio-p2",
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeHandleAntiRisk(w, r) {
			return
		}
		switch r.URL.Path {
		case "/x/web-interface/view":
			_, _ = w.Write([]byte(`{
				"code": 0,
				"data": {
					"aid": 123,
					"bvid": "` + bvid + `",
					"title": "多 P 回放",
					"pages": [
						{"cid": 456, "part": "P1", "page": 1},
						{"cid": 789, "part": "P2", "page": 2}
					]
				}
			}`))
		case "/x/player/wbi/playurl":
			cid := r.URL.Query().Get("cid")
			audioPath := "/audio/p1"
			if cid == "789" {
				audioPath = "/audio/p2"
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"code": 0,
				"data": {"dash": {"audio": [
					{"id": 1, "baseUrl": "%s%s", "bandwidth": 128000, "mimeType": "audio/mp4", "codecs": "mp4a.40.2"}
				]}}
			}`, baseURL, audioPath)))
		case "/x/v2/dm/web/seg.so":
			if r.URL.Query().Get("segment_index") != "1" {
				_, _ = w.Write([]byte{})
				return
			}
			switch r.URL.Query().Get("oid") {
			case "456":
				_, _ = w.Write(nativeSegReply(nativeSegElem(
					nativeProtoVarint(1, 11),
					nativeProtoVarint(2, 1000),
					nativeProtoVarint(3, 1),
					nativeProtoVarint(4, 25),
					nativeProtoVarint(5, 16777215),
					nativeProtoBytes(6, []byte("hash1")),
					nativeProtoBytes(7, []byte("seg-p1")),
					nativeProtoVarint(8, 1710000000),
					nativeProtoVarint(11, 0),
				)))
			case "789":
				_, _ = w.Write(nativeSegReply(nativeSegElem(
					nativeProtoVarint(1, 12),
					nativeProtoVarint(2, 2000),
					nativeProtoVarint(3, 1),
					nativeProtoVarint(4, 25),
					nativeProtoVarint(5, 16777215),
					nativeProtoBytes(6, []byte("hash2")),
					nativeProtoBytes(7, []byte("seg-p2")),
					nativeProtoVarint(8, 1710000001),
					nativeProtoVarint(11, 0),
				)))
			default:
				t.Fatalf("unexpected oid: %s", r.URL.Query().Get("oid"))
			}
		default:
			if body, ok := audioByPath[r.URL.Path]; ok {
				_, _ = w.Write([]byte(body))
				return
			}
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	rawDir := t.TempDir()
	ffmpeg, ffprobe := writeNativeMediaTools(t)
	err := (NativeDownloader{
		HTTPClient:  mockNativeHTTPDoer(handler),
		ViewBaseURL: baseURL,
		APIBaseURL:  baseURL,
		Cookie:      &biliutil.BiliCookie{SESSDATA: "sess", BiliJct: "jct", DedeUserID: "100"},
		Signer:      nativeFakeSigner{},
		FFmpeg:      ffmpeg,
		FFprobe:     ffprobe,
	}).Download(context.Background(), "https://www.bilibili.com/video/"+bvid, rawDir, "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertFileContent(t, filepath.Join(rawDir, "audio.m4a"), "concat-audio")
	assertFileContent(t, filepath.Join(rawDir, "danmaku_parts", "p001.xml"), `<i><d p="1.000,1,25,16777215,1710000000,0,hash1,11">seg-p1</d></i>`)
	assertFileContent(t, filepath.Join(rawDir, "danmaku_parts", "p002.xml"), `<i><d p="2.000,1,25,16777215,1710000001,0,hash2,12">seg-p2</d></i>`)
	if _, err := os.Stat(filepath.Join(rawDir, "metadata_parts", "p001.info.json")); err != nil {
		t.Fatalf("p001 metadata missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rawDir, "metadata_parts", "p002.info.json")); err != nil {
		t.Fatalf("p002 metadata missing: %v", err)
	}
	assertFileContent(t, filepath.Join(rawDir, "part_durations.json"), "[\n  {\n    \"index\": 1,\n    \"dur_secs\": 12.5\n  },\n  {\n    \"index\": 2,\n    \"dur_secs\": 12.5\n  }\n]\n")
	if _, err := os.Stat(filepath.Join(rawDir, "parts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("parts dir should be cleaned, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(rawDir, "concat.list")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("concat.list should be cleaned, stat err = %v", err)
	}
}

func TestNativeDownloaderSegFallbackToXML(t *testing.T) {
	const (
		bvid    = "BV1xx411c7mD"
		baseURL = "https://bili.test"
		xmlBody = `<i><d p="1,1,25,16777215,1,0,hash,1">xml</d></i>`
	)
	handler := nativeSinglePHandler(t, baseURL, bvid, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/v2/dm/web/seg.so":
			http.Error(w, "seg fail", http.StatusBadGateway)
		case "/456.xml":
			_, _ = w.Write([]byte(xmlBody))
		default:
			t.Fatalf("unexpected danmaku path: %s", r.URL.Path)
		}
	})

	rawDir := t.TempDir()
	err := (NativeDownloader{
		HTTPClient:  mockNativeHTTPDoer(handler),
		ViewBaseURL: baseURL,
		APIBaseURL:  baseURL,
		CommentURL:  baseURL,
		Cookie:      &biliutil.BiliCookie{SESSDATA: "sess", BiliJct: "jct", DedeUserID: "100"},
		Signer:      nativeFakeSigner{},
	}).Download(context.Background(), "https://www.bilibili.com/video/"+bvid, rawDir, "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertFileContent(t, filepath.Join(rawDir, "danmaku.xml"), xmlBody)
}

func TestNativeDownloaderDanmakuBothFailWritesEmpty(t *testing.T) {
	const (
		bvid    = "BV1xx411c7mD"
		baseURL = "https://bili.test"
	)
	handler := nativeSinglePHandler(t, baseURL, bvid, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/v2/dm/web/seg.so":
			http.Error(w, "seg fail", http.StatusBadGateway)
		case "/456.xml":
			http.Error(w, "xml fail", http.StatusBadGateway)
		default:
			t.Fatalf("unexpected danmaku path: %s", r.URL.Path)
		}
	})

	rawDir := t.TempDir()
	err := (NativeDownloader{
		HTTPClient:  mockNativeHTTPDoer(handler),
		ViewBaseURL: baseURL,
		APIBaseURL:  baseURL,
		CommentURL:  baseURL,
		Cookie:      &biliutil.BiliCookie{SESSDATA: "sess", BiliJct: "jct", DedeUserID: "100"},
		Signer:      nativeFakeSigner{},
	}).Download(context.Background(), "https://www.bilibili.com/video/"+bvid, rawDir, "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertFileContent(t, filepath.Join(rawDir, "danmaku.xml"), "<i></i>")
}

func TestNativeDownloaderNoPages(t *testing.T) {
	const (
		bvid    = "BV1xx411c7mD"
		baseURL = "https://bili.test"
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeHandleAntiRisk(w, r) {
			return
		}
		if r.URL.Path != "/x/web-interface/view" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"aid": 123,
				"bvid": "` + bvid + `",
				"title": "空回放",
				"pages": []
			}
		}`))
	})
	err := (NativeDownloader{
		HTTPClient:  mockNativeHTTPDoer(handler),
		ViewBaseURL: baseURL,
		Cookie:      &biliutil.BiliCookie{SESSDATA: "sess", BiliJct: "jct", DedeUserID: "100"},
	}).Download(context.Background(), "https://www.bilibili.com/video/"+bvid, t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "missing video data") {
		t.Fatalf("err = %v, want missing video data", err)
	}
}

func TestNativeDownloaderAudioAllURLsFail(t *testing.T) {
	const targetURL = "https://bili.test/audio.m4a"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != targetURL {
			t.Fatalf("url = %q", r.URL.String())
		}
		http.Error(w, "fail", http.StatusServiceUnavailable)
	})
	rawDir := t.TempDir()
	targetPath := filepath.Join(rawDir, "audio.m4a")
	err := (NativeDownloader{HTTPClient: mockNativeHTTPDoer(handler)}).downloadAudio(
		context.Background(),
		[]string{targetURL},
		"SESSDATA=sess",
		targetPath,
	)
	if !errors.Is(err, ErrAudioDownloadFailed) {
		t.Fatalf("err = %v, want ErrAudioDownloadFailed", err)
	}
	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target file should not exist, stat err = %v", err)
	}
	if _, err := os.Stat(targetPath + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmp file should be cleaned, stat err = %v", err)
	}
}

func TestAudioHTTPClientDefaultNoTimeout(t *testing.T) {
	client, ok := audioHTTPClientOrDefault(nil).(*http.Client)
	if !ok {
		t.Fatalf("default audio client type = %T, want *http.Client", client)
	}
	if client.Timeout != 0 {
		t.Fatalf("audio client timeout = %v, want 0", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("audio client transport = %T, want *http.Transport", client.Transport)
	}
	if transport == http.DefaultTransport {
		t.Fatal("audio client should not reuse http.DefaultTransport")
	}
	if transport.ForceAttemptHTTP2 {
		t.Fatal("audio client should disable HTTP/2")
	}
}

func mockNativeHTTPDoer(handler http.Handler) nativeRoundTripFunc {
	return nativeRoundTripFunc(func(req *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	})
}

func nativeSinglePHandler(t *testing.T, baseURL string, bvid string, danmaku http.HandlerFunc) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeHandleAntiRisk(w, r) {
			return
		}
		switch r.URL.Path {
		case "/x/web-interface/view":
			_, _ = w.Write([]byte(`{
				"code": 0,
				"data": {
					"aid": 123,
					"bvid": "` + bvid + `",
					"title": "测试回放",
					"pages": [{"cid": 456, "part": "P1", "page": 1}]
				}
			}`))
		case "/x/player/wbi/playurl":
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"code": 0,
				"data": {"dash": {"audio": [
					{"id": 1, "baseUrl": "%s/audio/single", "bandwidth": 128000, "mimeType": "audio/mp4", "codecs": "mp4a.40.2"}
				]}}
			}`, baseURL)))
		case "/audio/single":
			_, _ = w.Write([]byte("audio-single"))
		default:
			danmaku.ServeHTTP(w, r)
		}
	})
}

func writeNativeMediaTools(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	ffprobe := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffprobe, []byte("#!/bin/sh\nprintf '12.5\\n'\n"), 0o755); err != nil {
		t.Fatalf("write ffprobe: %v", err)
	}
	ffmpeg := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nout=\"\"\nwhile [ \"$#\" -gt 0 ]; do out=\"$1\"; shift; done\nprintf 'concat-audio' > \"$out\"\n"
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffmpeg: %v", err)
	}
	return ffmpeg, ffprobe
}

func nativeSegReply(elems ...[]byte) []byte {
	var buf bytes.Buffer
	for _, elem := range elems {
		buf.Write(nativeProtoBytes(1, elem))
	}
	return buf.Bytes()
}

func nativeSegElem(fields ...[]byte) []byte {
	var buf bytes.Buffer
	for _, field := range fields {
		buf.Write(field)
	}
	return buf.Bytes()
}

func nativeProtoVarint(field int, value uint64) []byte {
	var buf bytes.Buffer
	buf.Write(nativeVarint(uint64(field << 3)))
	buf.Write(nativeVarint(value))
	return buf.Bytes()
}

func nativeProtoBytes(field int, value []byte) []byte {
	var buf bytes.Buffer
	buf.Write(nativeVarint(uint64(field<<3 | 2)))
	buf.Write(nativeVarint(uint64(len(value))))
	buf.Write(value)
	return buf.Bytes()
}

func nativeVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}

func writeNativeCookieFile(t *testing.T) string {
	t.Helper()
	cookie := &biliutil.BiliCookie{
		SESSDATA:   "sess",
		BiliJct:    "jct",
		DedeUserID: "100",
	}
	path := filepath.Join(t.TempDir(), "cookie.txt")
	if err := os.WriteFile(path, cookie.NetscapeBytes(), 0o600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	return path
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

// ---------------------------------------------------------------------------
// fetchDanmakuShared（包级共享弹幕下载：seg.so + XML 回退 + 双失败写 <i></i>）
// 直接测包级函数，覆盖三种分支；native.Download 的端到端覆盖见上面两个 Test。
// ---------------------------------------------------------------------------

func TestFetchDanmakuShared(t *testing.T) {
	const cid int64 = 456
	segOK := nativeSegReply(nativeProtoBytes(7, []byte(`<d p="1,1,25,16777215,1,0,hash,1">seg</d>`)))
	const xmlBody = `<i><d p="1,1,25,16777215,1,0,hash,1">xml</d></i>`

	// doer 按 case 返回不同的 seg.so / {cid}.xml 响应。mode 控制两个端点的行为。
	newDoer := func(t *testing.T, segOKBody []byte, segFail, xmlFail bool) biliutil.HTTPDoer {
		t.Helper()
		return nativeRoundTripFunc(func(req *http.Request) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			switch r := req.URL.Path; {
			case r == "/x/v2/dm/web/seg.so":
				if segFail {
					rec.Code = http.StatusBadGateway
				} else {
					_, _ = rec.Write(segOKBody)
				}
			case r == fmt.Sprintf("/%d.xml", cid):
				if xmlFail {
					rec.Code = http.StatusBadGateway
				} else {
					_, _ = rec.Write([]byte(xmlBody))
				}
			default:
				t.Fatalf("unexpected path: %s", r)
			}
			return rec
		})
	}

	t.Run("seg_success_returns_seg_danmaku", func(t *testing.T) {
		got := fetchDanmakuShared(context.Background(), newDoer(t, segOK, false, false), "https://bili.test", "https://bili.test", cid, "")
		if !strings.Contains(string(got), "<d ") {
			t.Fatalf("got %q, want seg danmaku containing <d", got)
		}
	})

	t.Run("seg_fail_fallback_to_xml", func(t *testing.T) {
		got := fetchDanmakuShared(context.Background(), newDoer(t, segOK, true, false), "https://bili.test", "https://bili.test", cid, "")
		if !strings.Contains(string(got), "xml") {
			t.Fatalf("got %q, want xml fallback content", got)
		}
	})

	t.Run("both_fail_writes_empty", func(t *testing.T) {
		got := fetchDanmakuShared(context.Background(), newDoer(t, segOK, true, true), "https://bili.test", "https://bili.test", cid, "")
		if string(got) != "<i></i>" {
			t.Fatalf("got %q, want empty <i></i>", got)
		}
	})
}
