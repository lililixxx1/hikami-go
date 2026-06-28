//go:build probe

// 真实联调探针：仅在 `go test -tags=probe` 时编译运行，走真实 B 站网络。
// 用环境变量驱动：PROBE_COOKIE_FILE（Netscape cookie 文件路径）、PROBE_BVID（单 P 视频 BV 号）。
// 不参与常规测试，不联网场景下自动 t.Skip。
package download

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hikami-go/internal/biliutil"
)

// probeEnv 取必填环境变量，缺失则 skip。
func probeEnv(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("set %s to run probe", key)
	}
	return v
}

// probeCookie 从 PROBE_COOKIE_FILE 加载 Netscape cookie 并返回 Cookie header。
func probeCookie(t *testing.T) string {
	t.Helper()
	f := probeEnv(t, "PROBE_COOKIE_FILE")
	c, err := biliutil.LoadCookie(f)
	if err != nil {
		t.Fatalf("load cookie file %s: %v", f, err)
	}
	return c.CookieHeader()
}

// TestProbeView 验证 view 接口：拿 title/aid/cid/pages。
func TestProbeView(t *testing.T) {
	cookie := probeCookie(t)
	bvid := probeEnv(t, "PROBE_BVID")
	info, err := biliutil.VideoClient{}.Fetch(context.Background(), bvid, cookie)
	if err != nil {
		t.Fatalf("view 接口失败: %v", err)
	}
	t.Logf("✅ view: aid=%d bvid=%s title=%q pages=%d", info.AID, info.BVID, info.Title, len(info.Pages))
	for i, p := range info.Pages {
		t.Logf("   page[%d] cid=%d part=%q", i, p.CID, p.Part)
	}
	if len(info.Pages) != 1 {
		t.Logf("⚠️ pages=%d：native 仅支持单 P，多 P 会返回 ErrNativeUnsupported（auto 模式自动 fallback ytdlp）", len(info.Pages))
	}
}

// TestProbePlayURL 验证 WBI playurl：拿音频流并选最高 bandwidth。
func TestProbePlayURL(t *testing.T) {
	cookie := probeCookie(t)
	bvid := probeEnv(t, "PROBE_BVID")
	info, err := biliutil.VideoClient{}.Fetch(context.Background(), bvid, cookie)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	cid := info.Pages[0].CID
	streams, err := biliutil.FetchPlayURL(context.Background(), info.AID, cid, info.BVID, cookie, nil)
	if err != nil {
		t.Fatalf("playurl 接口失败: %v", err)
	}
	t.Logf("✅ playurl: %d 条音频流", len(streams))
	for _, s := range streams {
		t.Logf("   id=%d bandwidth=%d codecs=%s", s.ID, s.Bandwidth, s.Codecs)
	}
	best, err := biliutil.SelectBestAudioStream(streams)
	if err != nil {
		t.Fatalf("选流失败: %v", err)
	}
	t.Logf("✅ 选中最优音频: id=%d bandwidth=%d 备用地址数=%d", best.ID, best.Bandwidth, len(best.URLs())-1)
}

// TestProbeDanmaku 验证弹幕 XML 拉取（comment.bilibili.com/{cid}.xml）。
func TestProbeDanmaku(t *testing.T) {
	cookie := probeCookie(t)
	bvid := probeEnv(t, "PROBE_BVID")
	info, err := biliutil.VideoClient{}.Fetch(context.Background(), bvid, cookie)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	cid := info.Pages[0].CID
	xml, err := biliutil.FetchDanmakuXML(context.Background(), cid, cookie)
	if err != nil {
		t.Fatalf("弹幕 XML 接口失败: %v", err)
	}
	count := strings.Count(string(xml), "<d ")
	t.Logf("✅ danmaku: %d bytes，约 %d 条弹幕", len(xml), count)
}

// TestProbeE2E 端到端：NativeDownloader.Download 完整产出 audio.m4a + danmaku.xml + 元数据。
func TestProbeE2E(t *testing.T) {
	cookieFile := probeEnv(t, "PROBE_COOKIE_FILE")
	bvid := probeEnv(t, "PROBE_BVID")
	dir := t.TempDir()
	d := NativeDownloader{}
	sourceURL := "https://www.bilibili.com/video/" + bvid
	if err := d.Download(context.Background(), sourceURL, dir, cookieFile); err != nil {
		t.Fatalf("E2E 下载失败: %v", err)
	}
	for _, name := range []string{"audio.m4a", "danmaku.xml", "metadata.ytdlp.json"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("❌ 产物缺失: %s (%v)", name, err)
			continue
		}
		t.Logf("✅ 产物 %s: %d bytes", name, st.Size())
	}
}

// TestProbeSegDanmaku 验证 seg.so 真实拉取、protobuf 解码与 XML 转换。
// 检查转出的 XML 能被标准 xml.Unmarshal 解析（content 实体经 EscapeText 正确编码）。
func TestProbeSegDanmaku(t *testing.T) {
	cookie := probeCookie(t)
	bvid := probeEnv(t, "PROBE_BVID")
	info, err := biliutil.VideoClient{}.Fetch(context.Background(), bvid, cookie)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	cid := info.Pages[0].CID
	client := biliutil.SegDanmakuClient{}
	xmlData, err := client.FetchSegments(context.Background(), cid, cookie)
	if err != nil {
		t.Fatalf("seg.so 拉取失败: %v", err)
	}
	count := strings.Count(string(xmlData), "<d ")
	t.Logf("✅ seg.so: %d bytes，约 %d 条弹幕 (cid=%d)", len(xmlData), count, cid)
	var probe struct {
		D []struct{} `xml:"d"`
	}
	if err := xml.Unmarshal(xmlData, &probe); err != nil {
		t.Fatalf("seg.so 转 XML 非法: %v", err)
	}
	t.Logf("✅ seg.so XML 合法，xml.Unmarshal 解析出 %d 条 <d>", len(probe.D))
}

// TestProbeMultiP 端到端：NativeDownloader.Download 多 P 视频，验证产物对齐 yt-dlp 布局。
// 需 PROBE_MULTIP_BVID（多 P 视频 BV 号）。
func TestProbeMultiP(t *testing.T) {
	cookieFile := probeEnv(t, "PROBE_COOKIE_FILE")
	bvid := probeEnv(t, "PROBE_MULTIP_BVID")
	dir := t.TempDir()
	d := NativeDownloader{}
	if err := d.Download(context.Background(), "https://www.bilibili.com/video/"+bvid, dir, cookieFile); err != nil {
		t.Fatalf("多 P 下载失败: %v", err)
	}
	for _, name := range []string{"audio.m4a", "part_durations.json"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("❌ 缺失 %s: %v", name, err)
			continue
		}
		t.Logf("✅ %s: %d bytes", name, st.Size())
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "danmaku_parts")); err == nil {
		t.Logf("✅ danmaku_parts: %d 个分 P 弹幕", len(entries))
		for _, e := range entries {
			t.Logf("   %s", e.Name())
		}
	} else {
		t.Errorf("❌ danmaku_parts 读取失败: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "metadata_parts")); err == nil {
		t.Logf("✅ metadata_parts: %d 个分 P 元数据", len(entries))
	} else {
		t.Errorf("❌ metadata_parts 读取失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "parts")); !os.IsNotExist(err) {
		t.Errorf("❌ parts/ 临时目录未清理")
	}
	t.Logf("✅ 多 P 产物对齐 yt-dlp 布局（audio.m4a + danmaku_parts/ + metadata_parts/ + part_durations.json）")
}

// TestProbeSegDiag 诊断 seg.so 304 根因：对比单次 vs 复用连接、HTTP/2 vs HTTP/1.1。
func TestProbeSegDiag(t *testing.T) {
	cookie := probeCookie(t)
	bvid := probeEnv(t, "PROBE_BVID")
	info, err := biliutil.VideoClient{}.Fetch(context.Background(), bvid, cookie)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	cid := info.Pages[0].CID
	doReq := func(label string, client *http.Client, idx int) {
		endpoint := fmt.Sprintf("https://api.bilibili.com/x/v2/dm/web/seg.so?type=1&oid=%d&segment_index=%d", cid, idx)
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatalf("[%s] new req: %v", label, err)
		}
		req.Header.Set("User-Agent", biliutil.BrowserUA)
		req.Header.Set("Referer", "https://www.bilibili.com")
		req.Header.Set("Cookie", cookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("[%s idx=%d] err: %v", label, idx, err)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("[%s idx=%d] proto=%s status=%d size=%d ce=%q", label, idx, resp.Proto, resp.StatusCode, len(body), resp.Header.Get("Content-Encoding"))
	}
	// 1) 单次默认（HTTP/2）
	doReq("default-h2", &http.Client{Timeout: 30 * time.Second}, 1)
	// 2) 单次强制 HTTP/1.1
	doReq("force-h1", &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{ForceAttemptHTTP2: false}}, 1)
	// 3) 复用同一 h2 client 连发 index 1/2/3（模拟 FetchSegments）
	shared := &http.Client{Timeout: 30 * time.Second}
	for _, idx := range []int{1, 2, 3} {
		doReq("shared-h2", shared, idx)
	}
	// 4) 每个 index 用独立 client（模拟 curl 每次新连接）
	for _, idx := range []int{1, 2, 3} {
		doReq("fresh-h2", &http.Client{Timeout: 30 * time.Second}, idx)
	}
}
