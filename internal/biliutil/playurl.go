package biliutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var (
	// ErrNoAudioStream 表示 playurl 响应中没有可用 DASH 音频流。
	ErrNoAudioStream = errors.New("no audio stream")
	// ErrPlayURLFailed 表示 playurl 请求或解析失败。
	ErrPlayURLFailed = errors.New("playurl failed")
)

// PlayURLClient 调用 B 站 WBI playurl 接口。
type PlayURLClient struct {
	HTTPClient HTTPDoer
	BaseURL    string
	Signer     URLSigner
}

// AudioStream 是 DASH 音频流信息。
type AudioStream struct {
	ID        int      `json:"id"`
	BaseURL   string   `json:"baseUrl"`
	BackupURL []string `json:"backupUrl"`
	Bandwidth int      `json:"bandwidth"`
	MimeType  string   `json:"mimeType"`
	Codecs    string   `json:"codecs"`
}

// URLs 返回主 URL 和按顺序回退的 backup URL。
func (s AudioStream) URLs() []string {
	urls := make([]string, 0, 1+len(s.BackupURL))
	if strings.TrimSpace(s.BaseURL) != "" {
		urls = append(urls, s.BaseURL)
	}
	for _, rawURL := range s.BackupURL {
		if strings.TrimSpace(rawURL) != "" {
			urls = append(urls, rawURL)
		}
	}
	return urls
}

// FetchPlayURL 获取并解析 DASH 音频流列表。
func FetchPlayURL(ctx context.Context, aid int64, cid int64, bvid string, cookie string, signer URLSigner) ([]AudioStream, error) {
	return PlayURLClient{Signer: signer}.Fetch(ctx, aid, cid, bvid, cookie)
}

// Fetch 获取并解析 DASH 音频流列表。
func (c PlayURLClient) Fetch(ctx context.Context, aid int64, cid int64, bvid string, cookie string) ([]AudioStream, error) {
	rawURL, err := c.playURL(aid, cid, bvid)
	if err != nil {
		return nil, err
	}
	signer := c.Signer
	if signer == nil {
		signer = NewWBISigner(cookie)
	}
	signedURL, err := signer.SignURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: sign url: %v", ErrPlayURLFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %v", ErrPlayURLFailed, err)
	}
	setBiliHeaders(req, cookie)

	resp, err := httpClientOrDefault(c.HTTPClient).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %v", ErrPlayURLFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: http status %d", ErrPlayURLFailed, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrPlayURLFailed, err)
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			DASH struct {
				Audio []AudioStream `json:"audio"`
			} `json:"dash"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("%w: parse response: %v", ErrPlayURLFailed, err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("%w: api code %d: %s", ErrPlayURLFailed, result.Code, result.Message)
	}
	if len(result.Data.DASH.Audio) == 0 {
		return nil, ErrNoAudioStream
	}
	return result.Data.DASH.Audio, nil
}

// SelectBestAudioStream 选择 bandwidth 最高的音频流。
func SelectBestAudioStream(streams []AudioStream) (AudioStream, error) {
	var best AudioStream
	found := false
	for _, stream := range streams {
		if len(stream.URLs()) == 0 {
			continue
		}
		if !found || stream.Bandwidth > best.Bandwidth {
			best = stream
			found = true
		}
	}
	if !found {
		return AudioStream{}, ErrNoAudioStream
	}
	return best, nil
}

func (c PlayURLClient) playURL(aid int64, cid int64, bvid string) (string, error) {
	if aid <= 0 {
		return "", fmt.Errorf("%w: aid is required", ErrPlayURLFailed)
	}
	if cid <= 0 {
		return "", fmt.Errorf("%w: cid is required", ErrPlayURLFailed)
	}
	if strings.TrimSpace(bvid) == "" {
		return "", fmt.Errorf("%w: bvid is required", ErrPlayURLFailed)
	}
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = biliAPIBaseURL
	}
	values := url.Values{}
	values.Set("avid", strconv.FormatInt(aid, 10))
	values.Set("cid", strconv.FormatInt(cid, 10))
	values.Set("bvid", bvid)
	values.Set("qn", "127")
	values.Set("fnval", "16")
	values.Set("fnver", "0")
	values.Set("fourk", "1")
	return baseURL + "/x/player/wbi/playurl?" + values.Encode(), nil
}
