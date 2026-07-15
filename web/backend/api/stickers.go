// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/sticker"
)

// respondWithStickerJSON writes a JSON response with proper content-type.
func respondWithStickerJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

// respondWithStickerError writes a JSON error response.
func respondWithStickerError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

func (h *Handler) registerStickerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/telegram/stickers", h.handleListStickers)
	mux.HandleFunc("GET /api/telegram/stickers/{id}/image", h.handleStickerImage)
	mux.HandleFunc("POST /api/telegram/stickers/manual", h.handleManualUploadSticker)
	mux.HandleFunc("POST /api/telegram/stickers/import-set", h.handleImportStickerSet)
	mux.HandleFunc("POST /api/telegram/stickers/batch-delete", h.handleBatchDeleteStickers)
	mux.HandleFunc("DELETE /api/telegram/stickers/{id}", h.handleDeleteSticker)
}

// GET /api/telegram/stickers
func (h *Handler) handleListStickers(w http.ResponseWriter, r *http.Request) {
	store, err := sticker.NewStickerStore()
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to initialize sticker store: %v", err))
		return
	}
	items, err := store.GetAll()
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read stickers: %v", err))
		return
	}
	respondWithStickerJSON(w, http.StatusOK, map[string]interface{}{
		"stickers": items,
	})
}

// GET /api/telegram/stickers/{id}/image
func (h *Handler) handleStickerImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing sticker ID", http.StatusBadRequest)
		return
	}

	store, err := sticker.NewStickerStore()
	if err != nil {
		http.Error(w, "Failed to initialize sticker store", http.StatusInternalServerError)
		return
	}

	item, found, err := store.GetByID(id)
	if err != nil || !found {
		http.Error(w, "Sticker not found", http.StatusNotFound)
		return
	}

	if item.FilePath == "" {
		http.Error(w, "No file path for sticker", http.StatusNotFound)
		return
	}

	// Detect content type from file extension
	ext := strings.ToLower(filepath.Ext(item.FilePath))
	contentType := "image/webp"
	switch ext {
	case ".jpg", ".jpeg":
		contentType = "image/jpeg"
	case ".png":
		contentType = "image/png"
	case ".gif":
		contentType = "image/gif"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, item.FilePath)
}

// POST /api/telegram/stickers/manual
func (h *Handler) handleManualUploadSticker(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
		respondWithStickerError(w, http.StatusBadRequest, "Failed to parse form data")
		return
	}

	id := strings.TrimSpace(r.FormValue("id"))
	emojiHint := strings.TrimSpace(r.FormValue("emoji_hint"))
	description := strings.TrimSpace(r.FormValue("description"))
	usageScenarios := strings.TrimSpace(r.FormValue("usage_scenarios"))

	if id == "" || description == "" || usageScenarios == "" {
		respondWithStickerError(w, http.StatusBadRequest, "id, description, and usage_scenarios are required")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		respondWithStickerError(w, http.StatusBadRequest, "Missing file upload")
		return
	}
	defer file.Close()

	store, err := sticker.NewStickerStore()
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to initialize sticker store: %v", err))
		return
	}

	// Determine file extension from the uploaded filename
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".webp"
	}
	fileName := id + ext
	filePath := filepath.Join(store.MediaDir(), fileName)

	// Save the uploaded file
	dst, err := os.Create(filePath)
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save file: %v", err))
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to write file: %v", err))
		return
	}

	item := sticker.StickerItem{
		ID:             id,
		SourceType:     sticker.SourceManual,
		FilePath:       filePath,
		EmojiHint:      emojiHint,
		Description:    description,
		UsageScenarios: usageScenarios,
		CreatedAt:      time.Now(),
	}

	if err := store.Add(item); err != nil {
		// Clean up the file on failure
		_ = os.Remove(filePath)
		if strings.Contains(err.Error(), "already exists") {
			respondWithStickerError(w, http.StatusConflict, fmt.Sprintf("Sticker with ID %q already exists", id))
		} else {
			respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save sticker: %v", err))
		}
		return
	}

	respondWithStickerJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"message": fmt.Sprintf("Sticker %q uploaded successfully", id),
	})
}

type importSetRequest struct {
	StickerSetName string `json:"sticker_set_name"`
}

// POST /api/telegram/stickers/import-set
func (h *Handler) handleImportStickerSet(w http.ResponseWriter, r *http.Request) {
	var req importSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithStickerError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	req.StickerSetName = strings.TrimSpace(req.StickerSetName)
	if req.StickerSetName == "" {
		respondWithStickerError(w, http.StatusBadRequest, "sticker_set_name is required")
		return
	}

	// Strip t.me/addstickers/ prefix if user pasted a full link
	if idx := strings.LastIndex(req.StickerSetName, "/"); idx >= 0 {
		req.StickerSetName = req.StickerSetName[idx+1:]
	}

	store, err := sticker.NewStickerStore()
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to initialize sticker store: %v", err))
		return
	}

	// Load config to get Telegram token and default LLM model
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to load config: %v", err))
		return
	}

	// Get Telegram channel config for bot token
	tgChannel := cfg.Channels["telegram"]
	if tgChannel == nil || !tgChannel.Enabled {
		respondWithStickerError(w, http.StatusBadRequest, "Telegram channel is not configured or enabled")
		return
	}

	var tgCfg config.TelegramSettings
	if tgChannel.Settings != nil {
		if err := tgChannel.Settings.Decode(&tgCfg); err != nil {
			respondWithStickerError(w, http.StatusInternalServerError, "Failed to decode Telegram settings")
			return
		}
	}
	if tgCfg.Token.String() == "" {
		respondWithStickerError(w, http.StatusBadRequest, "Telegram bot token is not configured")
		return
	}

	// Get default LLM model name for multimodal description generation
	modelName := cfg.Agents.Defaults.GetModelName()
	log.Printf("[Sticker Import] Starting import of sticker set %q using default model: %s", req.StickerSetName, modelName)

	// Create LLM provider for multimodal description generation
	llmProvider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create LLM provider: %v", err))
		return
	}

	// Import sticker set via Telegram Bot API
	importer := sticker.NewSetImporter(tgCfg.Token.String(), tgCfg.BaseURL, tgCfg.Proxy, store, llmProvider)
	result, err := importer.ImportSet(r.Context(), req.StickerSetName)
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to import sticker set: %v", err))
		return
	}

	respondWithStickerJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"message": fmt.Sprintf("Imported %d stickers from set %q", len(result), req.StickerSetName),
		"count":   len(result),
	})
}

// DELETE /api/telegram/stickers/{id}
func (h *Handler) handleDeleteSticker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondWithStickerError(w, http.StatusBadRequest, "Sticker ID is required")
		return
	}

	store, err := sticker.NewStickerStore()
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to initialize sticker store: %v", err))
		return
	}

	if err := store.Delete(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondWithStickerError(w, http.StatusNotFound, fmt.Sprintf("Sticker %q not found", id))
		} else {
			respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete sticker: %v", err))
		}
		return
	}

	respondWithStickerJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"message": fmt.Sprintf("Sticker %q deleted", id),
	})
}

type batchDeleteRequest struct {
	IDs []string `json:"ids"`
}

// POST /api/telegram/stickers/batch-delete
func (h *Handler) handleBatchDeleteStickers(w http.ResponseWriter, r *http.Request) {
	var req batchDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithStickerError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if len(req.IDs) == 0 {
		respondWithStickerError(w, http.StatusBadRequest, "ids list is empty")
		return
	}

	store, err := sticker.NewStickerStore()
	if err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to initialize sticker store: %v", err))
		return
	}

	if err := store.BatchDelete(req.IDs); err != nil {
		respondWithStickerError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to batch delete: %v", err))
		return
	}

	respondWithStickerJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"message": fmt.Sprintf("Deleted %d stickers", len(req.IDs)),
	})
}
