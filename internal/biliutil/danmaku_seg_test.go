package biliutil

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeDanmakuSegMultiElemAndUnknownFields(t *testing.T) {
	first := testSegElem(
		testProtoVarint(1, 1001),
		testProtoVarint(2, 12345),
		testProtoVarint(3, 1),
		testProtoVarint(4, 25),
		testProtoVarint(5, 16777215),
		testProtoBytes(6, []byte("hash1")),
		testProtoBytes(7, []byte("hello")),
		testProtoVarint(8, 1710000000),
		testProtoVarint(9, 99),
		testProtoBytes(10, []byte("ignored action")),
		testProtoVarint(11, 0),
		testProtoVarint(99, 1),
		testProtoBytes(100, []byte("skip")),
	)
	second := testSegElem(
		testProtoVarint(1, 1002),
		testProtoVarint(2, 2000),
		testProtoVarint(3, 5),
		testProtoVarint(4, 30),
		testProtoVarint(5, 255),
		testProtoBytes(6, []byte("hash2")),
		testProtoBytes(7, []byte("top")),
		testProtoVarint(8, 1710000010),
		testProtoVarint(11, 1),
	)
	data := append(testProtoVarint(2, 123), testProtoBytes(1, first)...)
	data = append(data, testProtoBytes(3, []byte("outer skip"))...)
	data = append(data, testProtoBytes(1, second)...)

	got, err := DecodeDanmakuSeg(data)
	if err != nil {
		t.Fatalf("DecodeDanmakuSeg: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != 1001 || got[0].Progress != 12345 || got[0].Content != "hello" || got[0].Pool != 0 {
		t.Fatalf("first elem = %+v", got[0])
	}
	if got[1].ID != 1002 || got[1].Mode != 5 || got[1].FontSize != 30 || got[1].Color != 255 || got[1].Pool != 1 {
		t.Fatalf("second elem = %+v", got[1])
	}
}

func TestDecodeDanmakuSegTruncatedReturnsPartialAndError(t *testing.T) {
	valid := testProtoBytes(1, testSegElem(
		testProtoVarint(1, 1),
		testProtoBytes(7, []byte("ok")),
	))
	data := append(valid, byte(0x0a), byte(0x80))

	got, err := DecodeDanmakuSeg(data)
	if !errors.Is(err, ErrSegDanmakuDecodeFailed) {
		t.Fatalf("err = %v, want ErrSegDanmakuDecodeFailed", err)
	}
	if len(got) != 1 || got[0].Content != "ok" {
		t.Fatalf("partial elems = %+v", got)
	}
}

func TestDecodeDanmakuSegLengthDelimitedBoundary(t *testing.T) {
	data := testProtoBytes(1, []byte{byte(7<<3 | 2), 5, 'a'})
	_, err := DecodeDanmakuSeg(data)
	if !errors.Is(err, ErrSegDanmakuDecodeFailed) {
		t.Fatalf("err = %v, want ErrSegDanmakuDecodeFailed", err)
	}
}

func TestDecodeDanmakuSegVarintOverflow(t *testing.T) {
	overflow := []byte{byte(1 << 3), 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}
	data := testProtoBytes(1, overflow)

	_, err := DecodeDanmakuSeg(data)
	if !errors.Is(err, ErrSegDanmakuDecodeFailed) {
		t.Fatalf("err = %v, want ErrSegDanmakuDecodeFailed", err)
	}
}

func TestDecodeDanmakuSegSkipsUnknownFixedFields(t *testing.T) {
	data := testSegReply(testSegElem(
		testProtoVarint(1, 1001),
		testProtoFixed64(99, 123),
		testProtoBytes(7, []byte("hello")),
		testProtoFixed32(100, 456),
	))

	got, err := DecodeDanmakuSeg(data)
	if err != nil {
		t.Fatalf("DecodeDanmakuSeg: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1001 || got[0].Content != "hello" {
		t.Fatalf("elems = %+v", got)
	}
}

func TestSegDanmakuToXMLValidWithEscapedMidHash(t *testing.T) {
	got := segDanmakuToXML([]SegDanmaku{
		{
			ID:       11,
			Progress: 1000,
			Mode:     1,
			FontSize: 25,
			Color:    16777215,
			MidHash:  `hash"&bad`,
			Content:  "hello",
			CTime:    1710000000,
		},
	})
	var parsed struct {
		D []struct {
			P    string `xml:"p,attr"`
			Text string `xml:",chardata"`
		} `xml:"d"`
	}
	if err := xml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("xml.Unmarshal: %v, xml=%s", err, got)
	}
	if len(parsed.D) != 1 {
		t.Fatalf("len = %d, want 1", len(parsed.D))
	}
	if parsed.D[0].P != `1.000,1,25,16777215,1710000000,0,hash"&bad,11` {
		t.Fatalf("p = %q", parsed.D[0].P)
	}
}

func TestSegDanmakuClientFetchSegmentsToXMLAndEmptyStop(t *testing.T) {
	var segments []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/v2/dm/web/seg.so" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "1" || r.URL.Query().Get("oid") != "456" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if got := r.Header.Get("Cookie"); got != "SESSDATA=sess" {
			t.Fatalf("Cookie = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != BrowserUA {
			t.Fatalf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Referer"); got != biliReferer {
			t.Fatalf("Referer = %q", got)
		}
		segments = append(segments, r.URL.Query().Get("segment_index"))
		switch r.URL.Query().Get("segment_index") {
		case "1":
			_, _ = w.Write(testSegReply(testSegElem(
				testProtoVarint(1, 11),
				testProtoVarint(2, 12345),
				testProtoVarint(3, 1),
				testProtoVarint(4, 25),
				testProtoVarint(5, 16777215),
				testProtoBytes(6, []byte("hash")),
				testProtoBytes(7, []byte("a&b<c>d")),
				testProtoVarint(8, 1710000000),
				testProtoVarint(11, 0),
			)))
		case "2":
			_, _ = w.Write(testSegReply(testSegElem(
				testProtoVarint(1, 12),
				testProtoVarint(2, 1000),
				testProtoVarint(3, 4),
				testProtoVarint(4, 18),
				testProtoVarint(5, 255),
				testProtoBytes(6, []byte("hash2")),
				testProtoBytes(7, []byte("bottom")),
				testProtoVarint(8, 1710000001),
				testProtoVarint(11, 1),
			)))
		case "3":
			_, _ = w.Write([]byte{})
		default:
			t.Fatalf("unexpected segment_index: %s", r.URL.Query().Get("segment_index"))
		}
	})

	got, err := (SegDanmakuClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
	}).FetchSegments(context.Background(), 456, "SESSDATA=sess")
	if err != nil {
		t.Fatalf("FetchSegments: %v", err)
	}
	want := `<i><d p="12.345,1,25,16777215,1710000000,0,hash,11">a&amp;b&lt;c&gt;d</d><d p="1.000,4,18,255,1710000001,1,hash2,12">bottom</d></i>`
	if string(got) != want {
		t.Fatalf("xml = %q, want %q", got, want)
	}
	if strings.Join(segments, ",") != "1,2,3" {
		t.Fatalf("segments = %v", segments)
	}
}

func TestSegDanmakuClientFetchSegmentsStopsOnAPICode(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":-404,"message":"not found"}`))
	})

	got, err := (SegDanmakuClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
	}).FetchSegments(context.Background(), 456, "")
	if err != nil {
		t.Fatalf("FetchSegments: %v", err)
	}
	if string(got) != "<i></i>" {
		t.Fatalf("xml = %q", got)
	}
}

func TestSegDanmakuClientFetchSegmentsLimit(t *testing.T) {
	var count int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		index, err := strconv.Atoi(r.URL.Query().Get("segment_index"))
		if err != nil {
			t.Fatalf("segment_index: %v", err)
		}
		_, _ = w.Write(testSegReply(testSegElem(
			testProtoVarint(1, uint64(index)),
			testProtoVarint(2, uint64(index*1000)),
			testProtoBytes(7, []byte("x")),
		)))
	})

	got, err := (SegDanmakuClient{
		HTTPClient: mockHTTPDoer(handler),
		BaseURL:    "https://api.test",
	}).FetchSegments(context.Background(), 456, "")
	if err != nil {
		t.Fatalf("FetchSegments: %v", err)
	}
	if count != maxSegDanmakuSegments {
		t.Fatalf("count = %d, want %d", count, maxSegDanmakuSegments)
	}
	if strings.Count(string(got), "<d ") != maxSegDanmakuSegments {
		t.Fatalf("xml item count = %d", strings.Count(string(got), "<d "))
	}
}

func testSegReply(elems ...[]byte) []byte {
	var buf bytes.Buffer
	for _, elem := range elems {
		buf.Write(testProtoBytes(1, elem))
	}
	return buf.Bytes()
}

func testSegElem(fields ...[]byte) []byte {
	var buf bytes.Buffer
	for _, field := range fields {
		buf.Write(field)
	}
	return buf.Bytes()
}

func testProtoVarint(field int, value uint64) []byte {
	var buf bytes.Buffer
	buf.Write(testVarint(uint64(field<<3 | 0)))
	buf.Write(testVarint(value))
	return buf.Bytes()
}

func testProtoBytes(field int, value []byte) []byte {
	var buf bytes.Buffer
	buf.Write(testVarint(uint64(field<<3 | 2)))
	buf.Write(testVarint(uint64(len(value))))
	buf.Write(value)
	return buf.Bytes()
}

func testProtoFixed64(field int, value uint64) []byte {
	var buf bytes.Buffer
	buf.Write(testVarint(uint64(field<<3 | 1)))
	for i := 0; i < 8; i++ {
		buf.WriteByte(byte(value >> (8 * i)))
	}
	return buf.Bytes()
}

func testProtoFixed32(field int, value uint32) []byte {
	var buf bytes.Buffer
	buf.Write(testVarint(uint64(field<<3 | 5)))
	for i := 0; i < 4; i++ {
		buf.WriteByte(byte(value >> (8 * i)))
	}
	return buf.Bytes()
}

func testVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}
