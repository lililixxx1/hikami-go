package biliutil

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// DanmakuClient 拉取 B 站 XML 弹幕。
type DanmakuClient struct {
	HTTPClient HTTPDoer
	BaseURL    string
}

// FetchDanmakuXML 拉取指定 cid 的弹幕 XML。
func FetchDanmakuXML(ctx context.Context, cid int64, cookie string) ([]byte, error) {
	return DanmakuClient{}.FetchXML(ctx, cid, cookie)
}

// FetchXML 拉取指定 cid 的弹幕 XML。
func (c DanmakuClient) FetchXML(ctx context.Context, cid int64, cookie string) ([]byte, error) {
	if cid <= 0 {
		return nil, fmt.Errorf("cid is required")
	}
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = biliCommentBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/"+strconv.FormatInt(cid, 10)+".xml", nil)
	if err != nil {
		return nil, fmt.Errorf("create danmaku request: %w", err)
	}
	setBiliHeaders(req, cookie)

	resp, err := httpClientOrDefault(c.HTTPClient).Do(req)
	if err != nil {
		return nil, fmt.Errorf("danmaku request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("danmaku http status %d", resp.StatusCode)
	}
	body, err := readEncodedBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read danmaku response: %w", err)
	}
	return body, nil
}

func readEncodedBody(resp *http.Response) ([]byte, error) {
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch encoding {
	case "deflate":
		zlibReader, err := zlib.NewReader(bytes.NewReader(body))
		if err == nil {
			defer zlibReader.Close()
			return io.ReadAll(zlibReader)
		}
		flateReader := flate.NewReader(bytes.NewReader(body))
		defer flateReader.Close()
		return io.ReadAll(flateReader)
	case "gzip":
		gzipReader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		return io.ReadAll(gzipReader)
	}
	return body, nil
}
