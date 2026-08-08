// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incremental

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

// MaterializationSchemaVersion identifies the persisted manifest format.
const MaterializationSchemaVersion = 1

var (
	// ErrMaterializationNotFound indicates that no committed manifest exists.
	ErrMaterializationNotFound = errors.New("materialization not found")
	// ErrMaterializationRecovery indicates that an incomplete commit could not be recovered.
	ErrMaterializationRecovery = errors.New("materialization recovery failed")
)

// PathLockMode identifies how a materialization path is protected.
type PathLockMode string

const (
	PathLockShared    PathLockMode = "shared"
	PathLockExclusive PathLockMode = "exclusive"
)

// PathLockRequest requests protection for one canonical filesystem path.
type PathLockRequest struct {
	Key  string
	Mode PathLockMode
}

// MaterializationLock protects all paths involved in one evaluation.
type MaterializationLock interface {
	Release() error
}

// FileSnapshot is a verified regular-file content snapshot.
type FileSnapshot struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// Materialization records one committed incremental output.
type Materialization struct {
	SchemaVersion      int              `json:"schemaVersion"`
	MaterializationKey string           `json:"materializationKey"`
	CommitID           string           `json:"commitId"`
	DAGName            string           `json:"dagName"`
	StepID             string           `json:"stepId"`
	RecipeDigest       string           `json:"recipeDigest"`
	Fingerprint        string           `json:"fingerprint"`
	Inputs             []FileSnapshot   `json:"inputs,omitempty"`
	Output             FileSnapshot     `json:"output"`
	ProducerRun        dagrun.DAGRunRef `json:"producerRun"`
	ProducerAttemptID  string           `json:"producerAttemptId,omitempty"`
	CompletedAt        time.Time        `json:"completedAt"`
}

// MaterializationCommit describes publication of a staged output and its manifest state.
type MaterializationCommit struct {
	StagingPath      string
	FinalPath        string
	Manifest         Materialization
	PreserveManifest bool
}

// MaterializationStore persists manifests and coordinates path access.
type MaterializationStore interface {
	Get(ctx context.Context, key string) (*Materialization, error)
	AcquirePaths(ctx context.Context, requests []PathLockRequest) (MaterializationLock, error)
	Commit(ctx context.Context, lock MaterializationLock, req MaterializationCommit) error
}
