package live_record

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"hikami-go/internal/biliutil"

	"github.com/andybalholm/brotli"
	"github.com/gorilla/websocket"
)

type NoopDanmakuRecorder struct{}

func (NoopDanmakuRecorder) Record(ctx context.Context, roomID int64, outputPath string, cookieHeader string, uid int64) error {
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	<-ctx.Done()
	return nil
}

type BilibiliDanmakuRecorder struct {
	httpClient *http.Client
	dialer     *websocket.Dialer
	baseURL    string
	buvidURL   string

	// 按 cookie 缓存 WBISigner
	signers   map[string]*biliutil.WBISigner
	signersMu sync.Mutex

	// 按 cookie 缓存 buvid3，避免每次弹幕重连都请求指纹接口。
	buvids   map[string]cachedBuvid
	buvidsMu sync.Mutex
}

type cachedBuvid struct {
	value     string
	expiresAt time.Time
}

func NewBilibiliDanmakuRecorder() *BilibiliDanmakuRecorder {
	return &BilibiliDanmakuRecorder{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		dialer:     websocket.DefaultDialer,
		baseURL:    "https://api.live.bilibili.com",
		buvidURL:   "https://api.bilibili.com/x/frontend/finger/spi",
		signers:    make(map[string]*biliutil.WBISigner),
		buvids:     make(map[string]cachedBuvid),
	}
}

// signerForCookie 返回指定 cookie 对应的 WBISigner，按 cookie 字符串懒初始化和缓存。
func (r *BilibiliDanmakuRecorder) signerForCookie(cookie string) *biliutil.WBISigner {
	r.signersMu.Lock()
	defer r.signersMu.Unlock()
	if s, ok := r.signers[cookie]; ok {
		return s
	}
	s := biliutil.NewWBISigner(cookie)
	r.signers[cookie] = s
	return s
}

func (r *BilibiliDanmakuRecorder) Record(ctx context.Context, roomID int64, outputPath string, cookieHeader string, uid int64) error {
	return r.RecordWithStartTime(ctx, roomID, outputPath, cookieHeader, uid, time.Now())
}

func (r *BilibiliDanmakuRecorder) RecordWithStartTime(ctx context.Context, roomID int64, outputPath string, cookieHeader string, uid int64, startedAt time.Time) error {
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	backoff := 2 * time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}

		err := r.recordOnce(ctx, roomID, file, cookieHeader, uid, startedAt)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			slog.Warn("danmaku connection interrupted, reconnecting",
				"room_id", roomID, "backoff", backoff, "error", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (r *BilibiliDanmakuRecorder) recordOnce(ctx context.Context, roomID int64, file io.Writer, cookieHeader string, uid int64, startedAt time.Time) error {
	info, err := r.getDanmuInfo(ctx, roomID, cookieHeader)
	if err != nil {
		return err
	}
	slog.Info("danmaku info resolved", "room_id", roomID, "token_len", len(info.Token), "address_count", len(info.Addresses), "buvid_len", len(info.Buvid))

	addresses := info.Addresses
	if len(addresses) == 0 && info.Address != "" {
		addresses = []string{info.Address}
	}
	if len(addresses) == 0 {
		return fmt.Errorf("danmaku websocket address not found")
	}

	if info.Token == "" {
		slog.Warn("danmaku token is empty, connection may be rejected", "room_id", roomID)
	}

	conn, err := r.connectDanmaku(ctx, shuffledAddresses(addresses), roomID, info.Token, uid, info.CookieHeader, info.Buvid)
	if err != nil {
		return err
	}
	defer conn.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.readLoop(ctx, conn, file, startedAt)
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.BinaryMessage, packPacket(operationHeartbeat, 1, []byte{})); err != nil {
				_ = conn.Close()
				return err
			}
		}
	}
}

func (r *BilibiliDanmakuRecorder) connectDanmaku(ctx context.Context, addresses []string, roomID int64, token string, uid int64, cookieHeader string, buvid string) (*websocket.Conn, error) {
	var lastErr error
	for _, address := range addresses {
		conn, err := r.dialAndAuth(ctx, address, roomID, token, uid, cookieHeader, buvid, 3)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		slog.Warn("danmaku protover 3 auth failed, retrying protover 2",
			"room_id", roomID, "address", address, "error", err)

		conn, err = r.dialAndAuth(ctx, address, roomID, token, uid, cookieHeader, buvid, 2)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		slog.Warn("danmaku websocket host failed",
			"room_id", roomID, "address", address, "error", err)
	}
	if lastErr == nil {
		return nil, fmt.Errorf("danmaku websocket address not found")
	}
	return nil, lastErr
}

func (r *BilibiliDanmakuRecorder) readLoop(ctx context.Context, conn *websocket.Conn, writer io.Writer, startedAt time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		messages, err := unpackMessages(data)
		if err != nil {
			return err
		}
		receivedAt := time.Now()
		for _, raw := range messages {
			item, ok := parseDanmakuMessage(raw, startedAt, receivedAt)
			if !ok {
				continue
			}
			line, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if _, err := writer.Write(append(line, '\n')); err != nil {
				return err
			}
		}
	}
}

func (r *BilibiliDanmakuRecorder) dialAndAuth(ctx context.Context, address string, roomID int64, token string, uid int64, cookieHeader string, buvid string, protover int) (*websocket.Conn, error) {
	header := http.Header{
		"User-Agent": {biliutil.BiliUserAgent},
		"Origin":     {"https://live.bilibili.com"},
		"Referer":    {fmt.Sprintf("https://live.bilibili.com/%d", roomID)},
	}
	if cookieHeader != "" {
		header.Set("Cookie", cookieHeader)
	}

	conn, _, err := r.dialer.DialContext(ctx, address, header)
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, packPacket(operationAuth, 1, buildAuthBodyWithProtover(roomID, token, uid, protover, buvid))); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := waitAuthSuccess(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

type danmuInfo struct {
	Token        string
	Address      string
	Addresses    []string
	Buvid        string
	CookieHeader string
}

func (r *BilibiliDanmakuRecorder) getDanmuInfo(ctx context.Context, roomID int64, cookieHeader string) (danmuInfo, error) {
	signer := r.signerForCookie(cookieHeader)

	query := func() (int, string, *danmuInfo, error) {
		rawURL := r.baseURL + "/xlive/web-room/v1/index/getDanmuInfo?id=" + strconv.FormatInt(roomID, 10) + "&type=0"
		signedURL, err := signer.SignURL(rawURL)
		if err != nil {
			slog.Warn("wbi sign failed, using unsigned URL", "error", err)
			signedURL = rawURL
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
		if err != nil {
			return 0, "", nil, err
		}
		request.Header.Set("User-Agent", biliutil.BiliUserAgent)
		request.Header.Set("Referer", "https://www.bilibili.com")
		request.Header.Set("Origin", "https://www.bilibili.com")
		if cookieHeader != "" {
			request.Header.Set("Cookie", cookieHeader)
		}

		response, err := r.httpClient.Do(request)
		if err != nil {
			return 0, "", nil, err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return 0, "", nil, fmt.Errorf("getDanmuInfo http status %d", response.StatusCode)
		}

		var raw struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Token    string `json:"token"`
				HostList []struct {
					Host    string `json:"host"`
					WSSPort int    `json:"wss_port"`
				} `json:"host_list"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
			return 0, "", nil, err
		}

		var info danmuInfo
		if raw.Code == 0 {
			info.Token = raw.Data.Token
			info.Addresses = make([]string, 0, len(raw.Data.HostList))
			for _, host := range raw.Data.HostList {
				if host.Host == "" || host.WSSPort <= 0 {
					continue
				}
				info.Addresses = append(info.Addresses, fmt.Sprintf("wss://%s:%d/sub", host.Host, host.WSSPort))
			}
			if len(info.Addresses) > 0 {
				info.Address = info.Addresses[0]
			}
		}
		return raw.Code, raw.Message, &info, nil
	}

	code, msg, info, err := query()
	if err != nil {
		return danmuInfo{}, err
	}

	// -352 重试：强制刷新密钥后重试一次
	if code == -352 {
		slog.Warn("danmaku risk control (-352), refreshing WBI keys and retrying", "room_id", roomID)
		_ = signer.RefreshKeys()
		code, msg, info, err = query()
		if err != nil {
			return danmuInfo{}, err
		}
	}

	// 仍 -352：尝试旧版 getConf API
	if code == -352 {
		slog.Warn("danmaku risk control (-352) persists, trying legacy getConf API", "room_id", roomID)
		confInfo, confErr := r.getDanmuConf(ctx, roomID, cookieHeader)
		if confErr == nil && confInfo.Token != "" {
			if len(confInfo.Addresses) == 0 {
				confInfo.Addresses = []string{"wss://broadcastlv.chat.bilibili.com:2245/sub"}
				confInfo.Address = confInfo.Addresses[0]
			}
			return confInfo, nil
		}
		if confErr != nil {
			slog.Warn("danmaku legacy getConf also failed", "room_id", roomID, "error", confErr)
		} else {
			slog.Warn("danmaku legacy getConf returned empty token", "room_id", roomID)
		}

		// 最终降级：默认弹幕服务器（无 token，可能被鉴权拒绝）
		slog.Warn("danmaku falling back to default server without token", "room_id", roomID)
		info := danmuInfo{
			Token:   "",
			Address: "wss://broadcastlv.chat.bilibili.com:2245/sub",
		}
		info.Addresses = []string{info.Address}
		r.fillDanmuBuvid(ctx, &info, cookieHeader)
		return info, nil
	}

	if code != 0 {
		return danmuInfo{}, fmt.Errorf("getDanmuInfo code=%d message=%s", code, msg)
	}

	r.fillDanmuBuvid(ctx, info, cookieHeader)
	return *info, nil
}

// getDanmuConf 通过旧版 getConf API 获取弹幕 token 和服务器地址。
// 该 API 不需要 WBI 签名，可能不受 -352 风控影响。
func (r *BilibiliDanmakuRecorder) getDanmuConf(ctx context.Context, roomID int64, cookieHeader string) (danmuInfo, error) {
	rawURL := r.baseURL + "/room/v1/Danmu/getConf?room_id=" + strconv.FormatInt(roomID, 10) + "&platform=pc&player=web"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return danmuInfo{}, err
	}
	req.Header.Set("User-Agent", biliutil.BiliUserAgent)
	req.Header.Set("Referer", "https://www.bilibili.com")
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return danmuInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return danmuInfo{}, fmt.Errorf("getDanmuConf http status %d", resp.StatusCode)
	}

	var raw struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Token          string `json:"token"`
			Host           string `json:"host"`
			Port           int    `json:"port"`
			HostServerList []struct {
				Host    string `json:"host"`
				WssPort int    `json:"wss_port"`
				WsPort  int    `json:"ws_port"`
			} `json:"host_server_list"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return danmuInfo{}, err
	}
	if raw.Code != 0 {
		return danmuInfo{}, fmt.Errorf("getDanmuConf code=%d msg=%s", raw.Code, raw.Msg)
	}

	var info danmuInfo
	info.Token = raw.Data.Token
	for _, h := range raw.Data.HostServerList {
		if h.Host != "" && h.WssPort > 0 {
			info.Addresses = append(info.Addresses, fmt.Sprintf("wss://%s:%d/sub", h.Host, h.WssPort))
		}
	}
	// 只使用 host_server_list 的 wss_port，避免用非 TLS 端口拼 wss://
	// 地址为空时由调用方补默认地址
	if len(info.Addresses) > 0 {
		info.Address = info.Addresses[0]
	}
	r.fillDanmuBuvid(ctx, &info, cookieHeader)
	return info, nil
}

func (r *BilibiliDanmakuRecorder) fillDanmuBuvid(ctx context.Context, info *danmuInfo, cookieHeader string) {
	info.CookieHeader = cookieHeader

	buvid := cookieValue(cookieHeader, "buvid3")
	if buvid == "" {
		var err error
		buvid, err = r.getBuvidConf(ctx, cookieHeader)
		if err != nil {
			slog.Warn("get buvid3 failed, continuing without buvid", "error", err)
			return
		}
		info.CookieHeader = appendCookie(cookieHeader, "buvid3", buvid)
	}
	info.Buvid = buvid
}

func (r *BilibiliDanmakuRecorder) getBuvidConf(ctx context.Context, cookieHeader string) (string, error) {
	if r.buvidURL == "" {
		return "", nil
	}

	now := time.Now()
	r.buvidsMu.Lock()
	if r.buvids != nil {
		if cached, ok := r.buvids[cookieHeader]; ok && now.Before(cached.expiresAt) {
			r.buvidsMu.Unlock()
			return cached.value, nil
		}
	}
	r.buvidsMu.Unlock()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.buvidURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", biliutil.BiliUserAgent)
	request.Header.Set("Referer", "https://www.bilibili.com")
	request.Header.Set("Origin", "https://www.bilibili.com")
	if cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	}

	response, err := r.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("get buvid3 http status %d", response.StatusCode)
	}

	var raw struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			B3 string `json:"b_3"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return "", err
	}
	if raw.Code != 0 {
		return "", fmt.Errorf("get buvid3 code=%d message=%s", raw.Code, raw.Message)
	}
	if raw.Data.B3 == "" {
		return "", fmt.Errorf("get buvid3 returned empty b_3")
	}

	r.buvidsMu.Lock()
	if r.buvids == nil {
		r.buvids = make(map[string]cachedBuvid)
	}
	r.buvids[cookieHeader] = cachedBuvid{
		value:     raw.Data.B3,
		expiresAt: now.Add(24 * time.Hour),
	}
	r.buvidsMu.Unlock()

	return raw.Data.B3, nil
}

const (
	packetHeaderLength = 16
	operationHeartbeat = 2
	operationMessage   = 5
	operationAuth      = 7
	operationAuthReply = 8
)

func buildAuthBody(roomID int64, token string, uid int64) []byte {
	return buildAuthBodyWithProtover(roomID, token, uid, 3)
}

func buildAuthBodyWithProtover(roomID int64, token string, uid int64, protover int, buvidValue ...string) []byte {
	buvid := ""
	if len(buvidValue) > 0 {
		buvid = buvidValue[0]
	}
	body := map[string]any{
		"uid":      uid,
		"roomid":   roomID,
		"protover": protover,
		"platform": "web",
		"type":     2,
		"key":      token,
		"buvid":    buvid,
	}
	data, _ := json.Marshal(body)
	return data
}

func waitAuthSuccess(conn *websocket.Conn) error {
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{})

	_, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	operation, body, err := parseDanmakuPacket(data)
	if err != nil {
		return err
	}
	if operation != operationAuthReply {
		return fmt.Errorf("unexpected danmaku auth operation %d", operation)
	}

	var reply struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return err
	}
	if reply.Code != 0 {
		return fmt.Errorf("danmaku auth failed code=%d", reply.Code)
	}
	return nil
}

func parseDanmakuPacket(data []byte) (uint32, []byte, error) {
	if len(data) < packetHeaderLength {
		return 0, nil, fmt.Errorf("invalid danmaku packet length")
	}
	packetLength := int(binary.BigEndian.Uint32(data[0:4]))
	headerLength := int(binary.BigEndian.Uint16(data[4:6]))
	if packetLength < headerLength || packetLength > len(data) {
		return 0, nil, fmt.Errorf("invalid danmaku packet length")
	}
	operation := binary.BigEndian.Uint32(data[8:12])
	return operation, data[headerLength:packetLength], nil
}

func shuffledAddresses(addresses []string) []string {
	shuffled := append([]string(nil), addresses...)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	return shuffled
}

func cookieValue(cookieHeader string, name string) string {
	for _, item := range strings.Split(cookieHeader, ";") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) == 2 && parts[0] == name {
			return parts[1]
		}
	}
	return ""
}

func appendCookie(cookieHeader string, name string, value string) string {
	if value == "" || cookieValue(cookieHeader, name) != "" {
		return cookieHeader
	}
	if strings.TrimSpace(cookieHeader) == "" {
		return name + "=" + value
	}
	return cookieHeader + "; " + name + "=" + value
}

func packPacket(operation uint32, protocolVersion uint16, body []byte) []byte {
	length := packetHeaderLength + len(body)
	packet := make([]byte, length)
	binary.BigEndian.PutUint32(packet[0:4], uint32(length))
	binary.BigEndian.PutUint16(packet[4:6], packetHeaderLength)
	binary.BigEndian.PutUint16(packet[6:8], protocolVersion)
	binary.BigEndian.PutUint32(packet[8:12], operation)
	binary.BigEndian.PutUint32(packet[12:16], 1)
	copy(packet[16:], body)
	return packet
}

func unpackMessages(data []byte) ([]json.RawMessage, error) {
	var messages []json.RawMessage
	for len(data) >= packetHeaderLength {
		packetLength := int(binary.BigEndian.Uint32(data[0:4]))
		headerLength := int(binary.BigEndian.Uint16(data[4:6]))
		protocolVersion := binary.BigEndian.Uint16(data[6:8])
		operation := binary.BigEndian.Uint32(data[8:12])
		if packetLength < headerLength || packetLength > len(data) {
			return nil, fmt.Errorf("invalid danmaku packet length")
		}
		body := data[headerLength:packetLength]
		if operation == operationMessage {
			switch protocolVersion {
			case 0, 1:
				messages = append(messages, json.RawMessage(body))
			case 2:
				decompressed, err := zlibInflate(body)
				if err != nil {
					return nil, err
				}
				nested, err := unpackMessages(decompressed)
				if err != nil {
					return nil, err
				}
				messages = append(messages, nested...)
			case 3:
				decompressed, err := brotliInflate(body)
				if err != nil {
					return nil, err
				}
				nested, err := unpackMessages(decompressed)
				if err != nil {
					return nil, err
				}
				messages = append(messages, nested...)
			default:
				return nil, fmt.Errorf("unsupported danmaku protocol version %d", protocolVersion)
			}
		}
		data = data[packetLength:]
	}
	return messages, nil
}

func zlibInflate(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func brotliInflate(data []byte) ([]byte, error) {
	return io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
}

func parseDanmakuMessage(raw json.RawMessage, startedAt time.Time, receivedAt time.Time) (map[string]any, bool) {
	var message struct {
		Command string `json:"cmd"`
		Info    []any  `json:"info"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, false
	}
	if message.Command != "DANMU_MSG" || len(message.Info) < 3 {
		return nil, false
	}
	text, _ := message.Info[1].(string)
	if text == "" {
		return nil, false
	}
	userID, userName := parseUser(message.Info[2])
	color := parseColor(message.Info[0])
	sendAt, hasSendAt := parseSendTime(message.Info[0])
	timeMS := receivedAt.Sub(startedAt).Milliseconds()
	rawTime := receivedAt.Format(time.RFC3339)
	if hasSendAt {
		timeMS = sendAt.Sub(startedAt).Milliseconds()
		rawTime = sendAt.Format(time.RFC3339)
	}
	if timeMS < 0 {
		timeMS = 0
	}
	return map[string]any{
		"time_ms":           timeMS,
		"original_time_ms":  timeMS,
		"corrected_time_ms": timeMS,
		"type":              "danmaku",
		"user_id":           userID,
		"user_name":         userName,
		"text":              text,
		"color":             color,
		"raw_time":          rawTime,
		"received_at":       receivedAt.Format(time.RFC3339),
		"source":            "live_record",
	}, true
}

func parseUser(value any) (string, string) {
	items, ok := value.([]any)
	if !ok || len(items) < 2 {
		return "", ""
	}
	id := ""
	switch v := items[0].(type) {
	case float64:
		id = strconv.FormatInt(int64(v), 10)
	case string:
		id = v
	}
	name, _ := items[1].(string)
	return id, name
}

func parseColor(value any) string {
	items, ok := value.([]any)
	if !ok || len(items) < 4 {
		return ""
	}
	color, ok := items[3].(float64)
	if !ok {
		return ""
	}
	return fmt.Sprintf("#%06x", int(color))
}

func parseSendTime(value any) (time.Time, bool) {
	items, ok := value.([]any)
	if !ok || len(items) < 5 {
		return time.Time{}, false
	}
	var ms int64
	switch v := items[4].(type) {
	case float64:
		ms = int64(v)
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		ms = parsed
	default:
		return time.Time{}, false
	}
	if ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}
