package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hikami-go/internal/biliutil"
	"io"
	"log/slog"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrChannelNoCookieFile = errors.New("channel has no cookie_file configured")
	ErrCookieExpired       = biliutil.ErrCookieExpired
	ErrContentRejected     = errors.New("bilibili content rejected")
	ErrRateLimited         = errors.New("bilibili rate limited")
	ErrNotOwner            = errors.New("can only operate own bilibili dynamic")
	ErrBilibiliAPI         = errors.New("bilibili api error")
)

type OpusClient interface {
	SaveDraft(ctx context.Context, cookie *BiliCookie, req *DraftRequest) (draftID string, err error)
	PublishOpus(ctx context.Context, cookie *BiliCookie, req *PublishRequest) (dynID string, dynType int64, dynRid string, err error)
	DeleteDraft(ctx context.Context, cookie *BiliCookie, draftID string) error
}

type OpusCoverUploader interface {
	UploadCover(ctx context.Context, cookie *BiliCookie, imagePath string) (coverURL string, err error)
}

type BiliOpusClient struct {
	httpClient   *http.Client
	urlSigner    biliutil.URLSigner
	buvidCache   map[string]cachedBuvid
	buvidCacheMu sync.Mutex
}

type cachedBuvid struct {
	buvid3    string
	buvid4    string
	expiresAt time.Time
}

func NewBiliOpusClient() *BiliOpusClient {
	return &BiliOpusClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		buvidCache: make(map[string]cachedBuvid),
	}
}

func NewBiliOpusClientWithSigner(signer biliutil.URLSigner) *BiliOpusClient {
	return &BiliOpusClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		urlSigner:  signer,
		buvidCache: make(map[string]cachedBuvid),
	}
}

type DraftRequest struct {
	Title           string
	Paragraphs      []OpusParagraph
	Summary         string
	CategoryID      int
	ListID          int
	PrivatePub      int
	Original        int
	CoverURL        string
	Aigc            int
	TimerPubTime    int64
	Tags            string
	TopicID         int
	CloseComment    int
	UpChooseComment int
}

type PublishRequest struct {
	Title           string
	Paragraphs      []OpusParagraph
	CategoryID      int
	ListID          int
	PrivatePub      int
	Originality     int
	Reproduced      int
	DraftID         string
	Mid             string
	CoverURL        string
	Aigc            int
	Tags            string
	TopicID         int
	TopicName       string
	TimerPubTime    int64
	CloseComment    int
	UpChooseComment int
}

// OpusParagraph 对齐 B 站当前 opus 编辑器的段落结构（经抓包 draft/add 真实请求确认）。
// 设计为扁平结构：所有文字内容都放在 text.nodes，用 para_type + format 内嵌字段区分类型，
// 连续同类型段落（列表/引用）用 format.combine_hash 关联，而非嵌套 children 容器。
// para_type: 1=文本, 3=分割线, 4=引用, 6=列表, 9=标题
type OpusParagraph struct {
	ParaType int         `json:"para_type"`
	Format   *OpusFormat `json:"format,omitempty"`
	Text     *OpusText   `json:"text,omitempty"`
	Line     *OpusLine   `json:"line,omitempty"`
}

// OpusFormat 承载段落类型相关的附加字段。各字段按段落类型按需填充：
//   - indent: 文本/标题/列表/引用均带（默认零缩进）
//   - heading_type: 标题(para_type=9) 的级别（2=H2, 3=H3）
//   - list_format: 列表(para_type=6) 的层级/序号/主题
//   - combine_hash: 连续列表/引用段落的关联键（同组共享同一值）
type OpusFormat struct {
	Indent      *OpusIndent     `json:"indent,omitempty"`
	HeadingType int             `json:"heading_type,omitempty"`
	ListFormat  *OpusListFormat `json:"list_format,omitempty"`
	CombineHash string          `json:"combine_hash,omitempty"`
}

type OpusIndent struct {
	FirstLineIndent int `json:"first_line_indent"`
	Indent          int `json:"indent"`
}

// OpusListFormat 列表段落（para_type=6）的格式字段。
type OpusListFormat struct {
	Level int    `json:"level"`
	Order int    `json:"order"`
	Theme string `json:"theme"`
}

type OpusText struct {
	Nodes []OpusNode `json:"nodes"`
}

// OpusNode 的 NodeType 必须是整数 1（字段名 node_type）。
// 经抓包 B站官方编辑器 draft/add 真实请求确认（code:0 且 content 正常存储）：
// 文本节点用 "node_type": 1，而非字符串 type。此前误用字符串导致草稿正文空白。
type OpusNode struct {
	NodeType int       `json:"node_type"`
	Word     *OpusWord `json:"word,omitempty"`
}

type OpusWord struct {
	Words     string     `json:"words"`
	FontSize  int        `json:"font_size,omitempty"`
	Color     string     `json:"color,omitempty"`
	DarkColor string     `json:"dark_color,omitempty"`
	Style     *OpusStyle `json:"style,omitempty"`
	FontLevel string     `json:"font_level,omitempty"`
}

type OpusStyle struct {
	Bold          bool   `json:"bold,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
	Background    string `json:"background,omitempty"`
}

type OpusLine struct {
	LineType int `json:"line_type"`
}

type biliResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *BiliOpusClient) SaveDraft(ctx context.Context, cookie *BiliCookie, req *DraftRequest) (string, error) {
	paragraphs := make([]any, len(req.Paragraphs))
	for i, p := range req.Paragraphs {
		paragraphs[i] = p
	}

	body := map[string]any{
		"arg": map[string]any{
			"type":             4,
			"template_id":      1,
			"category_id":      req.CategoryID,
			"title":            req.Title,
			"private_pub":      req.PrivatePub,
			"reprint":          map[int]int{0: 1, 1: 0}[req.Original],
			"original":         req.Original,
			"list_id":          req.ListID,
			"comment_selected": req.UpChooseComment,
			"up_closed_reply":  req.CloseComment,
			"timer_pub_time":   req.TimerPubTime,
			// topic_id: 不写入。经抓包确认(B 站专栏编辑器 draft/add 真实请求),
			// 草稿端无 topic 字段——话题仅在发布时绑定(create/opus 的 opus_req.topic)。
			// 此前写入的 arg.topic_id 是无效字段,会被服务端忽略。
			"only_fans_level": 0,
			"only_fans_dnd":   0,
			"summary":         req.Summary,
			"opus": map[string]any{
				"opus_source": 2,
				"title":       req.Title,
				"content": map[string]any{
					"paragraphs": paragraphs,
				},
				"pub_info": map[string]any{
					"editor_version": "eva3-4.0.0",
				},
				"attachments": map[string]any{
					"is_aigc": req.Aigc,
				},
			},
		},
	}

	// 封面:草稿端字段为 arg.image_urls(字符串数组),经抓包 draft/add 真实请求确认。
	// 此前的 DraftRequest.CoverURL 字段存在但从未写入 JSON,导致封面失效。
	if req.CoverURL != "" {
		body["arg"].(map[string]any)["image_urls"] = []string{req.CoverURL}
	}

	// 注:tags 字段保留在 DraftRequest 以维持调用方兼容,但不再写入请求。
	// 经抓包确认 Opus 专栏编辑器无标签输入框,arg.tags 为无效字段。

	url := "https://api.bilibili.com/x/dynamic/feed/article/draft/add?csrf=" + cookie.BiliJct
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal draft body: %w", err)
	}

	respData, err := c.doRequest(ctx, cookie, url, data)
	if err != nil {
		return "", err
	}

	var result struct {
		ArticleID json.Number `json:"article_id"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return "", fmt.Errorf("parse draft response: %w", err)
	}
	return result.ArticleID.String(), nil
}

func (c *BiliOpusClient) PublishOpus(ctx context.Context, cookie *BiliCookie, req *PublishRequest) (string, int64, string, error) {
	paragraphs := make([]any, len(req.Paragraphs))
	for i, p := range req.Paragraphs {
		paragraphs[i] = p
	}

	uploadID := fmt.Sprintf("%s_%d_%d", req.Mid, time.Now().UnixMilli(), rand.Intn(1000))

	body := map[string]any{
		"raw_content": "",
		"opus_req": map[string]any{
			"upload_id": uploadID,
			"opus": map[string]any{
				"opus_source": 2,
				"title":       req.Title,
				"content": map[string]any{
					"paragraphs": paragraphs,
				},
				"article": map[string]any{
					"category_id": req.CategoryID,
					"list_id":     req.ListID,
					"originality": req.Originality,
					"reproduced":  req.Reproduced,
				},
				"pub_info": map[string]any{
					"editor_version": "eva3-4.0.0",
				},
			},
			"scene": 12,
			"meta": map[string]any{
				"app_meta": map[string]any{
					"from":     "create.creative.h5",
					"mobi_app": "web",
				},
			},
			"option": map[string]any{
				"aigc":              req.Aigc,
				"close_comment":     req.CloseComment,
				"up_choose_comment": req.UpChooseComment,
				"private_pub":       req.PrivatePub,
			},
		},
		"draft_id_str": req.DraftID,
	}

	// timer_pub_time:定时发布时间(Unix 秒)。经抓包 create/opus 真实请求确认,
	// B 站要求在 opus.pub_info 和 option 两处冗余写入。仅定时(>0)时写,立即发布(==0)不写。
	if req.TimerPubTime > 0 {
		pubInfo := body["opus_req"].(map[string]any)["opus"].(map[string]any)["pub_info"].(map[string]any)
		pubInfo["timer_pub_time"] = req.TimerPubTime
		option := body["opus_req"].(map[string]any)["option"].(map[string]any)
		option["timer_pub_time"] = req.TimerPubTime
	}

	// 话题:发布端字段为 opus_req.topic = {id, name}(对象,在 opus_req 顶层),
	// 经抓包 create/opus 真实请求确认。此前写入 option.topic_id 字段名/层级均错误,
	// 服务端忽略。注意 topic_id=0 表示无话题,此情况下不写入 topic 字段。
	if req.TopicID != 0 {
		opusReq := body["opus_req"].(map[string]any)
		opusReq["topic"] = map[string]any{
			"id":   req.TopicID,
			"name": req.TopicName,
		}
	}

	// 封面:发布端字段为 opus_req.opus.article.cover = [{url: "..."}](对象数组),
	// 经抓包 create/opus 真实请求确认。与草稿端 arg.image_urls 结构不同。
	if req.CoverURL != "" {
		article := body["opus_req"].(map[string]any)["opus"].(map[string]any)["article"].(map[string]any)
		article["cover"] = []map[string]any{{"url": req.CoverURL}}
	}

	// 注:tags 字段保留在 PublishRequest 以维持调用方兼容,但不再写入请求。
	// 经抓包确认 Opus 专栏编辑器无标签输入框,option.tags 为无效字段。

	// 构建基础 URL，添加设备指纹参数
	baseURL := "https://api.bilibili.com/x/dynamic/feed/create/opus"
	params := fmt.Sprintf("csrf=%s&gaia_source=main_web&dm_img_list=[]&dm_img_str=V2ViR0wgMS4wIChPcGVuR0wgRVMgMi4wIENocm9taXVtKQ&dm_cover_img_str=QU5HTEUgKEludGVsLCBNZXNhIEludGVsKFIpIFVIRCBHcmFwaGljcw&dm_img_inter=%%7B%%22ds%%22:[]%%2C%%22wh%%22:[5000%%2C5800%%2C100]%%2C%%22of%%22:[200%%2C300%%2C100]%%7D",
		cookie.BiliJct)
	url := baseURL + "?" + params

	data, err := json.Marshal(body)
	if err != nil {
		return "", 0, "", fmt.Errorf("marshal publish body: %w", err)
	}

	respData, err := c.doRequestWithGaia(ctx, cookie, url, data)
	if err != nil {
		return "", 0, "", err
	}

	// dyn_type/dyn_rid 在 create/opus 响应里可能缺失或类型不一（与 create/dyn 不同），
	// 用 RawMessage 容错：dyn_id_str 必有；dyn_rid 可能是 number 或 string。
	// 删除接口 dyn_id_str 必填、dyn_type/rid_str 可选，缺失不影响后续删除。
	var result struct {
		DynIDStr string          `json:"dyn_id_str"`
		DynType  int64           `json:"dyn_type"`
		DynRid   json.RawMessage `json:"dyn_rid"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return "", 0, "", fmt.Errorf("parse publish response: %w", err)
	}
	return result.DynIDStr, result.DynType, rawJSONString(result.DynRid), nil
}

// rawJSONString 将 JSON 原始字节安全转为字符串：字符串去引号、数字/其他原样返回、空值返回 ""。
// 用于容错解析 B 站响应中类型不定的字段（如 dyn_rid 可能是 number 或 string）。
func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func (c *BiliOpusClient) DeleteDraft(ctx context.Context, cookie *BiliCookie, draftID string) error {
	url := "https://api.bilibili.com/x/dynamic/feed/article/draft/del?csrf=" + cookie.BiliJct
	body := map[string]any{
		"article_id": draftID,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal delete body: %w", err)
	}

	_, err = c.doRequest(ctx, cookie, url, data)
	return err
}

// UploadCover 上传封面图到 B 站。经抓包确认(2026-06-22),Opus 类型专栏封面接口为
// /x/dynamic/feed/draw/upload_bfs(multipart form 字段 file_up,响应 data.image_url)。
// 此前使用的 /x/article/creative/article/upcover 是老专栏接口,已切换。
func (c *BiliOpusClient) UploadCover(ctx context.Context, cookie *BiliCookie, imagePath string) (string, error) {
	return c.uploadCoverToURL(ctx, "https://api.bilibili.com", cookie, imagePath)
}

// uploadCoverToURL 是 UploadCover 的可注入 host 版本,供测试用 httptest.Server 覆盖。
func (c *BiliOpusClient) uploadCoverToURL(ctx context.Context, baseURL string, cookie *BiliCookie, imagePath string) (string, error) {
	f, err := os.Open(imagePath)
	if err != nil {
		return "", fmt.Errorf("open cover image: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// 经抓包确认:form 字段名为 file_up(不是 binary)。
	part, err := writer.CreateFormFile("file_up", filepath.Base(imagePath))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("read cover image: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	url := baseURL + "/x/dynamic/feed/draw/upload_bfs?csrf=" + cookie.BiliJct
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", err
	}
	// 与 doRequest/doRequestWithGaia 一致:注入 buvid3+buvid4 以通过风控。
	// getBuvids 失败时降级为不带 buvid(仅 warn),保持与既有请求函数相同容错策略。
	cookieHeader := cookie.CookieHeader()
	buvid3, buvid4, berr := c.getBuvids(ctx, cookieHeader)
	if berr != nil {
		slog.Warn("failed to get buvids for cover upload, continuing without them", "error", berr)
	} else {
		cookieHeader = injectBuvids(cookieHeader, buvid3, buvid4)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("User-Agent", biliutil.BiliUserAgent)
	req.Header.Set("Referer", "https://member.bilibili.com/")
	req.Header.Set("Origin", "https://member.bilibili.com")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read cover response: %w", err)
	}
	var br biliResp
	if err := json.Unmarshal(raw, &br); err != nil {
		return "", fmt.Errorf("parse cover response: %w", err)
	}
	if br.Code != 0 {
		return "", mapBiliError(br.Code, br.Message)
	}

	// 经抓包确认:响应字段为 data.image_url(不是老接口的 data.url)。
	var result struct {
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(br.Data, &result); err != nil {
		return "", fmt.Errorf("parse cover data: %w", err)
	}
	coverURL := result.ImageURL
	if strings.HasPrefix(coverURL, "//") {
		coverURL = "https:" + coverURL
	}
	// 将上传返回的 i0.hdslb.com 等 BFS 域名改写为专栏图床域名 article.biliimg.com。
	// 原因:SaveDraft 时 B站服务端会把上传的 BFS 封面 URL 自动改写为 article.biliimg.com
	// (经 draft/view 读回确认),但 PublishOpus 的 article.cover 不会触发这个改写——
	// 若直接传入 i0.hdslb.com,发布端会静默丢弃封面,导致发布的专栏无封面。
	// 官方编辑器发布时用的正是草稿存储后改写过的 article.biliimg.com URL(经抓包 create/opus 确认)。
	// 两个域名指向同一张 BFS 图,改写后草稿端(image_urls)与发布端(article.cover)行为一致。
	coverURL = normalizeBiliCoverURL(coverURL)
	return coverURL, nil
}

// normalizeBiliCoverURL 把 B站 BFS 上传返回的通用图床域名(i*.hdslb.com)统一为
// 专栏图床域名 article.biliimg.com,与 B站服务端在 SaveDraft 时的改写行为对齐。
// 仅替换 host,保留 scheme/path/query。非 BFS 图床 URL 原样返回。
func normalizeBiliCoverURL(coverURL string) string {
	for _, host := range []string{"i0.hdslb.com", "i1.hdslb.com", "i2.hdslb.com", "i3.hdslb.com"} {
		if strings.Contains(coverURL, "://"+host+"/") {
			return strings.Replace(coverURL, "://"+host+"/", "://article.biliimg.com/", 1)
		}
		if strings.Contains(coverURL, "//"+host+"/") {
			return strings.Replace(coverURL, "//"+host+"/", "//article.biliimg.com/", 1)
		}
	}
	return coverURL
}

func (c *BiliOpusClient) doRequestWithGaia(ctx context.Context, cookie *BiliCookie, url string, body []byte) (json.RawMessage, error) {
	// 获取 buvid3+buvid4 并注入到 Cookie
	cookieHeader := cookie.CookieHeader()
	buvid3, buvid4, err := c.getBuvids(ctx, cookieHeader)
	if err != nil {
		slog.Warn("failed to get buvids, continuing without them", "error", err)
	} else {
		cookieHeader = injectBuvids(cookieHeader, buvid3, buvid4)
		slog.Info("buvids injected into request", "buvid3", buvid3, "buvid4", buvid4)
	}

	// 定义请求执行函数
	doReq := func(gaiaToken string) (json.RawMessage, int, string, string, error) {
		// WBI 签名
		requestURL := url
		if gaiaToken != "" {
			// 如果有 gaia token，添加到 URL
			requestURL = url + "&gaia_vtoken=" + gaiaToken
		}

		if c.urlSigner != nil {
			signedURL, err := c.urlSigner.SignURL(requestURL)
			if err != nil {
				slog.Warn("WBI sign failed, using unsigned URL", "error", err)
			} else {
				requestURL = signedURL
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
		if err != nil {
			return nil, 0, "", "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", cookieHeader)
		req.Header.Set("User-Agent", biliutil.BiliUserAgent)
		req.Header.Set("Referer", "https://member.bilibili.com/")
		req.Header.Set("Origin", "https://member.bilibili.com")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Site", "same-site")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, 0, "", "", err
		}
		defer resp.Body.Close()

		// 提取 gaia voucher（如果有）
		gaiaVoucher := resp.Header.Get("x-bili-gaia-vvoucher")

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, "", "", fmt.Errorf("read bilibili response: %w", err)
		}
		var br biliResp
		if err := json.Unmarshal(raw, &br); err != nil {
			slog.Error("failed to parse bilibili response", "url", requestURL, "status", resp.StatusCode, "body", string(raw))
			return nil, 0, "", "", fmt.Errorf("parse bilibili response: %w", err)
		}
		return br.Data, br.Code, gaiaVoucher, br.Message, nil
	}

	// 首次请求
	data, code, gaiaVoucher, message, err := doReq("")
	if err != nil {
		return nil, err
	}

	// -352 风控：尝试 gaia 验证
	if code == -352 && gaiaVoucher != "" {
		slog.Warn("bilibili risk control (-352), attempting gaia verification", "url", url, "voucher", gaiaVoucher)

		// 调用 gaia 验证流程
		gaiaToken, err := c.performGaiaVerification(ctx, cookie, gaiaVoucher)
		if err != nil {
			slog.Error("gaia verification failed", "error", err)
			return nil, mapBiliError(code, "-352")
		}

		// 使用 gaia token 重试
		slog.Info("gaia verification successful, retrying with token", "token", gaiaToken)
		data, code, _, message, err = doReq(gaiaToken)
		if err != nil {
			return nil, err
		}
	}

	// 检查返回码
	if code != 0 {
		return nil, mapBiliError(code, message)
	}
	return data, nil
}

func (c *BiliOpusClient) doRequest(ctx context.Context, cookie *BiliCookie, url string, body []byte) (json.RawMessage, error) {
	// 获取 buvid3+buvid4 并注入到 Cookie
	cookieHeader := cookie.CookieHeader()
	buvid3, buvid4, err := c.getBuvids(ctx, cookieHeader)
	if err != nil {
		slog.Warn("failed to get buvids, continuing without them", "error", err)
	} else {
		cookieHeader = injectBuvids(cookieHeader, buvid3, buvid4)
		slog.Info("buvids injected into request", "buvid3", buvid3, "buvid4", buvid4)
	}

	// 定义请求执行函数，用于重试
	doReq := func() (json.RawMessage, int, string, error) {
		// WBI 签名
		requestURL := url
		if c.urlSigner != nil {
			signedURL, err := c.urlSigner.SignURL(url)
			if err != nil {
				slog.Warn("WBI sign failed, using unsigned URL", "error", err)
			} else {
				requestURL = signedURL
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
		if err != nil {
			return nil, 0, "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", cookieHeader)
		req.Header.Set("User-Agent", biliutil.BiliUserAgent)
		req.Header.Set("Referer", "https://member.bilibili.com/")
		req.Header.Set("Origin", "https://member.bilibili.com")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		// 不设置 Accept-Encoding，让 Go 的 http.Client 自动处理 gzip
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Site", "same-site")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, 0, "", err
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, "", fmt.Errorf("read bilibili response: %w", err)
		}
		var br biliResp
		if err := json.Unmarshal(raw, &br); err != nil {
			slog.Error("failed to parse bilibili response", "url", requestURL, "status", resp.StatusCode, "body", string(raw))
			return nil, 0, "", fmt.Errorf("parse bilibili response: %w", err)
		}
		return br.Data, br.Code, br.Message, nil
	}

	// 首次请求
	data, code, message, err := doReq()
	if err != nil {
		return nil, err
	}

	// -352 重试：强制刷新 WBI 密钥后重试一次
	if code == -352 && c.urlSigner != nil {
		slog.Warn("bilibili risk control (-352), refreshing WBI keys and retrying", "url", url)
		if refresher, ok := c.urlSigner.(interface{ RefreshKeys() error }); ok {
			_ = refresher.RefreshKeys()
			data, code, message, err = doReq()
			if err != nil {
				return nil, err
			}
		}
	}

	// 检查返回码
	if code != 0 {
		return nil, mapBiliError(code, message)
	}
	return data, nil
}

func mapBiliError(code int, message string) error {
	// B站部分接口（如 draft/add）错误时 message 字段为空或仅含 code 数字，
	// 回退到 code 作为 message，避免错误信息无意义。
	if message == "" || message == fmt.Sprintf("%d", code) {
		message = fmt.Sprintf("bilibili code %d", code)
	}
	apiErr := fmt.Errorf("%w: code=%d, message=%s", ErrBilibiliAPI, code, message)
	switch {
	case code == -101:
		return fmt.Errorf("%w: %w", ErrCookieExpired, apiErr)
	case code == -403:
		return fmt.Errorf("%w: %w", ErrContentRejected, apiErr)
	case code == -509:
		return fmt.Errorf("%w: %w", ErrRateLimited, apiErr)
	case code == 4101144:
		return fmt.Errorf("%w: %w", ErrNotOwner, apiErr)
	default:
		return apiErr
	}
}

// getBuvids 从 B 站指纹接口获取 buvid3 和 buvid4，并缓存 24 小时
func (c *BiliOpusClient) getBuvids(ctx context.Context, cookieHeader string) (buvid3, buvid4 string, err error) {
	// 检查缓存（24小时有效期）
	now := time.Now()
	c.buvidCacheMu.Lock()
	if cached, ok := c.buvidCache[cookieHeader]; ok && now.Before(cached.expiresAt) {
		c.buvidCacheMu.Unlock()
		return cached.buvid3, cached.buvid4, nil
	}
	c.buvidCacheMu.Unlock()

	// 请求 B 站指纹接口
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.bilibili.com/x/frontend/finger/spi", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", biliutil.BiliUserAgent)
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("get buvids http status %d", resp.StatusCode)
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			B3 string `json:"b_3"`
			B4 string `json:"b_4"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	if result.Code != 0 {
		return "", "", fmt.Errorf("get buvids code=%d message=%s", result.Code, result.Message)
	}
	if result.Data.B3 == "" {
		return "", "", fmt.Errorf("get buvids returned empty b_3")
	}

	// 缓存（24小时）
	c.buvidCacheMu.Lock()
	c.buvidCache[cookieHeader] = cachedBuvid{
		buvid3:    result.Data.B3,
		buvid4:    result.Data.B4,
		expiresAt: now.Add(24 * time.Hour),
	}
	c.buvidCacheMu.Unlock()

	slog.Info("buvids fetched and cached", "buvid3", result.Data.B3, "buvid4", result.Data.B4)
	return result.Data.B3, result.Data.B4, nil
}

// injectBuvids 将 buvid3 和 buvid4 追加到 Cookie 头部
func injectBuvids(cookieHeader, buvid3, buvid4 string) string {
	var parts []string
	if cookieHeader != "" {
		parts = append(parts, cookieHeader)
	}
	if buvid3 != "" {
		parts = append(parts, "buvid3="+buvid3)
	}
	if buvid4 != "" {
		parts = append(parts, "buvid4="+buvid4)
	}
	return strings.Join(parts, "; ")
}

// performGaiaVerification 执行 gaia 风控验证流程
func (c *BiliOpusClient) performGaiaVerification(ctx context.Context, cookie *BiliCookie, voucher string) (string, error) {
	// 步骤 1: 注册验证会话
	dmTrack := `{"dm_img_list":"[]","dm_img_str":"V2ViR0wgMS4wIChPcGVuR0wgRVMgMi4wIENocm9taXVtKQ","dm_cover_img_str":"QU5HTEUgKEludGVsLCBNZXNhIEludGVsKFIpIFVIRCBHcmFwaGljcw","dm_img_inter":"{\"ds\":[],\"wh\":[5000,5800,100],\"of\":[200,300,100]}"}`

	registerURL := "https://api.bilibili.com/x/gaia-vgate/v2/register"
	registerBody := fmt.Sprintf("v_voucher=%s&dm_track=%s&csrf=%s", voucher, dmTrack, cookie.BiliJct)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, strings.NewReader(registerBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookie.CookieHeader())
	req.Header.Set("User-Agent", biliutil.BiliUserAgent)
	req.Header.Set("Referer", "https://member.bilibili.com/")
	req.Header.Set("Origin", "https://member.bilibili.com")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gaia register request failed: %w", err)
	}
	defer resp.Body.Close()

	var registerResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registerResp); err != nil {
		return "", fmt.Errorf("parse gaia register response: %w", err)
	}
	if registerResp.Code != 0 {
		return "", fmt.Errorf("gaia register failed: code=%d, message=%s", registerResp.Code, registerResp.Message)
	}

	// 步骤 2: 验证（模拟点击验证）
	validateURL := "https://api.bilibili.com/x/gaia-vgate/v2/validate"
	validateBody := fmt.Sprintf("csrf=%s", cookie.BiliJct)

	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, validateURL, strings.NewReader(validateBody))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Cookie", cookie.CookieHeader())
	req2.Header.Set("User-Agent", biliutil.BiliUserAgent)
	req2.Header.Set("Referer", "https://member.bilibili.com/")
	req2.Header.Set("Origin", "https://member.bilibili.com")

	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return "", fmt.Errorf("gaia validate request failed: %w", err)
	}
	defer resp2.Body.Close()

	var validateResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&validateResp); err != nil {
		return "", fmt.Errorf("parse gaia validate response: %w", err)
	}
	if validateResp.Code != 0 {
		return "", fmt.Errorf("gaia validate failed: code=%d, message=%s", validateResp.Code, validateResp.Message)
	}

	// 返回 voucher 作为 token（简化处理）
	// 实际上应该从某个响应中提取真正的 token，但根据观察，voucher 可以复用
	return voucher, nil
}
