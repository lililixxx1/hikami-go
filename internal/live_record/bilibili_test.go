package live_record

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBilibiliClientParsesLiveAndStreamInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xlive/web-room/v1/index/getInfoByRoom":
			_, _ = w.Write([]byte(`{
				"code":0,
				"message":"0",
				"data":{
					"room_info":{
						"room_id":123,
						"live_status":1,
						"title":"直播标题",
						"live_start_time":1770000000
					},
					"anchor_info":{"base_info":{"uname":"主播"}}
				}
			}`))
		case "/xlive/web-room/v2/index/getRoomPlayInfo":
			_, _ = w.Write([]byte(`{
				"code":0,
				"message":"0",
				"data":{
					"playurl_info":{
						"playurl":{
							"stream":[{
								"format":[{
									"codec":[{
										"codec_name":"hevc",
										"base_url":"/live.flv",
										"url_info":[{"host":"https://live.example.com","extra":"?token=1"}]
									}]
								}]
							}]
						}
					}
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	info, err := client.CheckLive(context.Background(), 123, "")
	if err != nil {
		t.Fatalf("check live: %v", err)
	}
	if !info.Live || info.RoomID != 123 || info.Title != "直播标题" {
		t.Fatalf("unexpected live info: %+v", info)
	}

	// 非 audioOnly 模式：应返回混合流
	stream, err := client.GetStream(context.Background(), 123, false, "")
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	if stream.URL != "https://live.example.com/live.flv?token=1" {
		t.Fatalf("stream url = %s", stream.URL)
	}
	if stream.AudioOnly {
		t.Fatalf("expected AudioOnly=false for non-audioOnly request")
	}
	if stream.Headers["Referer"] != "https://live.bilibili.com/" {
		t.Fatalf("missing bilibili referer header: %+v", stream.Headers)
	}

	// audioOnly 模式：hevc 不是音频 codec，应返回错误
	_, err = client.GetStream(context.Background(), 123, true, "")
	if err == nil {
		t.Fatalf("expected error when audioOnly=true with no audio codec, got nil")
	}
}

func TestBilibiliClientPrefersFLVMixedStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "SESSDATA=test" {
			t.Fatalf("cookie header = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"playurl_info":{
					"playurl":{
						"stream":[{
							"format":[{
								"format_name":"ts",
								"codec":[{
									"codec_name":"avc",
									"base_url":"/live.m3u8",
									"url_info":[{"host":"https://live.example.com","extra":"?hls=1"}]
								}]
							},{
								"format_name":"flv",
								"codec":[{
									"codec_name":"avc",
									"base_url":"/live.flv",
									"url_info":[{"host":"https://live.example.com","extra":"?flv=1"}]
								}]
							}]
						}]
					}
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)
	stream, err := client.GetStream(context.Background(), 123, false, "SESSDATA=test")
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	if stream.URL != "https://live.example.com/live.flv?flv=1" {
		t.Fatalf("stream url = %s", stream.URL)
	}
	if stream.Headers["Cookie"] != "SESSDATA=test" {
		t.Fatalf("stream cookie header = %q", stream.Headers["Cookie"])
	}
}

func TestBilibiliClientPrefersAudioCodec(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"code":0,
			"message":"0",
			"data":{
				"playurl_info":{
					"playurl":{
						"stream":[{
							"format":[{
								"codec":[{
									"codec_name":"hevc",
									"base_url":"/video.flv",
									"url_info":[{"host":"https://live.example.com","extra":""}]
								},{
									"codec_name":"aac",
									"base_url":"/audio.m4a",
									"url_info":[{"host":"https://live.example.com","extra":""}]
								}]
							}]
						}]
					}
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewBilibiliClientWithBaseURL(server.URL)

	// audioOnly=true：应选择 aac 纯音频流
	stream, err := client.GetStream(context.Background(), 123, true, "")
	if err != nil {
		t.Fatalf("get audio stream: %v", err)
	}
	if stream.URL != "https://live.example.com/audio.m4a" {
		t.Fatalf("expected audio stream url, got %s", stream.URL)
	}
	if !stream.AudioOnly {
		t.Fatalf("expected AudioOnly=true")
	}

	// audioOnly=false：应选择第一个可用流
	stream, err = client.GetStream(context.Background(), 123, false, "")
	if err != nil {
		t.Fatalf("get mixed stream: %v", err)
	}
	if stream.AudioOnly {
		t.Fatalf("expected AudioOnly=false for non-audioOnly request")
	}
}
