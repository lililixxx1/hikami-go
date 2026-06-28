package biliutil

import (
	"bytes"
	"compress/zlib"
	"context"
	"net/http"
	"testing"
)

func TestDanmakuClientFetchXML(t *testing.T) {
	const xml = `<i><d p="1,1,25,16777215,1,0,hash,1">hello</d></i>`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/456.xml" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != "SESSDATA=sess" {
			t.Fatalf("Cookie = %q", got)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(xml))
	})

	got, err := (DanmakuClient{HTTPClient: mockHTTPDoer(handler), BaseURL: "https://comment.test"}).FetchXML(context.Background(), 456, "SESSDATA=sess")
	if err != nil {
		t.Fatalf("FetchXML: %v", err)
	}
	if string(got) != xml {
		t.Fatalf("xml = %q", got)
	}
}

func TestDanmakuClientFetchXMLDeflateZlib(t *testing.T) {
	const xml = `<i><d p="1,1,25,16777215,1,0,hash,1">deflate</d></i>`
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write([]byte(xml)); err != nil {
		t.Fatalf("write zlib: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zlib: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "deflate")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(compressed.Bytes())
	})

	got, err := (DanmakuClient{HTTPClient: mockHTTPDoer(handler), BaseURL: "https://comment.test"}).FetchXML(context.Background(), 456, "")
	if err != nil {
		t.Fatalf("FetchXML: %v", err)
	}
	if string(got) != xml {
		t.Fatalf("xml = %q, want %q", got, xml)
	}
}
