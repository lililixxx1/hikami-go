package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/config"
)

type IdentifyInput struct {
	Input      string `json:"input"`
	UID        int64  `json:"uid"`
	LiveRoomID int64  `json:"live_room_id"`
}

type IdentifyResult struct {
	Channel UpsertInput `json:"channel"`
	Source  string      `json:"source"`
}

type IdentifySaveResult struct {
	Channel Channel `json:"channel"`
	Source  string  `json:"source"`
	Created bool    `json:"created"`
}

type Identifier struct {
	httpClient *http.Client
	baseURL    string
	cookieFile identifyCookieFileProvider
}

func NewIdentifier() *Identifier {
	return &Identifier{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    "https://api.live.bilibili.com",
	}
}

func NewIdentifierWithChannelStore(store *Store) *Identifier {
	identifier := NewIdentifier()
	identifier.cookieFile = downloadCookieFileProvider(store, nil)
	return identifier
}

func NewIdentifierWithChannelStoreAndBootstrap(store *Store, bootstrap []config.BootstrapChannel) *Identifier {
	identifier := NewIdentifier()
	identifier.cookieFile = downloadCookieFileProvider(store, bootstrap)
	return identifier
}

func NewIdentifierWithBaseURL(baseURL string) *Identifier {
	identifier := NewIdentifier()
	identifier.baseURL = baseURL
	return identifier
}

func (i *Identifier) Identify(ctx context.Context, input IdentifyInput) (IdentifyResult, error) {
	normalized, source, err := normalizeIdentifyInput(input)
	if err != nil {
		return IdentifyResult{}, err
	}
	cookieFile := i.downloadCookieFile(ctx, normalized)
	cookieHeader := cookieHeaderForFile(cookieFile)
	var result IdentifyResult
	if normalized.LiveRoomID > 0 {
		result, err = i.identifyByRoom(ctx, normalized.LiveRoomID, source, cookieHeader)
	} else if normalized.UID > 0 {
		result, err = i.identifyByUID(ctx, normalized.UID, source, cookieHeader)
	} else {
		return IdentifyResult{}, fmt.Errorf("%w: uid or live_room_id is required", ErrInvalid)
	}
	if err != nil {
		return IdentifyResult{}, err
	}
	if result.Channel.DownloadCookieFile == "" {
		result.Channel.DownloadCookieFile = cookieFile
	}
	return result, nil
}

func (i *Identifier) identifyByUID(ctx context.Context, uid int64, source string, cookieHeader string) (IdentifyResult, error) {
	roomID, err := i.liveRoomIDByUID(ctx, uid, cookieHeader)
	if err != nil {
		return IdentifyResult{}, err
	}
	if roomID > 0 {
		return i.identifyByRoom(ctx, roomID, source, cookieHeader)
	}
	return IdentifyResult{
		Source: source,
		Channel: UpsertInput{
			ID:              fmt.Sprintf("bili_%d", uid),
			Name:            fmt.Sprintf("B站用户 %d", uid),
			UID:             uid,
			LiveRoomID:      0,
			SpaceURL:        fmt.Sprintf("https://space.bilibili.com/%d", uid),
			ReplaySourceURL: fmt.Sprintf("https://space.bilibili.com/%d/video", uid),
			TitlePrefix:     "【直播回放】",
			Enabled:         true,
		},
	}, nil
}

func (i *Identifier) identifyByRoom(ctx context.Context, roomID int64, source string, cookieHeader string) (IdentifyResult, error) {
	var response roomInfoResponse
	endpoint := i.baseURL + "/xlive/web-room/v1/index/getInfoByRoom?room_id=" + url.QueryEscape(strconv.FormatInt(roomID, 10))
	if err := i.getJSON(ctx, endpoint, &response, cookieHeader); err != nil {
		return IdentifyResult{}, err
	}
	if response.Code != 0 {
		return IdentifyResult{}, fmt.Errorf("bilibili room info error: code=%d message=%s", response.Code, response.Message)
	}
	uid := response.Data.RoomInfo.UID
	if uid == 0 {
		uid = response.Data.AnchorInfo.BaseInfo.UID
	}
	name := response.Data.AnchorInfo.BaseInfo.UName
	if name == "" {
		name = response.Data.RoomInfo.Title
	}
	if uid <= 0 || name == "" {
		return IdentifyResult{}, fmt.Errorf("bilibili room info missing uid or name")
	}
	actualRoomID := response.Data.RoomInfo.RoomID
	if actualRoomID <= 0 {
		actualRoomID = roomID
	}
	return IdentifyResult{
		Source: source,
		Channel: UpsertInput{
			ID:              fmt.Sprintf("bili_%d", uid),
			Name:            name,
			UID:             uid,
			LiveRoomID:      actualRoomID,
			SpaceURL:        fmt.Sprintf("https://space.bilibili.com/%d", uid),
			ReplaySourceURL: fmt.Sprintf("https://space.bilibili.com/%d/video", uid),
			TitlePrefix:     "【直播回放】",
			Enabled:         true,
		},
	}, nil
}

func (i *Identifier) liveRoomIDByUID(ctx context.Context, uid int64, cookieHeader string) (int64, error) {
	var response roomInfoOldResponse
	endpoint := i.baseURL + "/room/v1/Room/getRoomInfoOld?mid=" + url.QueryEscape(strconv.FormatInt(uid, 10))
	if err := i.getJSON(ctx, endpoint, &response, cookieHeader); err != nil {
		return 0, err
	}
	if response.Code != 0 {
		return 0, fmt.Errorf("bilibili room lookup error: code=%d message=%s", response.Code, response.Message)
	}
	return response.Data.RoomID, nil
}

func (i *Identifier) getJSON(ctx context.Context, endpoint string, target any, cookieHeader string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 Hikami-Go")
	if cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	}
	response, err := i.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("bilibili http status %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

type identifyCookieFileProvider func(context.Context, IdentifyInput) string

func (i *Identifier) downloadCookieFile(ctx context.Context, input IdentifyInput) string {
	if i.cookieFile == nil {
		return ""
	}
	return i.cookieFile(ctx, input)
}

func cookieHeaderForFile(cookieFile string) string {
	if cookieFile == "" {
		return ""
	}
	cookie, err := biliutil.LoadCookie(cookieFile)
	if err != nil {
		slog.Warn("load download cookie for identify failed", "cookie_file", cookieFile, "error", err)
		return ""
	}
	return cookie.CookieHeader()
}

func downloadCookieFileProvider(store *Store, bootstrap []config.BootstrapChannel) identifyCookieFileProvider {
	return func(ctx context.Context, input IdentifyInput) string {
		var cookieFile string
		if store != nil {
			channels, err := store.List(ctx)
			if err != nil {
				slog.Warn("list channels for identify cookie failed", "error", err)
			} else {
				cookieFile = identifyCookieFile(channels, input)
			}
		}
		if cookieFile == "" {
			cookieFile = identifyBootstrapCookieFile(bootstrap, input)
		}
		return cookieFile
	}
}

func identifyCookieFile(channels []Channel, input IdentifyInput) string {
	var fallback string
	for _, item := range channels {
		if item.DownloadCookieFile == "" {
			continue
		}
		if fallback == "" {
			fallback = item.DownloadCookieFile
		}
		if input.UID > 0 && (item.UID == input.UID || item.ID == fmt.Sprintf("bili_%d", input.UID)) {
			return item.DownloadCookieFile
		}
		if input.LiveRoomID > 0 && item.LiveRoomID == input.LiveRoomID {
			return item.DownloadCookieFile
		}
	}
	return fallback
}

func identifyBootstrapCookieFile(channels []config.BootstrapChannel, input IdentifyInput) string {
	var fallback string
	for _, item := range channels {
		if item.DownloadCookieFile == "" {
			continue
		}
		if fallback == "" {
			fallback = item.DownloadCookieFile
		}
		if input.UID > 0 && (item.UID == input.UID || item.ID == fmt.Sprintf("bili_%d", input.UID)) {
			return item.DownloadCookieFile
		}
		if input.LiveRoomID > 0 && item.LiveRoomID == input.LiveRoomID {
			return item.DownloadCookieFile
		}
	}
	return fallback
}

func normalizeIdentifyInput(input IdentifyInput) (IdentifyInput, string, error) {
	if input.LiveRoomID > 0 || input.UID > 0 {
		return input, "explicit", nil
	}
	value := strings.TrimSpace(input.Input)
	if value == "" {
		return input, "", fmt.Errorf("%w: input is required", ErrInvalid)
	}
	if roomID, ok := parseLiveURL(value); ok {
		input.LiveRoomID = roomID
		return input, "live_url", nil
	}
	if uid, ok := parseSpaceURL(value); ok {
		input.UID = uid
		return input, "space_url", nil
	}
	if numeric, err := strconv.ParseInt(value, 10, 64); err == nil && numeric > 0 {
		input.UID = numeric
		return input, "uid", nil
	}
	return input, "", fmt.Errorf("%w: unsupported bilibili input", ErrInvalid)
}

var liveURLPattern = regexp.MustCompile(`live\.bilibili\.com/(?:blanc/)?(\d+)`)
var spaceURLPattern = regexp.MustCompile(`space\.bilibili\.com/(\d+)`)

func parseLiveURL(value string) (int64, bool) {
	matches := liveURLPattern.FindStringSubmatch(value)
	if len(matches) != 2 {
		return 0, false
	}
	id, err := strconv.ParseInt(matches[1], 10, 64)
	return id, err == nil && id > 0
}

func parseSpaceURL(value string) (int64, bool) {
	matches := spaceURLPattern.FindStringSubmatch(value)
	if len(matches) != 2 {
		return 0, false
	}
	id, err := strconv.ParseInt(matches[1], 10, 64)
	return id, err == nil && id > 0
}

type roomInfoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		RoomInfo struct {
			UID    int64  `json:"uid"`
			RoomID int64  `json:"room_id"`
			Title  string `json:"title"`
		} `json:"room_info"`
		AnchorInfo struct {
			BaseInfo struct {
				UID   int64  `json:"uid"`
				UName string `json:"uname"`
			} `json:"base_info"`
		} `json:"anchor_info"`
	} `json:"data"`
}

type roomInfoOldResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		RoomID int64 `json:"roomid"`
	} `json:"data"`
}
