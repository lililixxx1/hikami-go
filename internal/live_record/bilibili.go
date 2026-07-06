package live_record

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"hikami-go/internal/biliutil"
)

type BilibiliClient struct {
	httpClient *http.Client
	baseURL    string
	buvids     *biliutil.BuvidStore
	signers    map[string]biliutil.URLSigner
	signersMu  sync.Mutex
	// newSigner 按 cookie 创建 WBISigner（默认 biliutil.NewWBISigner），测试可注入桩。
	newSigner func(cookie string) biliutil.URLSigner
}

type streamCandidate struct {
	url       string
	codecName string
	priority  int
}

func NewBilibiliClient() *BilibiliClient {
	c := &BilibiliClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    "https://api.live.bilibili.com",
		signers:    make(map[string]biliutil.URLSigner),
		newSigner:  func(cookie string) biliutil.URLSigner { return biliutil.NewWBISigner(cookie) },
	}
	c.buvids = biliutil.NewBuvidStoreWithHTTPClient(c.httpClient)
	return c
}

func NewBilibiliClientWithBaseURL(baseURL string) *BilibiliClient {
	client := NewBilibiliClient()
	client.baseURL = baseURL
	return client
}

// signerForCookie 返回指定 cookie 对应的 URLSigner，按 cookie 懒初始化和缓存（与 identify/danmaku 同模式）。
// WBI 签名（w_rid + wts）是 getInfoByRoom / getRoomPlayInfo 端点通过 -352 风控的必要条件，
// 单靠 buvid 注入不够（探针实测：buvid only 仍 -352，buvid + WBI → 200 code=0）。
func (c *BilibiliClient) signerForCookie(cookie string) biliutil.URLSigner {
	c.signersMu.Lock()
	defer c.signersMu.Unlock()
	if s, ok := c.signers[cookie]; ok {
		return s
	}
	s := c.newSigner(cookie)
	c.signers[cookie] = s
	return s
}

// SetBuvidStore 替换内部 buvid 存储，仅用于测试注入指向 httptest 桩的 spi URL。
func (c *BilibiliClient) SetBuvidStore(store *biliutil.BuvidStore) {
	c.buvids = store
}

// SetSignerFactory 替换签名器工厂，仅用于测试注入桩签名器（避免打真实 WBI nav）。
func (c *BilibiliClient) SetSignerFactory(fn func(cookie string) biliutil.URLSigner) {
	c.newSigner = fn
}

// injectAntiRisk 注入 buvid3/buvid4 对抗 -352 风控（与 identify/danmaku/publisher 共享 BuvidStore）。
// GetBuvids 失败或返回空值时降级为不改 cookie（仅 warn），避免在无新指纹时误剔除已有 buvid3。
func (c *BilibiliClient) injectAntiRisk(ctx context.Context, cookieHeader string) string {
	if c.buvids == nil {
		return cookieHeader
	}
	b3, b4, berr := c.buvids.GetBuvids(ctx, cookieHeader)
	if berr != nil {
		slog.Warn("live_record: get buvids failed, continuing without buvid", "error", berr)
		return cookieHeader
	}
	if b3 == "" && b4 == "" {
		return cookieHeader
	}
	return biliutil.InjectBuvids(cookieHeader, b3, b4)
}

// signURL 对端点做 WBI 签名（失败降级为不签名，仍尝试，保持容错）。
func (c *BilibiliClient) signURL(endpoint, cookieHeader string) string {
	signed, err := c.signerForCookie(cookieHeader).SignURL(endpoint)
	if err != nil {
		slog.Warn("live_record: wbi sign failed, continuing unsigned", "error", err)
		return endpoint
	}
	return signed
}

func (c *BilibiliClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	// baseCookie 是原始入参,同时是 BuvidStore 缓存 key 和 signer 选择 key。
	// injectAntiRisk 会向 cookie 注入 buvid3/buvid4(改 Cookie 头),但 WBI 签名密钥源自
	// B站账号身份(nav API)不随 buvid 变,故 signer 按 baseCookie 选(账号维度)。
	// 这样 -352 重试时 RefreshKeys 刷的就是 query() 实际签名用的 signer(codex 审核要点)。
	baseCookie := cookieHeader

	// query 每次都从 baseCookie 重新注入 buvid + 重新签名 + 重新请求,
	// 确保 -352 重试(已 Invalidate(baseCookie) + RefreshKeys)用新 buvid + 新签名。
	query := func() (roomInfoResponse, error) {
		injected := c.injectAntiRisk(ctx, baseCookie)
		endpoint := c.baseURL + "/xlive/web-room/v1/index/getInfoByRoom?room_id=" + url.QueryEscape(strconv.FormatInt(roomID, 10))
		// signer 按 baseCookie 选(v3 修正:不再按 injected),WBI 密钥随账号不随 buvid
		if signed, err := c.signerForCookie(baseCookie).SignURL(endpoint); err == nil {
			endpoint = signed
		} else {
			slog.Warn("live_record: wbi sign failed, continuing unsigned", "error", err)
		}
		var resp roomInfoResponse
		if err := c.getJSON(ctx, endpoint, &resp, injected, roomID); err != nil {
			return roomInfoResponse{}, err
		}
		return resp, nil
	}

	response, err := query()
	if err != nil {
		return LiveInfo{}, err
	}

	// -352 单次重试(对齐 danmaku.go:326 范式):刷新 WBI 密钥 + 失效 buvid 缓存后重试一次。
	if response.Code == -352 {
		slog.Warn("checklive risk control -352, refreshing keys/buvid and retrying once",
			"channel_id", ctx.Value(liveRecordChannelIDKey), "room_id", roomID)
		// 局部断言访问 RefreshKeys(不扩大公共 URLSigner 接口,避免影响 playurl/publisher/channel)
		if rs, ok := c.signerForCookie(baseCookie).(interface{ RefreshKeys() error }); ok {
			if rerr := rs.RefreshKeys(); rerr != nil {
				slog.Warn("checklive RefreshKeys failed, retrying with existing keys", "error", rerr)
			}
		}
		if c.buvids != nil {
			c.buvids.Invalidate(baseCookie)
		}
		response, err = query()
		if err != nil {
			return LiveInfo{}, err
		}
	}

	if response.Code != 0 {
		if response.Code == -352 {
			// 重试仍 -352:返回可识别哨兵,checkOne 据此触发频道级冷却
			return LiveInfo{}, fmt.Errorf("%w: room_id=%d", ErrRiskControl352, roomID)
		}
		return LiveInfo{}, fmt.Errorf("bilibili room info error: code=%d message=%s", response.Code, response.Message)
	}

	room := response.Data.RoomInfo
	startedAt := parseBilibiliTime(room.LiveStartTime)
	title := room.Title
	if title == "" {
		title = response.Data.AnchorInfo.BaseInfo.UName
	}
	live := room.LiveStatus == 1
	if live {
		slog.Info("bilibili live status checked",
			"channel_id", ctx.Value(liveRecordChannelIDKey),
			"room_id", room.RoomID,
			"live", live,
			"title", title)
	} else {
		slog.Info("bilibili live status checked",
			"channel_id", ctx.Value(liveRecordChannelIDKey),
			"room_id", room.RoomID,
			"live", live)
	}
	return LiveInfo{
		RoomID:    room.RoomID,
		Live:      live,
		Title:     title,
		Cover:     room.Cover,
		StartedAt: startedAt,
	}, nil
}

func (c *BilibiliClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	cookieHeader = c.injectAntiRisk(ctx, cookieHeader)
	var response playInfoResponse
	endpoint := c.baseURL + "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=" +
		url.QueryEscape(strconv.FormatInt(roomID, 10)) + "&protocol=0,1&format=0,1,2&codec=0,1&qn=10000&platform=web"
	endpoint = c.signURL(endpoint, cookieHeader)
	if err := c.getJSON(ctx, endpoint, &response, cookieHeader, roomID); err != nil {
		return StreamInfo{}, err
	}
	if response.Code != 0 {
		return StreamInfo{}, fmt.Errorf("bilibili play info error: code=%d message=%s", response.Code, response.Message)
	}

	var audioCandidates []streamCandidate
	var fallbackCandidates []streamCandidate

	for _, stream := range response.Data.PlayURLInfo.PlayURL.Stream {
		for _, format := range stream.Format {
			for _, codec := range format.Codec {
				for _, urlInfo := range codec.URLInfo {
					if urlInfo.Host == "" || codec.BaseURL == "" {
						continue
					}
					fullURL := urlInfo.Host + codec.BaseURL + urlInfo.Extra
					isAudioOnly := isAudioCodec(codec.CodecName)
					entry := streamCandidate{
						url:       fullURL,
						codecName: codec.CodecName,
						priority:  streamPriority(format.FormatName, codec.CodecName, codec.BaseURL),
					}
					if isAudioOnly {
						audioCandidates = append(audioCandidates, entry)
					}
					fallbackCandidates = append(fallbackCandidates, entry)
				}
			}
		}
	}

	if audioOnly {
		if len(audioCandidates) > 0 {
			selected := bestCandidate(audioCandidates)
			return StreamInfo{
				URL:       selected.url,
				AudioOnly: true,
				Headers:   bilibiliStreamHeaders(cookieHeader),
			}, nil
		}
		// 纯音频流不可用时不自动回退，由调用方决定
		// 收集可用 codec 名称用于错误信息
		available := make([]string, len(fallbackCandidates))
		for i, c := range fallbackCandidates {
			available[i] = c.codecName
		}
		return StreamInfo{}, fmt.Errorf("audio-only stream not found (available codecs: %v)", available)
	}

	if len(fallbackCandidates) == 0 {
		return StreamInfo{}, fmt.Errorf("bilibili stream url not found")
	}
	selected := bestCandidate(fallbackCandidates)
	return StreamInfo{
		URL:       selected.url,
		AudioOnly: false,
		Headers:   bilibiliStreamHeaders(cookieHeader),
	}, nil
}

// isAudioCodec 判断 codec 名称是否为纯音频编码。
func isAudioCodec(name string) bool {
	switch name {
	case "aac", "mp4a", "aac_he", "aac_he_v2", "aac_ld", "aac_eld", "opus", "mp3":
		return true
	}
	return false
}

func bestCandidate(candidates []streamCandidate) streamCandidate {
	best := candidates[0]
	for _, item := range candidates[1:] {
		if item.priority > best.priority {
			best = item
		}
	}
	return best
}

func streamPriority(formatName string, codecName string, baseURL string) int {
	if isAudioCodec(codecName) {
		return 1
	}
	if isFLVStream(baseURL) && codecName == "avc" {
		return 100
	}
	if isFLVStream(baseURL) {
		return 90
	}
	if formatName == "flv" && codecName == "avc" {
		return 80
	}
	if formatName == "flv" {
		return 70
	}
	if codecName == "avc" {
		return 60
	}
	return 50
}

func isFLVStream(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), ".flv")
}

func bilibiliStreamHeaders(cookieHeader string) map[string]string {
	headers := map[string]string{
		"User-Agent": biliutil.BiliUserAgent,
		"Referer":    "https://live.bilibili.com/",
	}
	if cookieHeader != "" {
		headers["Cookie"] = cookieHeader
	}
	return headers
}

func (c *BilibiliClient) getJSON(ctx context.Context, endpoint string, target any, cookieHeader string, roomID int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", biliutil.BiliUserAgent)
	// Referer/Origin 同步 identify.go 的 header 策略（异常 #7：-352 风控对抗的组成部分）。
	request.Header.Set("Referer", "https://live.bilibili.com/"+strconv.FormatInt(roomID, 10))
	request.Header.Set("Origin", "https://live.bilibili.com")
	if cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("bilibili http status %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func parseBilibiliTime(value int64) time.Time {
	if value <= 0 {
		return time.Now()
	}
	return time.Unix(value, 0)
}

type roomInfoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		RoomInfo struct {
			RoomID        int64  `json:"room_id"`
			LiveStatus    int    `json:"live_status"`
			Title         string `json:"title"`
			Cover         string `json:"cover"` // 直播间封面 URL
			LiveStartTime int64  `json:"live_start_time"`
		} `json:"room_info"`
		AnchorInfo struct {
			BaseInfo struct {
				UName string `json:"uname"`
			} `json:"base_info"`
		} `json:"anchor_info"`
	} `json:"data"`
}

type playInfoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		PlayURLInfo struct {
			PlayURL struct {
				Stream []struct {
					Format []struct {
						FormatName string `json:"format_name"`
						Codec      []struct {
							CodecName string `json:"codec_name"`
							BaseURL   string `json:"base_url"`
							URLInfo   []struct {
								Host  string `json:"host"`
								Extra string `json:"extra"`
							} `json:"url_info"`
						} `json:"codec"`
					} `json:"format"`
				} `json:"stream"`
			} `json:"playurl"`
		} `json:"playurl_info"`
	} `json:"data"`
}
