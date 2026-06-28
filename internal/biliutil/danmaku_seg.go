package biliutil

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxSegDanmakuSegments = 200

var (
	// ErrSegDanmakuDecodeFailed 表示 seg.so protobuf 弹幕解码失败。
	ErrSegDanmakuDecodeFailed = errors.New("seg danmaku decode failed")
	// ErrSegDanmakuFetchFailed 表示 seg.so 弹幕拉取失败。
	ErrSegDanmakuFetchFailed = errors.New("seg danmaku fetch failed")
)

// SegDanmaku 是 seg.so 解码出的单条弹幕。
type SegDanmaku struct {
	ID       int64
	Progress int64
	Mode     int32
	FontSize int32
	Color    uint32
	MidHash  string
	Content  string
	CTime    int64
	Pool     int32
}

// SegDanmakuClient 拉取 B 站 seg.so protobuf 弹幕。
type SegDanmakuClient struct {
	HTTPClient HTTPDoer
	BaseURL    string
}

// DecodeDanmakuSeg 手写解码 DmSegMobileReply protobuf。
// 截断或非法 wire 数据会返回已完整解析的弹幕片段，并同时返回 ErrSegDanmakuDecodeFailed。
func DecodeDanmakuSeg(data []byte) ([]SegDanmaku, error) {
	reader := protoReader{data: data}
	elems := make([]SegDanmaku, 0)
	for !reader.done() {
		field, wireType, err := reader.readTag()
		if err != nil {
			return elems, fmt.Errorf("%w: read reply tag: %v", ErrSegDanmakuDecodeFailed, err)
		}
		if field == 1 && wireType == 2 {
			raw, err := reader.readBytes()
			if err != nil {
				return elems, fmt.Errorf("%w: read elem: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem, err := decodeDanmakuElem(raw)
			if err != nil {
				return elems, err
			}
			elems = append(elems, elem)
			continue
		}
		if err := reader.skip(wireType); err != nil {
			return elems, fmt.Errorf("%w: skip reply field %d: %v", ErrSegDanmakuDecodeFailed, field, err)
		}
	}
	return elems, nil
}

// FetchSegments 分页拉取 seg.so，并返回合并后的 B 站 XML 弹幕。
// segment_index 从 1 递增，空页、404、API code -404/-796 会停止；最多拉取 200 段防止失控。
func (c SegDanmakuClient) FetchSegments(ctx context.Context, cid int64, cookie string) ([]byte, error) {
	if cid <= 0 {
		return nil, fmt.Errorf("%w: cid is required", ErrSegDanmakuFetchFailed)
	}

	all := make([]SegDanmaku, 0)
	for index := 1; index <= maxSegDanmakuSegments; index++ {
		elems, stop, err := c.fetchSegment(ctx, cid, cookie, index)
		if err != nil {
			return nil, err
		}
		if stop || len(elems) == 0 {
			break
		}
		all = append(all, elems...)
	}
	return segDanmakuToXML(all), nil
}

func (c SegDanmakuClient) fetchSegment(ctx context.Context, cid int64, cookie string, index int) ([]SegDanmaku, bool, error) {
	endpoint := c.segmentURL(cid, index)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, fmt.Errorf("%w: create request: %v", ErrSegDanmakuFetchFailed, err)
	}
	setBiliHeaders(req, cookie)

	resp, err := httpClientOrDefault(c.HTTPClient).Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("%w: request: %v", ErrSegDanmakuFetchFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotModified {
		// 404/304 视为该分段无弹幕，停止分页（304 兜底：即便 type-first，B 站 CDN 仍可能偶发返回）。
		return nil, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("%w: http status %d", ErrSegDanmakuFetchFailed, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("%w: read response: %v", ErrSegDanmakuFetchFailed, err)
	}
	if code, ok := segAPICode(body); ok {
		if code == -404 || code == -796 {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("%w: api code %d", ErrSegDanmakuFetchFailed, code)
	}
	elems, err := DecodeDanmakuSeg(body)
	if err != nil {
		return nil, false, err
	}
	return elems, len(elems) == 0, nil
}

func (c SegDanmakuClient) segmentURL(cid int64, index int) string {
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = biliAPIBaseURL
	}
	// type 参数须置于首位：B 站 seg.so 对 type 排在末尾（url.Values 字母序 oid/segment_index/type）
	// 的请求，空分段返回 304；type-first 则返回 200 + 空 protobuf。联调验证。
	return fmt.Sprintf("%s/x/v2/dm/web/seg.so?type=1&oid=%d&segment_index=%d", baseURL, cid, index)
}

func segAPICode(body []byte) (int, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return 0, false
	}
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(trimmed, &result); err != nil {
		return 0, false
	}
	return result.Code, true
}

func decodeDanmakuElem(data []byte) (SegDanmaku, error) {
	reader := protoReader{data: data}
	var elem SegDanmaku
	for !reader.done() {
		field, wireType, err := reader.readTag()
		if err != nil {
			return elem, fmt.Errorf("%w: read elem tag: %v", ErrSegDanmakuDecodeFailed, err)
		}
		switch field {
		case 1:
			value, err := reader.readExpectedVarint(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read id: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.ID = int64(value)
		case 2:
			value, err := reader.readExpectedVarint(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read progress: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.Progress = int64(value)
		case 3:
			value, err := reader.readExpectedVarint(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read mode: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.Mode = int32(value)
		case 4:
			value, err := reader.readExpectedVarint(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read fontsize: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.FontSize = int32(value)
		case 5:
			value, err := reader.readExpectedVarint(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read color: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.Color = uint32(value)
		case 6:
			value, err := reader.readExpectedBytes(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read mid hash: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.MidHash = string(value)
		case 7:
			value, err := reader.readExpectedBytes(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read content: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.Content = string(value)
		case 8:
			value, err := reader.readExpectedVarint(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read ctime: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.CTime = int64(value)
		case 11:
			value, err := reader.readExpectedVarint(wireType)
			if err != nil {
				return elem, fmt.Errorf("%w: read pool: %v", ErrSegDanmakuDecodeFailed, err)
			}
			elem.Pool = int32(value)
		default:
			if err := reader.skip(wireType); err != nil {
				return elem, fmt.Errorf("%w: skip elem field %d: %v", ErrSegDanmakuDecodeFailed, field, err)
			}
		}
	}
	return elem, nil
}

func segDanmakuToXML(elems []SegDanmaku) []byte {
	var buf bytes.Buffer
	buf.WriteString("<i>")
	for _, elem := range elems {
		buf.WriteString(`<d p="`)
		buf.WriteString(strconv.FormatFloat(float64(elem.Progress)/1000, 'f', 3, 64))
		buf.WriteByte(',')
		buf.WriteString(strconv.FormatInt(int64(elem.Mode), 10))
		buf.WriteByte(',')
		buf.WriteString(strconv.FormatInt(int64(elem.FontSize), 10))
		buf.WriteByte(',')
		buf.WriteString(strconv.FormatUint(uint64(elem.Color), 10))
		buf.WriteByte(',')
		buf.WriteString(strconv.FormatInt(elem.CTime, 10))
		buf.WriteByte(',')
		buf.WriteString(strconv.FormatInt(int64(elem.Pool), 10))
		buf.WriteByte(',')
		_ = xml.EscapeText(&buf, []byte(elem.MidHash))
		buf.WriteByte(',')
		buf.WriteString(strconv.FormatInt(elem.ID, 10))
		buf.WriteString(`">`)
		_ = xml.EscapeText(&buf, []byte(elem.Content))
		buf.WriteString("</d>")
	}
	buf.WriteString("</i>")
	return buf.Bytes()
}

type protoReader struct {
	data []byte
	pos  int
}

func (r *protoReader) done() bool {
	return r.pos >= len(r.data)
}

func (r *protoReader) readTag() (int, int, error) {
	value, err := r.readVarint()
	if err != nil {
		return 0, 0, err
	}
	field := int(value >> 3)
	wireType := int(value & 0x7)
	if field <= 0 {
		return 0, 0, fmt.Errorf("invalid field %d", field)
	}
	return field, wireType, nil
}

func (r *protoReader) readExpectedVarint(wireType int) (uint64, error) {
	if wireType != 0 {
		return 0, fmt.Errorf("unexpected wire type %d", wireType)
	}
	return r.readVarint()
}

func (r *protoReader) readExpectedBytes(wireType int) ([]byte, error) {
	if wireType != 2 {
		return nil, fmt.Errorf("unexpected wire type %d", wireType)
	}
	return r.readBytes()
}

func (r *protoReader) readVarint() (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if r.pos >= len(r.data) {
			return 0, io.ErrUnexpectedEOF
		}
		b := r.data[r.pos]
		r.pos++
		if shift == 63 && b > 1 {
			return 0, fmt.Errorf("varint overflow")
		}
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("varint overflow")
}

func (r *protoReader) readBytes() ([]byte, error) {
	length, err := r.readVarint()
	if err != nil {
		return nil, err
	}
	if length > uint64(len(r.data)-r.pos) {
		return nil, io.ErrUnexpectedEOF
	}
	start := r.pos
	r.pos += int(length)
	return r.data[start:r.pos], nil
}

func (r *protoReader) skip(wireType int) error {
	switch wireType {
	case 0:
		_, err := r.readVarint()
		return err
	case 1:
		if r.pos+8 > len(r.data) {
			return io.ErrUnexpectedEOF
		}
		r.pos += 8
		return nil
	case 2:
		_, err := r.readBytes()
		return err
	case 5:
		if r.pos+4 > len(r.data) {
			return io.ErrUnexpectedEOF
		}
		r.pos += 4
		return nil
	default:
		return fmt.Errorf("unsupported wire type %d", wireType)
	}
}
