package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/glossary"
	"hikami-go/internal/recap"

	"github.com/gin-gonic/gin"
)

// ConfigExportBundle is the top-level JSON structure for full config export.
type ConfigExportBundle struct {
	Version      string                  `json:"version"`
	ExportedAt   string                  `json:"exported_at"`
	RecapAI      config.RecapAIConfig    `json:"recap_ai"`
	Publish      config.PublishConfig    `json:"publish"`
	WebDAV       config.WebDAVConfig     `json:"webdav"`
	Secrets      map[string]string       `json:"secrets"`
	Channels     []channel.UpsertInput   `json:"channels"`
	Glossary     GlossaryExportSection   `json:"glossary"`
	Templates    TemplateExportSection   `json:"templates"`
	BiliAccounts []BiliAccountExportItem `json:"bili_accounts"`
}

type GlossaryExportSection struct {
	Global  *glossary.GlossaryExport            `json:"global,omitempty"`
	Channel map[string]*glossary.GlossaryExport `json:"channels,omitempty"`
}

type TemplateExportSection struct {
	Global  *recap.TemplateExport            `json:"global,omitempty"`
	Channel map[string]*recap.TemplateExport `json:"channels,omitempty"`
}

type BiliAccountExportItem struct {
	UID               int64  `json:"uid"`
	Nickname          string `json:"nickname"`
	IsDefaultDownload bool   `json:"is_default_download"`
	IsDefaultPublish  bool   `json:"is_default_publish"`
}

type importResult struct {
	Imported bool     `json:"imported"`
	Strategy string   `json:"strategy"`
	Warnings []string `json:"warnings,omitempty"`
	Details  struct {
		SecretsCount      int `json:"secrets_count"`
		ChannelsCount     int `json:"channels_count"`
		GlossaryCount     int `json:"glossary_count"`
		TemplatesCount    int `json:"templates_count"`
		BiliAccountsCount int `json:"bili_accounts_count"`
	} `json:"details"`
}

func (s *Server) handleExportConfig(ctx *gin.Context) {
	bundle := ConfigExportBundle{
		Version:    "1",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Secrets:    make(map[string]string),
	}

	// In-memory config
	s.publishMu.RLock()
	bundle.RecapAI = s.cfg.RecapAI
	bundle.Publish = s.cfg.Publish
	bundle.WebDAV = s.cfg.WebDAV
	s.publishMu.RUnlock()

	// Secrets (actual values)
	secretList, err := s.secrets.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	for _, sec := range secretList {
		if sec.Value != "" {
			bundle.Secrets[sec.Key] = sec.Value
		}
	}

	// Channels
	chList, err := s.channels.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	bundle.Channels = make([]channel.UpsertInput, 0, len(chList))
	for _, ch := range chList {
		bundle.Channels = append(bundle.Channels, channelToUpsertInput(ch))
	}

	// Glossary: global + per-channel
	glossaryGlobal, err := s.glossary.ExportJSON(ctx.Request.Context(), "")
	if err == nil && glossaryGlobal != nil {
		var ge glossary.GlossaryExport
		if json.Unmarshal(glossaryGlobal, &ge) == nil {
			bundle.Glossary.Global = &ge
		}
	}
	if len(chList) > 0 {
		bundle.Glossary.Channel = make(map[string]*glossary.GlossaryExport)
		for _, ch := range chList {
			data, err := s.glossary.ExportJSON(ctx.Request.Context(), ch.ID)
			if err != nil || data == nil {
				continue
			}
			var ge glossary.GlossaryExport
			if json.Unmarshal(data, &ge) == nil && len(ge.Entries) > 0 {
				bundle.Glossary.Channel[ch.ID] = &ge
			}
		}
	}

	// Templates: global + per-channel
	tplGlobal, err := s.recapTemplates.ExportJSON(ctx.Request.Context(), "")
	if err == nil && tplGlobal != nil {
		var te recap.TemplateExport
		if json.Unmarshal(tplGlobal, &te) == nil {
			bundle.Templates.Global = &te
		}
	}
	if len(chList) > 0 {
		bundle.Templates.Channel = make(map[string]*recap.TemplateExport)
		for _, ch := range chList {
			data, err := s.recapTemplates.ExportJSON(ctx.Request.Context(), ch.ID)
			if err != nil || data == nil {
				continue
			}
			var te recap.TemplateExport
			if json.Unmarshal(data, &te) == nil && len(te.Templates) > 0 {
				bundle.Templates.Channel[ch.ID] = &te
			}
		}
	}

	// Bili accounts (metadata only, no cookie files)
	accounts, err := s.cookieAccounts.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	bundle.BiliAccounts = make([]BiliAccountExportItem, 0, len(accounts))
	for _, a := range accounts {
		bundle.BiliAccounts = append(bundle.BiliAccounts, BiliAccountExportItem{
			UID:               a.UID,
			Nickname:          a.Nickname,
			IsDefaultDownload: a.IsDefaultDownload,
			IsDefaultPublish:  a.IsDefaultPublish,
		})
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Header("Content-Type", "application/json")
	ctx.Header("Content-Disposition", `attachment; filename="hikami-config-export.json"`)
	ctx.Data(http.StatusOK, "application/json", data)
}

func (s *Server) handleImportConfig(ctx *gin.Context) {
	strategy := ctx.DefaultQuery("strategy", "merge")
	if strategy != "merge" && strategy != "overwrite" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "strategy must be 'merge' or 'overwrite'"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, 10<<20))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var bundle ConfigExportBundle
	if err := json.Unmarshal(body, &bundle); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}
	if bundle.Version != "1" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "unsupported export version: " + bundle.Version})
		return
	}

	result := importResult{Imported: true, Strategy: strategy}
	cctx := ctx.Request.Context()

	// Overwrite: clear existing data first
	if strategy == "overwrite" {
		_ = s.secrets.Clear(cctx)
		_ = s.glossary.ClearAll(cctx)
		_ = s.recapTemplates.ClearCustom(cctx)
		_ = s.cookieAccounts.ClearAll(cctx)
	}

	// Apply in-memory config
	s.publishMu.Lock()
	if bundle.RecapAI.BaseURL != "" || bundle.RecapAI.Model != "" {
		s.cfg.RecapAI = bundle.RecapAI
	}
	if bundle.Publish.Mode != "" {
		s.cfg.Publish = bundle.Publish
	}
	if bundle.WebDAV.Remote != "" || bundle.WebDAV.URL != "" {
		s.cfg.WebDAV = bundle.WebDAV
	}
	s.publishMu.Unlock()

	// Secrets
	for key, value := range bundle.Secrets {
		if value == "" {
			continue
		}
		if err := s.secrets.Set(cctx, key, value); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("secret %s: %v", key, err))
			continue
		}
		os.Setenv(key, value)
		result.Details.SecretsCount++
	}
	s.bumpConfigGen()

	// Channels (upsert)
	for _, input := range bundle.Channels {
		if input.ID == "" {
			continue
		}
		_, err := s.channels.Get(cctx, input.ID)
		if err == nil {
			_, err = s.channels.Update(cctx, input.ID, input)
		} else {
			_, err = s.channels.Create(cctx, input)
		}
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("channel %s: %v", input.ID, err))
			continue
		}
		result.Details.ChannelsCount++
	}

	// Glossary
	if bundle.Glossary.Global != nil {
		data, _ := json.Marshal(bundle.Glossary.Global)
		if count, err := s.glossary.ImportJSON(cctx, "", data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("glossary global: %v", err))
		} else {
			result.Details.GlossaryCount += count
		}
	}
	for chID, ge := range bundle.Glossary.Channel {
		data, _ := json.Marshal(ge)
		if count, err := s.glossary.ImportJSON(cctx, chID, data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("glossary channel %s: %v", chID, err))
		} else {
			result.Details.GlossaryCount += count
		}
	}

	// Templates
	if bundle.Templates.Global != nil {
		data, _ := json.Marshal(bundle.Templates.Global)
		if count, err := s.recapTemplates.ImportJSON(cctx, "", data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("templates global: %v", err))
		} else {
			result.Details.TemplatesCount += count
		}
	}
	for chID, te := range bundle.Templates.Channel {
		data, _ := json.Marshal(te)
		if count, err := s.recapTemplates.ImportJSON(cctx, chID, data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("templates channel %s: %v", chID, err))
		} else {
			result.Details.TemplatesCount += count
		}
	}

	// Bili accounts
	for _, item := range bundle.BiliAccounts {
		if item.UID == 0 {
			continue
		}
		// merge: skip if UID already exists
		if strategy == "merge" {
			if existing, _ := s.cookieAccounts.GetByUID(cctx, item.UID); existing != nil {
				continue
			}
		}
		_, err := s.cookieAccounts.CreateImported(cctx, &biliutil.CookieAccount{
			UID:               item.UID,
			Nickname:          item.Nickname,
			IsDefaultDownload: item.IsDefaultDownload,
			IsDefaultPublish:  item.IsDefaultPublish,
		})
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("bili account uid %d: %v", item.UID, err))
			continue
		}
		result.Details.BiliAccountsCount++
	}

	if len(bundle.BiliAccounts) > 0 {
		result.Warnings = append(result.Warnings, "B站账号已导入元数据，需重新扫码登录恢复 Cookie")
	}

	cfgSnapshot, gen := s.configSnapshot()
	s.refreshRuntimeStatus(cfgSnapshot, gen)

	ctx.JSON(http.StatusOK, result)
}

func channelToUpsertInput(ch channel.Channel) channel.UpsertInput {
	autoRecap := ch.AutoRecap
	return channel.UpsertInput{
		ID:                  ch.ID,
		Name:                ch.Name,
		UID:                 ch.UID,
		LiveRoomID:          ch.LiveRoomID,
		ReplaySourceURL:     ch.ReplaySourceURL,
		SpaceURL:            ch.SpaceURL,
		TitlePrefix:         ch.TitlePrefix,
		CookieFile:          ch.CookieFile,
		DownloadCookieFile:  ch.DownloadCookieFile,
		DownloadAccountID:   ch.DownloadAccountID,
		Enabled:             ch.Enabled,
		AutoRecord:          ch.AutoRecord,
		AutoASR:             ch.AutoASR,
		AutoRecap:           &autoRecap,
		RecordDanmaku:       ch.RecordDanmaku,
		SourceMode:          ch.SourceMode,
		DiscoverLimit:       ch.DiscoverLimit,
		PublishEnabled:      ch.PublishEnabled,
		PublishMode:         ch.PublishMode,
		PublishCategoryID:   ch.PublishCategoryID,
		PublishListID:       ch.PublishListID,
		PublishPrivatePub:   ch.PublishPrivatePub,
		PublishOriginal:     ch.PublishOriginal,
		AutoPublish:         ch.AutoPublish,
		PublishAigc:         ch.PublishAigc,
		PublishTimerPubTime: ch.PublishTimerPubTime,
		PublishCoverURL:     ch.PublishCoverURL,
		PublishTopics:       ch.PublishTopics,
		RecapModel:          ch.RecapModel,
		MaxContinuations:    ch.MaxContinuations,
	}
}
