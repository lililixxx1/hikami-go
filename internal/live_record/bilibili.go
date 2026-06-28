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
	"time"

	"hikami-go/internal/biliutil"
)

type BilibiliClient struct {
	httpClient *http.Client
	baseURL    string
}

type streamCandidate struct {
	url       string
	codecName string
	priority  int
}

func NewBilibiliClient() *BilibiliClient {
	return &BilibiliClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    "https://api.live.bilibili.com",
	}
}

func NewBilibiliClientWithBaseURL(baseURL string) *BilibiliClient {
	client := NewBilibiliClient()
	client.baseURL = baseURL
	return client
}

func (c *BilibiliClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (LiveInfo, error) {
	var response roomInfoResponse
	endpoint := c.baseURL + "/xlive/web-room/v1/index/getInfoByRoom?room_id=" + url.QueryEscape(strconv.FormatInt(roomID, 10))
	if err := c.getJSON(ctx, endpoint, &response, cookieHeader); err != nil {
		return LiveInfo{}, err
	}
	if response.Code != 0 {
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
		StartedAt: startedAt,
	}, nil
}

func (c *BilibiliClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (StreamInfo, error) {
	var response playInfoResponse
	endpoint := c.baseURL + "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=" +
		url.QueryEscape(strconv.FormatInt(roomID, 10)) + "&protocol=0,1&format=0,1,2&codec=0,1&qn=10000&platform=web"
	if err := c.getJSON(ctx, endpoint, &response, cookieHeader); err != nil {
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

func (c *BilibiliClient) getJSON(ctx context.Context, endpoint string, target any, cookieHeader string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", biliutil.BiliUserAgent)
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
