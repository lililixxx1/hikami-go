package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hikami-go/internal/session"
)

const defaultDashScopeUploadsURL = "https://dashscope.aliyuncs.com/api/v1/uploads"

// DashScopeTempPublisher 把本地 ASR 音频上传到百炼临时存储。返回的 oss:// URL
// 与模型和账号绑定，并在 48 小时后过期。该后端适合开发和低频试跑。
type DashScopeTempPublisher struct {
	httpClient *http.Client
	uploadsURL string
	apiKeyEnv  string
	model      string
}

type dashScopeUploadPolicy struct {
	Policy              string          `json:"policy"`
	Signature           string          `json:"signature"`
	UploadDir           string          `json:"upload_dir"`
	UploadHost          string          `json:"upload_host"`
	MaxFileSizeMB       json.RawMessage `json:"max_file_size_mb"`
	OSSAccessKeyID      string          `json:"oss_access_key_id"`
	XOSSObjectACL       string          `json:"x_oss_object_acl"`
	XOSSForbidOverwrite string          `json:"x_oss_forbid_overwrite"`
}

type dashScopeUploadPolicyResponse struct {
	RequestID string                `json:"request_id"`
	Data      dashScopeUploadPolicy `json:"data"`
	Code      string                `json:"code"`
	Message   string                `json:"message"`
}

func newDashScopeTempPublisher(client *http.Client, uploadsURL, apiKeyEnv, model string) *DashScopeTempPublisher {
	return &DashScopeTempPublisher{
		httpClient: client,
		uploadsURL: uploadsURL,
		apiKeyEnv:  apiKeyEnv,
		model:      NormalizeDashScopeASRModel(model),
	}
}

func (p *DashScopeTempPublisher) Publish(ctx context.Context, audioPath string, sess session.Session) (string, string, error) {
	info, err := os.Stat(audioPath)
	if err != nil {
		return "", "", fmt.Errorf("dashscope temporary upload: stat audio: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("dashscope temporary upload: audio is not a regular file")
	}

	policy, err := p.getPolicy(ctx)
	if err != nil {
		return "", "", err
	}
	maxBytes, err := uploadLimitBytes(policy.MaxFileSizeMB)
	if err != nil {
		return "", "", fmt.Errorf("dashscope temporary upload: invalid max_file_size_mb: %w", err)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return "", "", fmt.Errorf("dashscope temporary upload: audio is %.1f MB, model %s permits %.1f MB",
			float64(info.Size())/(1024*1024), p.model, float64(maxBytes)/(1024*1024))
	}

	filename := filepath.Base(audioPath)
	// L13(2026-08-15):objectKey 加 session ID 前缀——所有场次的临时音频都叫
	// audio.asr.mp3,共用同一 objectKey 会在 DashScope OSS 端互相覆盖/冲突;
	// 同一场重跑复用同一 key：行为取决于 policy 的 x_oss_forbid_overwrite(为 true 时 OSS 拒 FileAlreadyExists 而非覆盖，上报为上传失败,任务重试换新 key 不可行——同场 ID 不变,需实测确认真实 policy 该值)。零值 session(旧测试路径)保持原文件名。
	if sess.ID != "" {
		filename = sess.ID + "_" + filename
	}
	objectKey := strings.TrimRight(policy.UploadDir, "/") + "/" + filename
	if err := p.upload(ctx, policy, objectKey, audioPath, info.Size()); err != nil {
		return "", "", err
	}
	ossURL := "oss://" + strings.TrimLeft(objectKey, "/")
	return ossURL, ossURL, nil
}

func (p *DashScopeTempPublisher) getPolicy(ctx context.Context) (dashScopeUploadPolicy, error) {
	endpoint, err := url.Parse(p.uploadsURL)
	if err != nil {
		return dashScopeUploadPolicy{}, fmt.Errorf("dashscope temporary upload: invalid uploads URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("action", "getPolicy")
	query.Set("model", p.model)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return dashScopeUploadPolicy{}, fmt.Errorf("dashscope temporary upload: create policy request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv(p.apiKeyEnv))
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return dashScopeUploadPolicy{}, fmt.Errorf("dashscope temporary upload: get policy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return dashScopeUploadPolicy{}, fmt.Errorf("dashscope temporary upload: get policy HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result dashScopeUploadPolicyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return dashScopeUploadPolicy{}, fmt.Errorf("dashscope temporary upload: decode policy: %w", err)
	}
	policy := result.Data
	if policy.Policy == "" || policy.Signature == "" || policy.UploadDir == "" || policy.UploadHost == "" || policy.OSSAccessKeyID == "" {
		return dashScopeUploadPolicy{}, fmt.Errorf("dashscope temporary upload: incomplete policy response (request_id=%s, code=%s, message=%s)", result.RequestID, result.Code, result.Message)
	}
	uploadURL, err := url.Parse(policy.UploadHost)
	if err != nil || uploadURL.Scheme != "https" || uploadURL.Host == "" {
		return dashScopeUploadPolicy{}, fmt.Errorf("dashscope temporary upload: policy returned invalid HTTPS upload host")
	}
	return policy, nil
}

func (p *DashScopeTempPublisher) upload(ctx context.Context, policy dashScopeUploadPolicy, objectKey, audioPath string, fileSize int64) error {
	file, err := os.Open(audioPath)
	if err != nil {
		return fmt.Errorf("dashscope temporary upload: open audio: %w", err)
	}
	defer file.Close()

	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	fields := []struct{ name, value string }{
		{"OSSAccessKeyId", policy.OSSAccessKeyID},
		{"policy", policy.Policy},
		{"Signature", policy.Signature},
		{"key", objectKey},
		{"x-oss-object-acl", policy.XOSSObjectACL},
		{"x-oss-forbid-overwrite", policy.XOSSForbidOverwrite},
		{"success_action_status", "200"},
	}
	for _, field := range fields {
		if err := writer.WriteField(field.name, field.value); err != nil {
			return fmt.Errorf("dashscope temporary upload: build form: %w", err)
		}
	}
	// OSS 要求 file 是最后一个 multipart 字段。分别保留文件前后的表单字节，
	// 让文件内容直接流式传输，避免把大音频完整缓存在内存中。
	if _, err := writer.CreateFormFile("file", filepath.Base(audioPath)); err != nil {
		return fmt.Errorf("dashscope temporary upload: build file form: %w", err)
	}
	prefixLen := envelope.Len()
	if err := writer.Close(); err != nil {
		return fmt.Errorf("dashscope temporary upload: close form: %w", err)
	}
	encoded := envelope.Bytes()
	prefix := encoded[:prefixLen]
	suffix := encoded[prefixLen:]
	body := io.MultiReader(bytes.NewReader(prefix), file, bytes.NewReader(suffix))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, policy.UploadHost, body)
	if err != nil {
		return fmt.Errorf("dashscope temporary upload: create upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = int64(len(prefix)) + fileSize + int64(len(suffix))
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dashscope temporary upload: upload audio: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("dashscope temporary upload: OSS HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func uploadLimitBytes(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		mb, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || mb < 0 {
			return 0, fmt.Errorf("%s", raw)
		}
		return int64(mb * 1024 * 1024), nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	mb, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || mb < 0 {
		return 0, fmt.Errorf("%q", text)
	}
	return int64(mb * 1024 * 1024), nil
}
