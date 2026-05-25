// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package filesession provides a file-based implementation of the SessionStore interface.
package filesession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
)

const (
	sessionFileExtension   = ".json"
	sessionDirPermissions  = 0750
	sessionFilePermissions = 0600
	maxTitleLength         = 50
)

// SessionForStorage is the JSON-serializable format for sessions.
type SessionForStorage struct {
	ID              string          `json:"id"`
	UserID          string          `json:"user_id"`
	DAGName         string          `json:"dag_name,omitempty"`
	Title           string          `json:"title,omitempty"`
	Model           string          `json:"model,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ParentSessionID string          `json:"parent_session_id,omitempty"`
	DelegateTask    string          `json:"delegate_task,omitempty"`
	Messages        []agent.Message `json:"messages"`
}

// ToSession converts a SessionForStorage to an agent.Session.
func (c *SessionForStorage) ToSession() *agent.Session {
	return &agent.Session{
		ID:              c.ID,
		UserID:          c.UserID,
		DAGName:         c.DAGName,
		Title:           c.Title,
		Model:           c.Model,
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
		ParentSessionID: c.ParentSessionID,
		DelegateTask:    c.DelegateTask,
	}
}

// FromSession creates a SessionForStorage from an agent.Session.
func FromSession(sess *agent.Session, messages []agent.Message) *SessionForStorage {
	return &SessionForStorage{
		ID:              sess.ID,
		UserID:          sess.UserID,
		DAGName:         sess.DAGName,
		Title:           sess.Title,
		Model:           sess.Model,
		CreatedAt:       sess.CreatedAt,
		UpdatedAt:       sess.UpdatedAt,
		ParentSessionID: sess.ParentSessionID,
		DelegateTask:    sess.DelegateTask,
		Messages:        messages,
	}
}

// Store implements agent.SessionStore using the local filesystem.
// Sessions are stored as individual JSON files in user-specific directories.
// Thread-safe through internal locking.
type Store struct {
	baseDir    string
	maxPerUser int // 0 = unlimited
	mu         sync.RWMutex
	byID       map[string]string    // session ID -> file path
	byUser     map[string][]string  // user ID -> session IDs (sorted by UpdatedAt descending)
	byParent   map[string][]string  // parent session ID -> child session IDs
	updatedAt  map[string]time.Time // session ID -> last update time
}

var _ agent.SessionStore = (*Store)(nil)

// Option is a functional option for configuring the Store.
type Option func(*Store)

// WithMaxPerUser configures automatic cleanup of the oldest sessions when a
// user exceeds the given limit. A value of 0 or negative disables cleanup.
func WithMaxPerUser(n int) Option {
	return func(s *Store) { s.maxPerUser = n }
}

// New creates a new file-based session store.
// The baseDir must be non-empty; provided Option functions are applied to the store.
// If baseDir does not exist it is created with directory permissions 0750, and an initial
// in-memory index is built from existing session files. Returns an error on invalid input,
// failure to create the directory, or failure to build the initial index.
func New(baseDir string, opts ...Option) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("filesession: baseDir cannot be empty")
	}

	store := &Store{
		baseDir:   baseDir,
		byID:      make(map[string]string),
		byUser:    make(map[string][]string),
		byParent:  make(map[string][]string),
		updatedAt: make(map[string]time.Time),
	}

	for _, opt := range opts {
		opt(store)
	}

	// Create base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, sessionDirPermissions); err != nil {
		return nil, fmt.Errorf("filesession: failed to create directory %s: %w", baseDir, err)
	}

	// Build initial index
	if err := store.rebuildIndex(); err != nil {
		return nil, fmt.Errorf("filesession: failed to build index: %w", err)
	}

	return store, nil
}

// rebuildIndex scans the directory and rebuilds the in-memory index.
func (s *Store) rebuildIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID = make(map[string]string)
	s.byUser = make(map[string][]string)
	s.byParent = make(map[string][]string)
	s.updatedAt = make(map[string]time.Time)

	userDirs, err := os.ReadDir(s.baseDir)
	if err != nil {
		return fmt.Errorf("filesession: failed to read directory %s: %w", s.baseDir, err)
	}

	for _, userDir := range userDirs {
		if !userDir.IsDir() {
			continue
		}
		s.indexUserDirectory(userDir.Name())
	}

	for userID := range s.byUser {
		s.sortUserSessions(userID)
	}

	return nil
}

// indexUserDirectory indexes all sessions in a user's directory.
// Must be called with mu held.
func (s *Store) indexUserDirectory(userID string) {
	userPath := filepath.Join(s.baseDir, userID)
	entries, err := os.ReadDir(userPath)
	if err != nil {
		slog.Warn("Failed to read user directory during index rebuild",
			slog.String("path", userPath),
			slog.String("error", err.Error()))
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != sessionFileExtension {
			continue
		}

		filePath := filepath.Join(userPath, entry.Name())
		sess, err := s.loadSessionFromFile(filePath)
		if err != nil {
			slog.Warn("Failed to load session file during index rebuild",
				slog.String("file", filePath),
				slog.String("error", err.Error()))
			continue
		}

		s.byID[sess.ID] = filePath
		s.byUser[sess.UserID] = append(s.byUser[sess.UserID], sess.ID)
		s.updatedAt[sess.ID] = sess.UpdatedAt
		if sess.ParentSessionID != "" {
			s.byParent[sess.ParentSessionID] = append(s.byParent[sess.ParentSessionID], sess.ID)
		}
	}
}

// sortUserSessions sorts a user's session list by UpdatedAt descending.
// Must be called with mu held.
func (s *Store) sortUserSessions(userID string) {
	sessIDs := s.byUser[userID]
	sort.Slice(sessIDs, func(i, j int) bool {
		return s.updatedAt[sessIDs[i]].After(s.updatedAt[sessIDs[j]])
	})
}

// loadSessionFromFile reads and parses a session from a JSON file.
func (s *Store) loadSessionFromFile(filePath string) (*SessionForStorage, error) {
	data, err := fileutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("filesession: failed to read file %s: %w", filePath, err)
	}

	var stored SessionForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("filesession: failed to parse session file %s: %w", filePath, err)
	}

	return &stored, nil
}

// sessionFilePath returns the file path for a session.
func (s *Store) sessionFilePath(userID, sessionID string) string {
	return filepath.Join(s.baseDir, userID, sessionID+sessionFileExtension)
}

// userDirPath returns the directory path for a user.
func (s *Store) userDirPath(userID string) string {
	return filepath.Join(s.baseDir, userID)
}

// CreateSession creates a new session.
func (s *Store) CreateSession(_ context.Context, sess *agent.Session) error {
	if err := validateSession(sess, true); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byID[sess.ID]; exists {
		return fmt.Errorf("filesession: session %s already exists", sess.ID)
	}

	userDir := s.userDirPath(sess.UserID)
	if err := os.MkdirAll(userDir, sessionDirPermissions); err != nil {
		return fmt.Errorf("filesession: failed to create user directory %s: %w", userDir, err)
	}

	filePath := s.sessionFilePath(sess.UserID, sess.ID)
	if err := s.writeSessionToFile(filePath, FromSession(sess, nil)); err != nil {
		return err
	}

	s.byID[sess.ID] = filePath
	s.byUser[sess.UserID] = append(s.byUser[sess.UserID], sess.ID)
	s.updatedAt[sess.ID] = sess.UpdatedAt
	if sess.ParentSessionID != "" {
		s.byParent[sess.ParentSessionID] = append(s.byParent[sess.ParentSessionID], sess.ID)
	}
	s.sortUserSessions(sess.UserID)
	s.enforceMaxSessionsLocked(sess.UserID)

	return nil
}

// validateSession checks that a session is valid for storage operations.
func validateSession(sess *agent.Session, requireUserID bool) error {
	if sess == nil {
		return errors.New("filesession: session cannot be nil")
	}
	if sess.ID == "" {
		return fmt.Errorf("filesession: %w", agent.ErrInvalidSessionID)
	}
	if containsPathTraversal(sess.ID) {
		return fmt.Errorf("filesession: %w: contains invalid characters", agent.ErrInvalidSessionID)
	}
	if requireUserID && sess.UserID == "" {
		return fmt.Errorf("filesession: %w", agent.ErrInvalidUserID)
	}
	if requireUserID && containsPathTraversal(sess.UserID) {
		return fmt.Errorf("filesession: %w: contains invalid characters", agent.ErrInvalidUserID)
	}
	return nil
}

// containsPathTraversal checks if an ID contains path separator or traversal characters.
func containsPathTraversal(id string) bool {
	return strings.ContainsAny(id, `/\`) || strings.Contains(id, "..")
}

// writeSessionToFile writes a session to a JSON file atomically.
func (s *Store) writeSessionToFile(filePath string, sess *SessionForStorage) error {
	if err := fileutil.WriteJSONAtomic(filePath, sess, sessionFilePermissions); err != nil {
		return fmt.Errorf("filesession: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID.
func (s *Store) GetSession(_ context.Context, id string) (*agent.Session, error) {
	if id == "" {
		return nil, agent.ErrInvalidSessionID
	}

	stored, err := s.loadSessionByID(id)
	if err != nil {
		return nil, err
	}

	return stored.ToSession(), nil
}

// ListSessions returns all sessions for a user, sorted by UpdatedAt descending.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]*agent.Session, error) {
	if userID == "" {
		return nil, agent.ErrInvalidUserID
	}

	s.mu.RLock()
	sessIDs := make([]string, len(s.byUser[userID]))
	copy(sessIDs, s.byUser[userID])
	s.mu.RUnlock()

	sessions := make([]*agent.Session, 0, len(sessIDs))
	for _, id := range sessIDs {
		sess, err := s.GetSession(ctx, id)
		if err != nil {
			// Skip sessions that can't be loaded
			if errors.Is(err, agent.ErrSessionNotFound) {
				continue
			}
			return nil, err
		}
		sessions = append(sessions, sess)
	}

	return sessions, nil
}

// UpdateSession updates session metadata.
func (s *Store) UpdateSession(_ context.Context, sess *agent.Session) error {
	if err := validateSession(sess, false); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, exists := s.byID[sess.ID]
	if !exists {
		return agent.ErrSessionNotFound
	}

	stored, err := s.loadSessionFromFile(filePath)
	if err != nil {
		return fmt.Errorf("filesession: failed to load existing session: %w", err)
	}

	stored.Title = sess.Title
	stored.UpdatedAt = sess.UpdatedAt

	if err := s.writeSessionToFile(filePath, stored); err != nil {
		return err
	}

	s.updatedAt[sess.ID] = sess.UpdatedAt
	s.sortUserSessions(stored.UserID)

	return nil
}

// DeleteSession removes a session and all its messages.
func (s *Store) DeleteSession(_ context.Context, id string) error {
	if id == "" {
		return agent.ErrInvalidSessionID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteSessionLocked(id)
}

// deleteSessionLocked removes a session file and its index entries.
// Must be called with mu held.
func (s *Store) deleteSessionLocked(id string) error {
	filePath, exists := s.byID[id]
	if !exists {
		return agent.ErrSessionNotFound
	}

	stored, err := s.loadSessionFromFile(filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filesession: failed to load session for deletion: %w", err)
	}

	if err := fileutil.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filesession: failed to delete session file: %w", err)
	}

	delete(s.byID, id)
	delete(s.updatedAt, id)

	userID := ""
	parentID := ""
	if stored != nil {
		userID = stored.UserID
		parentID = stored.ParentSessionID
	}
	s.removeSessionFromUserIndex(id, userID)
	if parentID != "" {
		s.byParent[parentID] = removeFromSlice(s.byParent[parentID], id)
		if len(s.byParent[parentID]) == 0 {
			delete(s.byParent, parentID)
		}
	}

	return nil
}

// removeSessionFromUserIndex removes a session ID from the user index.
// Must be called with mu held.
func (s *Store) removeSessionFromUserIndex(id string, userID string) {
	if userID != "" {
		s.byUser[userID] = removeFromSlice(s.byUser[userID], id)
		return
	}

	for uid, sessIDs := range s.byUser {
		if updated := removeFromSlice(sessIDs, id); len(updated) != len(sessIDs) {
			s.byUser[uid] = updated
			return
		}
	}
}

// removeFromSlice removes the first occurrence of target from slice.
func removeFromSlice(slice []string, target string) []string {
	for i, v := range slice {
		if v == target {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// enforceMaxSessionsLocked deletes the oldest top-level sessions for a user
// when the count exceeds maxPerUser. Must be called with mu held.
func (s *Store) enforceMaxSessionsLocked(userID string) {
	if s.maxPerUser <= 0 {
		return
	}

	sessIDs := s.byUser[userID] // already sorted by updatedAt desc

	// Build sub-session set to exclude from counting.
	subSessions := make(map[string]struct{})
	for _, children := range s.byParent {
		for _, childID := range children {
			subSessions[childID] = struct{}{}
		}
	}

	// Collect top-level sessions in order (newest first).
	var topLevel []string
	for _, id := range sessIDs {
		if _, isSub := subSessions[id]; !isSub {
			topLevel = append(topLevel, id)
		}
	}

	if len(topLevel) <= s.maxPerUser {
		return
	}

	// Delete excess (oldest = tail of sorted list).
	excess := topLevel[s.maxPerUser:]
	for _, id := range excess {
		// Snapshot child IDs before iterating — deleteSessionLocked mutates
		// s.byParent[id] via removeFromSlice, which shifts the backing array
		// and would cause the range loop to skip elements.
		children := append([]string{}, s.byParent[id]...)
		for _, childID := range children {
			if err := s.deleteSessionLocked(childID); err != nil {
				slog.Warn("filesession: failed to delete sub-session during cleanup",
					slog.String("session_id", childID),
					slog.String("parent_id", id),
					slog.String("error", err.Error()))
			}
		}
		if err := s.deleteSessionLocked(id); err != nil {
			slog.Warn("filesession: failed to delete session during cleanup",
				slog.String("session_id", id),
				slog.String("error", err.Error()))
		}
	}

	slog.Info("filesession: purged excess sessions",
		slog.String("user_id", userID),
		slog.Int("removed", len(excess)),
		slog.Int("max_per_user", s.maxPerUser))
}

// AddMessage appends a message to a session.
func (s *Store) AddMessage(_ context.Context, sessionID string, msg *agent.Message) error {
	if err := validateMessageInput(sessionID, msg); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, exists := s.byID[sessionID]
	if !exists {
		return agent.ErrSessionNotFound
	}

	stored, err := s.loadSessionFromFile(filePath)
	if err != nil {
		return fmt.Errorf("filesession: failed to load session: %w", err)
	}

	stored.Messages = append(stored.Messages, *msg)
	stored.UpdatedAt = time.Now()
	stored.setTitleFromMessage(msg)

	if err := s.writeSessionToFile(filePath, stored); err != nil {
		return err
	}

	s.updatedAt[sessionID] = stored.UpdatedAt
	s.sortUserSessions(stored.UserID)

	return nil
}

// validateMessageInput validates the input parameters for AddMessage.
func validateMessageInput(sessionID string, msg *agent.Message) error {
	if sessionID == "" {
		return agent.ErrInvalidSessionID
	}
	if msg == nil {
		return errors.New("filesession: message cannot be nil")
	}
	return nil
}

// setTitleFromMessage sets the session title from the first user message if not already set.
func (c *SessionForStorage) setTitleFromMessage(msg *agent.Message) {
	if c.Title == "" && msg.Type == agent.MessageTypeUser && msg.Content != "" {
		c.Title = truncateTitle(msg.Content)
	}
}

// truncateTitle truncates a title to maxTitleLength runes with ellipsis.
func truncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= maxTitleLength {
		return title
	}
	if maxTitleLength < 3 {
		return string(runes[:maxTitleLength])
	}
	return string(runes[:maxTitleLength-3]) + "..."
}

// GetMessages retrieves all messages for a session.
func (s *Store) GetMessages(_ context.Context, sessionID string) ([]agent.Message, error) {
	if sessionID == "" {
		return nil, agent.ErrInvalidSessionID
	}

	stored, err := s.loadSessionByID(sessionID)
	if err != nil {
		return nil, err
	}

	messages := make([]agent.Message, len(stored.Messages))
	copy(messages, stored.Messages)

	return messages, nil
}

// loadSessionByID looks up and loads a session by its ID.
// Note: Uses explicit RUnlock instead of defer since file I/O follows the lock.
// The file could be deleted between index lookup and read, but loadSessionFromFile
// handles os.ErrNotExist gracefully.
func (s *Store) loadSessionByID(id string) (*SessionForStorage, error) {
	s.mu.RLock()
	filePath, exists := s.byID[id]
	s.mu.RUnlock()

	if !exists {
		return nil, agent.ErrSessionNotFound
	}

	stored, err := s.loadSessionFromFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, agent.ErrSessionNotFound
		}
		return nil, fmt.Errorf("filesession: failed to load session: %w", err)
	}

	return stored, nil
}

// ListSubSessions returns all sub-sessions for a parent session.
func (s *Store) ListSubSessions(ctx context.Context, parentSessionID string) ([]*agent.Session, error) {
	if parentSessionID == "" {
		return nil, agent.ErrInvalidSessionID
	}

	s.mu.RLock()
	childIDs := make([]string, len(s.byParent[parentSessionID]))
	copy(childIDs, s.byParent[parentSessionID])
	s.mu.RUnlock()

	result := make([]*agent.Session, 0, len(childIDs))
	for _, id := range childIDs {
		sess, err := s.GetSession(ctx, id)
		if err != nil {
			if errors.Is(err, agent.ErrSessionNotFound) {
				continue
			}
			return nil, err
		}
		result = append(result, sess)
	}
	return result, nil
}

// GetLatestSequenceID returns the highest sequence ID for a session.
func (s *Store) GetLatestSequenceID(_ context.Context, sessionID string) (int64, error) {
	if sessionID == "" {
		return 0, agent.ErrInvalidSessionID
	}

	stored, err := s.loadSessionByID(sessionID)
	if err != nil {
		return 0, err
	}

	var maxSeq int64
	for _, msg := range stored.Messages {
		if msg.SequenceID > maxSeq {
			maxSeq = msg.SequenceID
		}
	}

	return maxSeq, nil
}
