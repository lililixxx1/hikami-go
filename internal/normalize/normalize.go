package normalize

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

const TaskType = "normalize"

type AudioConverter interface {
	Convert(ctx context.Context, inputPath string, outputPath string) error
}

type FFmpegConverter struct {
	Command string
}

func (c FFmpegConverter) Convert(ctx context.Context, inputPath string, outputPath string) error {
	command := c.Command
	if command == "" {
		command = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, command,
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-i", inputPath,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		"-b:a", "64k",
		"-f", "mp3",
		outputPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg normalize failed: %w: %s", err, string(output))
	}
	return nil
}

type Handler struct {
	cfg       *config.Config
	sessions  *session.Store
	states    *state.Store
	converter AudioConverter
	onSuccess func(ctx context.Context, task worker.Task)
}

func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store, converter AudioConverter) *Handler {
	return &Handler{
		cfg:       cfg,
		sessions:  sessions,
		states:    states,
		converter: converter,
	}
}

func (h *Handler) SetOnSuccess(fn func(ctx context.Context, task worker.Task)) {
	h.onSuccess = fn
}

func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask)
}

func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(h.cfg.OutputRoot, task.ChannelID, sessionInfo.Slug)
	rawDir := filepath.Join(sessionDir, "raw")
	asrDir := filepath.Join(sessionDir, "asr")
	packageDir := filepath.Join(sessionDir, "package")

	if err := os.MkdirAll(asrDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		return err
	}

	if err := reporter.Progress(ctx, 10, "locating raw audio"); err != nil {
		return err
	}
	rawAudioPath, err := findRawAudio(rawDir)
	if err != nil {
		return err
	}

	if err := reporter.Progress(ctx, 30, "generating asr audio"); err != nil {
		return err
	}
	asrAudioPath := filepath.Join(asrDir, "audio.asr.mp3")
	if err := h.convertAtomic(ctx, rawAudioPath, asrAudioPath); err != nil {
		return err
	}

	if err := reporter.Progress(ctx, 60, "normalizing danmaku"); err != nil {
		return err
	}
	danmaku, err := normalizeDanmaku(rawDir, sessionInfo.SourceType)
	if err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(packageDir, "danmaku.json"), danmaku); err != nil {
		return err
	}

	if err := reporter.Progress(ctx, 80, "writing metadata"); err != nil {
		return err
	}
	metadata := buildMetadata(sessionInfo, rawAudioPath, asrAudioPath, len(danmaku))
	if err := writeJSONAtomic(filepath.Join(packageDir, "metadata.json"), metadata); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(sessionDir, "metadata.json"), metadata); err != nil {
		return err
	}

	if _, err := h.states.Apply(ctx, task.SessionID, state.EventNormalizeSucceeded, task.ID, ""); err != nil {
		return err
	}
	if err := reporter.Progress(ctx, 95, "normalize completed"); err != nil {
		return err
	}
	if h.onSuccess != nil {
		h.onSuccess(ctx, task)
	}
	return nil
}

func (h *Handler) convertAtomic(ctx context.Context, inputPath string, outputPath string) error {
	tempPath := outputPath + ".tmp"
	_ = os.Remove(tempPath)
	if err := h.converter.Convert(ctx, inputPath, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return os.Rename(tempPath, outputPath)
}

func findRawAudio(rawDir string) (string, error) {
	preferred := filepath.Join(rawDir, "audio.m4a")
	if _, err := os.Stat(preferred); err == nil {
		return preferred, nil
	}

	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "audio.") {
			return filepath.Join(rawDir, name), nil
		}
	}
	return "", fmt.Errorf("raw audio not found in %s", rawDir)
}

// DanmakuItem represents a single danmaku event after normalization.
type DanmakuItem struct {
	TimeMS          int64  `json:"time_ms"`
	OriginalTimeMS  int64  `json:"original_time_ms,omitempty"`
	CorrectedTimeMS int64  `json:"corrected_time_ms,omitempty"`
	Type            string `json:"type"`
	UserID          string `json:"user_id,omitempty"`
	UserName        string `json:"user_name,omitempty"`
	Text            string `json:"text"`
	Color           string `json:"color,omitempty"`
	RawTime         string `json:"raw_time,omitempty"`
	ReceivedAt      string `json:"received_at,omitempty"`
	Source          string `json:"source"`
	Weight          int    `json:"weight,omitempty"`
}

// normalizeDanmaku resolves danmaku with priority: danmaku.jsonl > danmaku.xml > danmaku_parts/.
// For multi-P (danmaku_parts/), it reads each XML file and offsets time_ms based on part durations.
func normalizeDanmaku(rawDir string, source string) ([]DanmakuItem, error) {
	// Priority 1: danmaku.jsonl (live recording or manual import)
	jsonlPath := filepath.Join(rawDir, "danmaku.jsonl")
	if _, err := os.Stat(jsonlPath); err == nil {
		items, parseErr := parseJSONLDanmaku(jsonlPath, source)
		if parseErr != nil {
			return nil, parseErr
		}
		if len(items) > 0 {
			return items, nil
		}
	}

	// Priority 2: danmaku.xml (single-P replay)
	xmlPath := filepath.Join(rawDir, "danmaku.xml")
	if _, err := os.Stat(xmlPath); err == nil {
		items, parseErr := parseXMLDanmaku(xmlPath, source, 0)
		if parseErr != nil {
			return nil, parseErr
		}
		if len(items) > 0 {
			return items, nil
		}
	}

	// Priority 3: danmaku_parts/ (multi-P replay)
	partsDir := filepath.Join(rawDir, "danmaku_parts")
	if _, err := os.Stat(partsDir); err == nil {
		items, mergeErr := mergeMultiPDanmaku(rawDir, partsDir, source)
		if mergeErr != nil {
			return nil, mergeErr
		}
		if len(items) > 0 {
			return items, nil
		}
	}

	return []DanmakuItem{}, nil
}

// parseJSONLDanmaku reads danmaku from a JSONL file.
func parseJSONLDanmaku(path, source string) ([]DanmakuItem, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var items []DanmakuItem
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item DanmakuItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		if item.Type == "" {
			item.Type = "danmaku"
		}
		if item.Source == "" {
			item.Source = source
		}
		if item.OriginalTimeMS == 0 {
			item.OriginalTimeMS = item.TimeMS
		}
		if item.CorrectedTimeMS > 0 {
			item.TimeMS = item.CorrectedTimeMS
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if items == nil {
		return []DanmakuItem{}, nil
	}
	return items, nil
}

// bilibiliXML represents the root <i> element of Bilibili XML danmaku.
type bilibiliXML struct {
	D []bilibiliDanmaku `xml:"d"`
}

// bilibiliDanmaku represents a single <d> element.
// The p attribute contains comma-separated fields:
// appear_time_seconds, mode, font_size, color, send_timestamp, pool, user_hash, danmaku_id
type bilibiliDanmaku struct {
	P    string `xml:"p,attr"`
	Text string `xml:",innerxml"`
}

// parseXMLDanmaku parses a Bilibili XML danmaku file and returns normalized items.
// timeOffsetMS is added to each danmaku's time_ms (used for multi-P merge).
func parseXMLDanmaku(path, source string, timeOffsetMS int64) ([]DanmakuItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc bilibiliXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse xml danmaku: %w", err)
	}

	var items []DanmakuItem
	for _, d := range doc.D {
		fields := strings.Split(d.P, ",")
		if len(fields) < 4 {
			continue
		}

		timeSec, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		colorHex := fields[3]

		item := DanmakuItem{
			TimeMS:         int64(timeSec*1000) + timeOffsetMS,
			OriginalTimeMS: int64(timeSec * 1000),
			Type:           "danmaku",
			Text:           html.UnescapeString(d.Text),
			Color:          fmt.Sprintf("#%s", colorHex),
			Source:         source,
		}

		// user_hash is field[6], danmaku_id is field[7]
		if len(fields) >= 8 {
			item.UserID = fields[7] // use danmaku_id as user_id
		}
		if len(fields) >= 7 {
			item.RawTime = fields[4] // send timestamp
		}

		items = append(items, item)
	}
	if items == nil {
		return []DanmakuItem{}, nil
	}
	return items, nil
}

// partDurationRecord mirrors the JSON structure written by download's multi-P logic.
type partDurationRecord struct {
	Index   int     `json:"index"`
	DurSecs float64 `json:"dur_secs"`
}

// mergeMultiPDanmaku reads all XML files from danmaku_parts/, sorts by index,
// and merges them with cumulative time offsets based on part durations.
func mergeMultiPDanmaku(rawDir, partsDir, source string) ([]DanmakuItem, error) {
	// Read part durations from raw/part_durations.json.
	durations, err := loadPartDurations(rawDir)
	if err != nil {
		return nil, fmt.Errorf("load part durations: %w", err)
	}
	if len(durations) == 0 {
		return []DanmakuItem{}, nil
	}

	// List XML files in danmaku_parts/.
	entries, err := os.ReadDir(partsDir)
	if err != nil {
		return nil, err
	}

	// Build a map of index -> file path.
	type xmlFile struct {
		index int
		path  string
	}
	var xmlFiles []xmlFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".xml") {
			continue
		}
		// Extract index from filename pattern pNNN.xml.
		base := strings.TrimSuffix(name, ".xml")
		idx, err := strconv.Atoi(base[1:]) // strip "p" prefix
		if err != nil {
			continue
		}
		xmlFiles = append(xmlFiles, xmlFile{index: idx, path: filepath.Join(partsDir, name)})
	}
	if len(xmlFiles) == 0 {
		return []DanmakuItem{}, nil
	}

	// Sort by index.
	sort.Slice(xmlFiles, func(i, j int) bool {
		return xmlFiles[i].index < xmlFiles[j].index
	})

	// Build cumulative duration offset map: part index -> time offset in ms.
	durMap := make(map[int]float64)
	for _, d := range durations {
		durMap[d.Index] = d.DurSecs
	}

	var allItems []DanmakuItem
	cumulativeMS := durationBeforePart(durations, xmlFiles[0].index)

	for i, xf := range xmlFiles {
		items, err := parseXMLDanmaku(xf.path, source, cumulativeMS)
		if err != nil {
			return nil, fmt.Errorf("parse danmaku part %d: %w", xf.index, err)
		}
		allItems = append(allItems, items...)

		// 按当前 P 的音频时长累计偏移；缺失弹幕 XML 的 P 也必须计入。
		nextIndex := 0
		if i+1 < len(xmlFiles) {
			nextIndex = xmlFiles[i+1].index
		}
		cumulativeMS += durationBetweenParts(durations, xf.index, nextIndex)
	}

	if allItems == nil {
		return []DanmakuItem{}, nil
	}
	return allItems, nil
}

func durationBetweenParts(durations []partDurationRecord, currentIndex, nextIndex int) int64 {
	var total int64
	for _, d := range durations {
		if d.Index < currentIndex {
			continue
		}
		if nextIndex > 0 && d.Index >= nextIndex {
			break
		}
		total += int64(d.DurSecs * 1000)
	}
	return total
}

func durationBeforePart(durations []partDurationRecord, partIndex int) int64 {
	var total int64
	for _, d := range durations {
		if d.Index >= partIndex {
			break
		}
		total += int64(d.DurSecs * 1000)
	}
	return total
}

// loadPartDurations reads raw/part_durations.json and returns sorted duration records.
func loadPartDurations(rawDir string) ([]partDurationRecord, error) {
	data, err := os.ReadFile(filepath.Join(rawDir, "part_durations.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var durations []partDurationRecord
	if err := json.Unmarshal(data, &durations); err != nil {
		return nil, err
	}
	sort.Slice(durations, func(i, j int) bool {
		return durations[i].Index < durations[j].Index
	})
	return durations, nil
}

// Metadata holds normalized session metadata.
type Metadata struct {
	SessionID    string `json:"session_id"`
	ChannelID    string `json:"channel_id"`
	Slug         string `json:"slug"`
	SourceType   string `json:"source_type"`
	SourceID     string `json:"source_id"`
	Title        string `json:"title"`
	StartedAt    string `json:"started_at,omitempty"`
	EndedAt      string `json:"ended_at,omitempty"`
	SourceURL    string `json:"source_url"`
	RawAudioPath string `json:"raw_audio_path"`
	ASRAudioPath string `json:"asr_audio_path"`
	DanmakuCount int    `json:"danmaku_count"`
	GeneratedAt  string `json:"generated_at"`
}

func buildMetadata(sessionInfo session.Session, rawAudioPath string, asrAudioPath string, danmakuCount int) Metadata {
	return Metadata{
		SessionID:    sessionInfo.ID,
		ChannelID:    sessionInfo.ChannelID,
		Slug:         sessionInfo.Slug,
		SourceType:   sessionInfo.SourceType,
		SourceID:     sessionInfo.SourceID,
		Title:        sessionInfo.Title,
		StartedAt:    sessionInfo.StartedAt,
		EndedAt:      sessionInfo.EndedAt,
		SourceURL:    sessionInfo.SourceURL,
		RawAudioPath: filepath.ToSlash(filepath.Join("raw", filepath.Base(rawAudioPath))),
		ASRAudioPath: filepath.ToSlash(filepath.Join("asr", filepath.Base(asrAudioPath))),
		DanmakuCount: danmakuCount,
		GeneratedAt:  time.Now().Format(time.RFC3339),
	}
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
