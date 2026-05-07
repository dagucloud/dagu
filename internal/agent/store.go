// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
)

// Sentinel errors for session store operations.
var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrInvalidSessionID = errors.New("invalid session ID")
	ErrInvalidUserID    = errors.New("invalid user ID")
)

// ConfigStore provides access to agent configuration.
// All implementations must be safe for concurrent use.
type ConfigStore interface {
	// Load reads the agent configuration.
	Load(ctx context.Context) (*Config, error)
	// Save writes the agent configuration.
	Save(ctx context.Context, cfg *Config) error
	// IsEnabled returns whether the agent feature is enabled.
	IsEnabled(ctx context.Context) bool
}

// ModelStore defines the interface for model configuration persistence.
// All implementations must be safe for concurrent use.
type ModelStore interface {
	Create(ctx context.Context, model *ModelConfig) error
	GetByID(ctx context.Context, id string) (*ModelConfig, error)
	List(ctx context.Context) ([]*ModelConfig, error)
	Update(ctx context.Context, model *ModelConfig) error
	Delete(ctx context.Context, id string) error
}

// MemoryStore provides access to agent memory files.
// All implementations must be safe for concurrent use.
type MemoryStore interface {
	// LoadGlobalMemory reads the global MEMORY.md, truncated to maxLines.
	LoadGlobalMemory(ctx context.Context) (string, error)

	// LoadDAGMemory reads the MEMORY.md for a specific DAG, truncated to maxLines.
	LoadDAGMemory(ctx context.Context, dagName string) (string, error)

	// SaveGlobalMemory writes content to the global MEMORY.md.
	SaveGlobalMemory(ctx context.Context, content string) error

	// SaveDAGMemory writes content to a DAG-specific MEMORY.md.
	SaveDAGMemory(ctx context.Context, dagName string, content string) error

	// MemoryDir returns the root memory directory path.
	MemoryDir() string

	// ListDAGMemories returns the names of all DAGs that have memory files.
	ListDAGMemories(ctx context.Context) ([]string, error)

	// DeleteGlobalMemory removes the global MEMORY.md file.
	DeleteGlobalMemory(ctx context.Context) error

	// DeleteDAGMemory removes a DAG-specific MEMORY.md file.
	DeleteDAGMemory(ctx context.Context, dagName string) error
}

// SessionStore defines the interface for session persistence.
// All implementations must be safe for concurrent use.
type SessionStore interface {
	// CreateSession creates a new session.
	// Returns ErrInvalidSessionID if sess.ID is empty.
	// Returns ErrInvalidUserID if sess.UserID is empty.
	CreateSession(ctx context.Context, sess *Session) error

	// GetSession retrieves a session by ID.
	// Returns ErrInvalidSessionID if id is empty.
	// Returns ErrSessionNotFound if the session does not exist.
	GetSession(ctx context.Context, id string) (*Session, error)

	// ListSessions returns all sessions for a user, sorted by UpdatedAt descending.
	// Returns ErrInvalidUserID if userID is empty.
	// Returns an empty slice if the user has no sessions.
	ListSessions(ctx context.Context, userID string) ([]*Session, error)

	// UpdateSession updates session metadata such as Title and UpdatedAt.
	// Returns ErrSessionNotFound if the session does not exist.
	UpdateSession(ctx context.Context, sess *Session) error

	// DeleteSession removes a session and all its messages.
	// Returns ErrInvalidSessionID if id is empty.
	// Returns ErrSessionNotFound if the session does not exist.
	DeleteSession(ctx context.Context, id string) error

	// AddMessage appends a message to a session and updates the session's UpdatedAt.
	// Returns ErrInvalidSessionID if sessionID is empty.
	// Returns ErrSessionNotFound if the session does not exist.
	AddMessage(ctx context.Context, sessionID string, msg *Message) error

	// GetMessages retrieves all messages for a session, ordered by SequenceID ascending.
	// Returns ErrInvalidSessionID if sessionID is empty.
	// Returns ErrSessionNotFound if the session does not exist.
	GetMessages(ctx context.Context, sessionID string) ([]Message, error)

	// GetLatestSequenceID returns the highest sequence ID for a session.
	// Returns 0 if the session has no messages.
	// Returns ErrInvalidSessionID if sessionID is empty.
	// Returns ErrSessionNotFound if the session does not exist.
	GetLatestSequenceID(ctx context.Context, sessionID string) (int64, error)

	// ListSubSessions returns all sub-sessions for a parent session.
	// Returns an empty slice if no sub-sessions exist.
	// Returns ErrInvalidSessionID if parentSessionID is empty.
	ListSubSessions(ctx context.Context, parentSessionID string) ([]*Session, error)
}
