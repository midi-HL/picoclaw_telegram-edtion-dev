// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package sticker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StickerSource indicates how a sticker was added.
type StickerSource string

const (
	SourceManual       StickerSource = "manual"        // Mode A: local upload
	SourceTelegramSet  StickerSource = "telegram_set"  // Mode B: Telegram sticker set import
)

// StickerItem represents a single sticker entry in the shared database.
type StickerItem struct {
	ID             string        `json:"id"`
	SourceType     StickerSource `json:"source_type"`
	StickerSetName string        `json:"sticker_set_name,omitempty"`
	FilePath       string        `json:"file_path"`
	TelegramFileID string        `json:"telegram_file_id,omitempty"`
	EmojiHint      string        `json:"emoji_hint,omitempty"`
	Description    string        `json:"description"`
	UsageScenarios string        `json:"usage_scenarios"`
	CreatedAt      time.Time     `json:"created_at"`
}

// StickerDatabase is the top-level JSON structure stored on disk.
type StickerDatabase struct {
	Stickers []StickerItem `json:"stickers"`
}

// StickerStore provides concurrent-safe CRUD operations on the shared
// ~/.picoclaw/telegram_stickers.json file. Both Launcher and Gateway
// processes read/write this same file to stay in sync.
type StickerStore struct {
	mu       sync.RWMutex
	filePath string
	mediaDir string
}

// NewStickerStore creates (or opens) the shared sticker store.
// It resolves ~/.picoclaw/ dynamically via os.UserHomeDir() and
// auto-creates the directory tree if missing.
func NewStickerStore() (*StickerStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("sticker store: failed to get user home dir: %w", err)
	}

	dir := filepath.Join(home, ".picoclaw")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("sticker store: failed to create storage directory: %w", err)
	}

	mediaDir := filepath.Join(dir, "media", "stickers")
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return nil, fmt.Errorf("sticker store: failed to create media directory: %w", err)
	}

	filePath := filepath.Join(dir, "telegram_stickers.json")
	store := &StickerStore{filePath: filePath, mediaDir: mediaDir}

	// Initialize the file if it doesn't exist
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		initial := StickerDatabase{Stickers: []StickerItem{}}
		data, _ := json.MarshalIndent(initial, "", "  ")
		if writeErr := os.WriteFile(filePath, data, 0644); writeErr != nil {
			return nil, fmt.Errorf("sticker store: failed to initialize database: %w", writeErr)
		}
	}

	return store, nil
}

// MediaDir returns the path to the sticker media cache directory.
func (s *StickerStore) MediaDir() string {
	return s.mediaDir
}

// FilePath returns the path to the shared JSON database.
func (s *StickerStore) FilePath() string {
	return s.filePath
}

// GetAll returns a copy of all sticker items.
func (s *StickerStore) GetAll() ([]StickerItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	db, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	if db.Stickers == nil {
		return []StickerItem{}, nil
	}
	return db.Stickers, nil
}

// GetByID returns a single sticker by its ID.
func (s *StickerStore) GetByID(id string) (StickerItem, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	db, err := s.loadLocked()
	if err != nil {
		return StickerItem{}, false, err
	}
	for _, item := range db.Stickers {
		if item.ID == id {
			return item, true, nil
		}
	}
	return StickerItem{}, false, nil
}

// Add inserts a new sticker item. Returns an error if the ID already exists.
func (s *StickerStore) Add(item StickerItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.loadLocked()
	if err != nil {
		return err
	}
	for _, existing := range db.Stickers {
		if existing.ID == item.ID {
			return fmt.Errorf("sticker with ID %q already exists", item.ID)
		}
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	db.Stickers = append(db.Stickers, item)
	return s.saveLocked(db)
}

// Update replaces an existing sticker item (matched by ID).
func (s *StickerStore) Update(item StickerItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.loadLocked()
	if err != nil {
		return err
	}
	for i, existing := range db.Stickers {
		if existing.ID == item.ID {
			db.Stickers[i] = item
			return s.saveLocked(db)
		}
	}
	return fmt.Errorf("sticker with ID %q not found", item.ID)
}

// Delete removes a sticker by ID and optionally deletes its media file.
func (s *StickerStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.loadLocked()
	if err != nil {
		return err
	}
	found := false
	var remaining []StickerItem
	var filePath string
	for _, item := range db.Stickers {
		if item.ID == id {
			found = true
			filePath = item.FilePath
		} else {
			remaining = append(remaining, item)
		}
	}
	if !found {
		return fmt.Errorf("sticker with ID %q not found", id)
	}
	db.Stickers = remaining
	if err := s.saveLocked(db); err != nil {
		return err
	}
	// Best-effort delete of the media file
	if filePath != "" {
		_ = os.Remove(filePath)
	}
	return nil
}

// BatchDelete removes multiple stickers by ID.
func (s *StickerStore) BatchDelete(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	db, err := s.loadLocked()
	if err != nil {
		return err
	}

	var remaining []StickerItem
	var filesToDelete []string
	for _, item := range db.Stickers {
		if _, ok := idSet[item.ID]; ok {
			if item.FilePath != "" {
				filesToDelete = append(filesToDelete, item.FilePath)
			}
		} else {
			remaining = append(remaining, item)
		}
	}
	db.Stickers = remaining
	if err := s.saveLocked(db); err != nil {
		return err
	}
	// Best-effort delete media files
	for _, fp := range filesToDelete {
		_ = os.Remove(fp)
	}
	return nil
}

// BackfeedFileID updates the TelegramFileID for a sticker (used after first upload).
func (s *StickerStore) BackfeedFileID(id string, fileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.loadLocked()
	if err != nil {
		return err
	}
	for i, item := range db.Stickers {
		if item.ID == id {
			db.Stickers[i].TelegramFileID = fileID
			return s.saveLocked(db)
		}
	}
	return fmt.Errorf("sticker with ID %q not found", id)
}

// loadLocked reads and deserializes the JSON database. Caller must hold at least RLock.
func (s *StickerStore) loadLocked() (StickerDatabase, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return StickerDatabase{}, fmt.Errorf("sticker store: failed to read database: %w", err)
	}
	var db StickerDatabase
	if err := json.Unmarshal(data, &db); err != nil {
		return StickerDatabase{}, fmt.Errorf("sticker store: failed to parse database: %w", err)
	}
	return db, nil
}

// saveLocked serializes and writes the JSON database. Caller must hold Lock.
func (s *StickerStore) saveLocked(db StickerDatabase) error {
	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return fmt.Errorf("sticker store: failed to serialize database: %w", err)
	}
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("sticker store: failed to write database: %w", err)
	}
	return nil
}
