package biliutil

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultQRLoginBaseURL     = "https://passport.bilibili.com"
	defaultQRLoginTTL         = 180 * time.Second
	qrLoginRetentionAfterDone = 5 * time.Minute
)

var (
	ErrQRLoginSessionNotFound = errors.New("qr login session not found")
	ErrQRLoginSessionExpired  = errors.New("qr login session expired")
	ErrQRLoginNotSucceeded    = errors.New("qr login not succeeded")
	ErrBiliLoginUpstream      = errors.New("bilibili login upstream error")
)

type QRLoginStatus string

const (
	QRLoginPending   QRLoginStatus = "pending"
	QRLoginScanned   QRLoginStatus = "scanned"
	QRLoginExpired   QRLoginStatus = "expired"
	QRLoginSucceeded QRLoginStatus = "succeeded"
	QRLoginFailed    QRLoginStatus = "failed"
)

type QRLoginClient struct {
	HTTPClient  *http.Client
	BaseURL     string
	Now         func() time.Time
	UserAgent   string
	PollReferer string
}

type QRLoginSessionStore struct {
	client *QRLoginClient
	ttl    time.Duration
	now    func() time.Time

	mu       sync.RWMutex
	sessions map[string]*QRLoginSession
}

type QRCodeManager = QRLoginSessionStore

type QRLoginSession struct {
	SessionID     string
	QRCodeKey     string
	QRCodeURL     string
	Status        QRLoginStatus
	Message       string
	UID           int64
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Cookies       []*http.Cookie
	RefreshToken  string
	RawSuccessURL string
}

type QRCodeGenerateResult struct {
	SessionID string    `json:"session_id"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type QRCodePollResult struct {
	SessionID string        `json:"session_id"`
	Status    QRLoginStatus `json:"status"`
	Message   string        `json:"message"`
	UID       int64         `json:"uid,omitempty"`
	ExpiresAt time.Time     `json:"expires_at"`
}

type qrGenerateResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		URL       string `json:"url"`
		QRCodeKey string `json:"qrcode_key"`
	} `json:"data"`
}

type qrPollResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		URL          string `json:"url"`
		RefreshToken string `json:"refresh_token"`
		Code         int    `json:"code"`
		Message      string `json:"message"`
	} `json:"data"`
}

func NewQRLoginClient(httpClient *http.Client) *QRLoginClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &QRLoginClient{
		HTTPClient:  httpClient,
		BaseURL:     defaultQRLoginBaseURL,
		Now:         time.Now,
		UserAgent:   BiliUserAgent,
		PollReferer: "https://www.bilibili.com/",
	}
}

func NewQRLoginSessionStore(client *QRLoginClient, ttl time.Duration) *QRLoginSessionStore {
	if client == nil {
		client = NewQRLoginClient(nil)
	}
	if ttl <= 0 {
		ttl = defaultQRLoginTTL
	}
	now := client.Now
	if now == nil {
		now = time.Now
	}
	return &QRLoginSessionStore{
		client:   client,
		ttl:      ttl,
		now:      now,
		sessions: map[string]*QRLoginSession{},
	}
}

func NewQRCodeManager(client *QRLoginClient, ttl time.Duration) *QRCodeManager {
	return NewQRLoginSessionStore(client, ttl)
}

func (s *QRLoginSessionStore) Create(ctx context.Context) (QRCodeGenerateResult, error) {
	now := s.now()
	s.CleanupExpired(now)

	payload, err := s.client.generateQRCode(ctx)
	if err != nil {
		return QRCodeGenerateResult{}, err
	}
	sessionID, err := randomToken()
	if err != nil {
		return QRCodeGenerateResult{}, fmt.Errorf("generate session id: %w", err)
	}
	session := &QRLoginSession{
		SessionID: sessionID,
		QRCodeKey: payload.Data.QRCodeKey,
		QRCodeURL: payload.Data.URL,
		Status:    QRLoginPending,
		Message:   "未扫码",
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}

	s.mu.Lock()
	s.sessions[sessionID] = session
	s.mu.Unlock()

	return QRCodeGenerateResult{
		SessionID: sessionID,
		URL:       session.QRCodeURL,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

func (s *QRLoginSessionStore) Poll(ctx context.Context, sessionID string) (QRCodePollResult, error) {
	now := s.now()

	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	if ok {
		session = cloneQRLoginSession(session)
	}
	s.mu.RUnlock()
	if !ok {
		return QRCodePollResult{}, ErrQRLoginSessionNotFound
	}
	if session.Status == QRLoginSucceeded && now.Sub(session.UpdatedAt) <= qrLoginRetentionAfterDone {
		return session.pollResult(), nil
	}
	if now.After(session.ExpiresAt) {
		s.markExpired(sessionID, now)
		return QRCodePollResult{}, ErrQRLoginSessionExpired
	}
	if isTerminalQRLoginStatus(session.Status) {
		return session.pollResult(), nil
	}

	payload, cookies, err := s.client.pollQRCode(ctx, session.QRCodeKey)
	if err != nil {
		return QRCodePollResult{}, err
	}

	status := mapQRLoginStatus(payload.Data.Code)
	message := payload.Data.Message
	if message == "" {
		message = defaultQRLoginMessage(status)
	}

	s.mu.Lock()
	current, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return QRCodePollResult{}, ErrQRLoginSessionNotFound
	}
	current.Status = status
	current.Message = message
	current.UpdatedAt = now
	if status == QRLoginSucceeded {
		current.Cookies = cloneCookies(cookies)
		current.RefreshToken = payload.Data.RefreshToken
		current.RawSuccessURL = payload.Data.URL
		current.UID = parseUIDFromCookies(cookies)
	} else if status == QRLoginExpired {
		current.ExpiresAt = now
	}
	result := current.pollResult()
	s.mu.Unlock()

	return result, nil
}

func (s *QRLoginSessionStore) GetSucceeded(sessionID string) (*QRLoginSession, error) {
	now := s.now()

	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	if ok {
		session = cloneQRLoginSession(session)
	}
	s.mu.RUnlock()
	if !ok {
		return nil, ErrQRLoginSessionNotFound
	}
	if session.Status == QRLoginSucceeded && now.Sub(session.UpdatedAt) <= qrLoginRetentionAfterDone {
		return session, nil
	}
	if now.After(session.ExpiresAt) {
		return nil, ErrQRLoginSessionExpired
	}
	if session.Status != QRLoginSucceeded {
		return nil, ErrQRLoginNotSucceeded
	}
	return session, nil
}

func (s *QRLoginSessionStore) Delete(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

func (s *QRLoginSessionStore) CleanupExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, session := range s.sessions {
		if isTerminalQRLoginStatus(session.Status) {
			if now.Sub(session.UpdatedAt) > qrLoginRetentionAfterDone {
				delete(s.sessions, id)
			}
			continue
		}
		if now.After(session.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
}

func (c *QRLoginClient) generateQRCode(ctx context.Context) (qrGenerateResponse, error) {
	var payload qrGenerateResponse
	endpoint := strings.TrimRight(c.baseURL(), "/") + "/x/passport-login/web/qrcode/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return payload, err
	}
	c.setHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return payload, fmt.Errorf("%w: generate qrcode: %v", ErrBiliLoginUpstream, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return payload, fmt.Errorf("%w: generate qrcode status %d", ErrBiliLoginUpstream, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return payload, fmt.Errorf("%w: decode generate qrcode response: %v", ErrBiliLoginUpstream, err)
	}
	if payload.Code != 0 || payload.Data.URL == "" || payload.Data.QRCodeKey == "" {
		return payload, fmt.Errorf("%w: generate qrcode rejected: %s", ErrBiliLoginUpstream, payload.Message)
	}
	return payload, nil
}

func (c *QRLoginClient) pollQRCode(ctx context.Context, qrcodeKey string) (qrPollResponse, []*http.Cookie, error) {
	var payload qrPollResponse
	endpoint := strings.TrimRight(c.baseURL(), "/") + "/x/passport-login/web/qrcode/poll?qrcode_key=" + url.QueryEscape(qrcodeKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return payload, nil, err
	}
	c.setHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return payload, nil, fmt.Errorf("%w: poll qrcode: %v", ErrBiliLoginUpstream, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return payload, nil, fmt.Errorf("%w: poll qrcode status %d", ErrBiliLoginUpstream, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return payload, nil, fmt.Errorf("%w: decode poll qrcode response: %v", ErrBiliLoginUpstream, err)
	}
	if payload.Code != 0 {
		return payload, nil, fmt.Errorf("%w: poll qrcode rejected: %s", ErrBiliLoginUpstream, payload.Message)
	}
	return payload, resp.Cookies(), nil
}

func (c *QRLoginClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *QRLoginClient) baseURL() string {
	if c.BaseURL == "" {
		return defaultQRLoginBaseURL
	}
	return c.BaseURL
}

func (c *QRLoginClient) setHeaders(req *http.Request) {
	userAgent := c.UserAgent
	if userAgent == "" {
		userAgent = BiliUserAgent
	}
	referer := c.PollReferer
	if referer == "" {
		referer = "https://www.bilibili.com/"
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	req.Header.Set("Accept", "application/json, text/plain, */*")
}

func (s *QRLoginSessionStore) markExpired(sessionID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[sessionID]; ok {
		session.Status = QRLoginExpired
		session.Message = defaultQRLoginMessage(QRLoginExpired)
		session.ExpiresAt = now
		session.UpdatedAt = now
	}
}

func (s *QRLoginSession) pollResult() QRCodePollResult {
	return QRCodePollResult{
		SessionID: s.SessionID,
		Status:    s.Status,
		Message:   s.Message,
		UID:       s.UID,
		ExpiresAt: s.ExpiresAt,
	}
}

func mapQRLoginStatus(code int) QRLoginStatus {
	switch code {
	case 86101:
		return QRLoginPending
	case 86090:
		return QRLoginScanned
	case 86038:
		return QRLoginExpired
	case 0:
		return QRLoginSucceeded
	default:
		return QRLoginFailed
	}
}

func defaultQRLoginMessage(status QRLoginStatus) string {
	switch status {
	case QRLoginPending:
		return "未扫码"
	case QRLoginScanned:
		return "已扫码，请在手机端确认"
	case QRLoginExpired:
		return "二维码已过期"
	case QRLoginSucceeded:
		return "登录成功"
	default:
		return "登录失败"
	}
}

func isTerminalQRLoginStatus(status QRLoginStatus) bool {
	return status == QRLoginSucceeded || status == QRLoginExpired || status == QRLoginFailed
}

func parseUIDFromCookies(cookies []*http.Cookie) int64 {
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == "DedeUserID" {
			uid, _ := strconv.ParseInt(cookie.Value, 10, 64)
			return uid
		}
	}
	return 0
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func cloneQRLoginSession(session *QRLoginSession) *QRLoginSession {
	if session == nil {
		return nil
	}
	clone := *session
	clone.Cookies = cloneCookies(session.Cookies)
	return &clone
}

func cloneCookies(cookies []*http.Cookie) []*http.Cookie {
	if len(cookies) == 0 {
		return nil
	}
	cloned := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		item := *cookie
		cloned = append(cloned, &item)
	}
	return cloned
}
