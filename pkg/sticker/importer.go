// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package sticker

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mymmrac/telego"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const multimodalPrompt = "你是一个表情包分析助手。请用一两句简练、生动、客观的话，描述这张表情包中角色的形象、表情动作、所传达的情绪，以及它适合用在什么聊天场景或语境中。请直接返回核心描述，不要有任何多余的开场白或修饰。"

// SetImporter handles importing a Telegram sticker set, downloading files,
// and generating multimodal descriptions via LLM.
type SetImporter struct {
	botToken   string
	baseURL    string
	proxy      string
	store      *StickerStore
	llmProvider providers.LLMProvider
	httpClient  *http.Client
}

// NewSetImporter creates a new SetImporter.
func NewSetImporter(
	botToken string,
	baseURL string,
	proxy string,
	store *StickerStore,
	llmProvider providers.LLMProvider,
) *SetImporter {
	transport := &http.Transport{}
	if proxy != "" {
		if proxyURL, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	return &SetImporter{
		botToken:    botToken,
		baseURL:     baseURL,
		proxy:       proxy,
		store:       store,
		llmProvider: llmProvider,
		httpClient:  &http.Client{Transport: transport, Timeout: 60 * time.Second},
	}
}

// ImportSet fetches a Telegram sticker set, downloads all stickers,
// generates multimodal descriptions, and saves them to the store.
func (si *SetImporter) ImportSet(ctx context.Context, setName string) ([]StickerItem, error) {
	// Create a telego bot for API calls
	botOpts := []telego.BotOption{
		telego.WithHTTPClient(si.httpClient),
	}
	if si.baseURL != "" {
		baseURL := strings.TrimRight(strings.TrimSpace(si.baseURL), "/")
		if baseURL != "" {
			botOpts = append(botOpts, telego.WithAPIServer(baseURL))
		}
	}

	bot, err := telego.NewBot(si.botToken, botOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create telego bot: %w", err)
	}

	// Fetch the sticker set from Telegram
	stickerSet, err := bot.GetStickerSet(ctx, &telego.GetStickerSetParams{
		Name: setName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get sticker set %q: %w", setName, err)
	}

	log.Printf("[Sticker Import] Fetched sticker set %q with %d stickers", setName, len(stickerSet.Stickers))

	var imported []StickerItem

	for i, s := range stickerSet.Stickers {
		select {
		case <-ctx.Done():
			return imported, ctx.Err()
		default:
		}

		item, err := si.importOneSticker(ctx, bot, setName, s, i)
		if err != nil {
			log.Printf("[Sticker Import] Warning: failed to import sticker %d: %v", i, err)
			continue
		}
		imported = append(imported, item)
	}

	return imported, nil
}

func (si *SetImporter) importOneSticker(
	ctx context.Context,
	bot *telego.Bot,
	setName string,
	s telego.Sticker,
	index int,
) (StickerItem, error) {
	var targetFileID string
	var ext string

	// Format downgrade: animated/video stickers -> static thumbnail
	if (s.IsAnimated || s.IsVideo) && s.Thumbnail != nil {
		targetFileID = s.Thumbnail.FileID
		ext = ".jpg"
		log.Printf("[Sticker Import] Sticker %d: animated/video, using thumbnail", index)
	} else {
		targetFileID = s.FileID
		ext = ".webp"
	}

	// Download the file from Telegram
	localPath, err := si.downloadTelegramFile(ctx, bot, targetFileID, ext)
	if err != nil {
		return StickerItem{}, fmt.Errorf("failed to download sticker: %w", err)
	}

	// Copy to our media directory
	id := fmt.Sprintf("%s_%d", setName, index)
	destFileName := id + ext
	destPath := filepath.Join(si.store.MediaDir(), destFileName)

	if err := copyFile(localPath, destPath); err != nil {
		return StickerItem{}, fmt.Errorf("failed to copy sticker file: %w", err)
	}

	// Generate multimodal description via LLM
	description := ""
	if si.llmProvider != nil {
		desc, err := si.generateDescription(ctx, destPath)
		if err != nil {
			log.Printf("[Sticker Import] Warning: failed to generate description for sticker %d: %v", index, err)
		} else {
			description = desc
		}
	}

	// Extract emoji hint from sticker
	emojiHint := ""
	if s.Emoji != "" {
		emojiHint = s.Emoji
	}

	item := StickerItem{
		ID:             id,
		SourceType:     SourceTelegramSet,
		StickerSetName: setName,
		FilePath:       destPath,
		TelegramFileID: s.FileID, // Cache the original file_id for direct sending
		EmojiHint:      emojiHint,
		Description:    description,
		UsageScenarios: "", // Will be inferred by LLM from description if needed
		CreatedAt:      time.Now(),
	}

	// Save to store
	if err := si.store.Add(item); err != nil {
		// If ID conflict, try with a different ID
		id = fmt.Sprintf("%s_%d_%d", setName, index, time.Now().UnixMilli())
		item.ID = id
		if err2 := si.store.Add(item); err2 != nil {
			return StickerItem{}, fmt.Errorf("failed to save sticker: %w", err2)
		}
	}

	return item, nil
}

func (si *SetImporter) downloadTelegramFile(ctx context.Context, bot *telego.Bot, fileID, ext string) (string, error) {
	file, err := bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("GetFile failed: %w", err)
	}

	if file.FilePath == "" {
		return "", fmt.Errorf("empty file path from Telegram API")
	}

	// Build download URL
	downloadURL := bot.FileDownloadURL(file.FilePath)

	// Download the file
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := si.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Save to temp file
	tmpDir := filepath.Join(os.TempDir(), "picoclaw_sticker_import")
	_ = os.MkdirAll(tmpDir, 0700)
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("sticker_%s%s", fileID[:min(16, len(fileID))], ext))

	dst, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, resp.Body); err != nil {
		return "", fmt.Errorf("failed to save downloaded file: %w", err)
	}

	return tmpFile, nil
}

func (si *SetImporter) generateDescription(ctx context.Context, imagePath string) (string, error) {
	// Read image and encode as a data URL (the standard multimodal input format in this codebase)
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to read image: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(imagePath))
	mimeType := "image/webp"
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	}

	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(imageData)

	// Build multimodal message for the LLM using the Media field (data URL format)
	messages := []providers.Message{
		{
			Role:  "user",
			Content: multimodalPrompt,
			Media: []string{dataURL},
		},
	}

	log.Printf("[Sticker Import] Generating multimodal description using model: %s", si.llmProvider.GetDefaultModel())

	resp, err := si.llmProvider.Chat(ctx, messages, nil, si.llmProvider.GetDefaultModel(), nil)
	if err != nil {
		return "", fmt.Errorf("LLM Chat failed: %w", err)
	}

	if resp.Content == "" {
		return "", fmt.Errorf("LLM returned empty description")
	}

	return strings.TrimSpace(resp.Content), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
