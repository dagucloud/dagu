// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/worker"
)

type ExecutionMode string

const (
	ExecutionModeLocal       ExecutionMode = "local"
	ExecutionModeDistributed ExecutionMode = "distributed"
)

type Options struct {
	HomeDir     string
	ConfigFile  string
	DAGsDir     string
	DataDir     string
	LogDir      string
	ArtifactDir string
	BaseConfig  string
	Logger      *slog.Logger

	Persistence        Persistence
	PersistenceFactory PersistenceFactory
	RunStateStore      runstate.Store
	DAGRunStore        coreexec.DAGRunStore
	DefaultMode        ExecutionMode
	Distributed        *DistributedOptions
}

type DistributedOptions struct {
	Coordinators    []string
	TLS             TLSOptions
	WorkerSelector  map[string]string
	PollInterval    time.Duration
	MaxStatusErrors int
}

type TLSOptions struct {
	Insecure      bool
	CertFile      string
	KeyFile       string
	ClientCAFile  string
	SkipTLSVerify bool
}

type RunOptions struct {
	RunID             string
	Name              string
	Params            map[string]string
	ParamsList        []string
	DefaultWorkingDir string
	Mode              ExecutionMode
	WorkerSelector    map[string]string
	Labels            []string
	DryRun            bool
}

type RunRef struct {
	Name string
	ID   string
}

type Status struct {
	Name        string
	RunID       string
	AttemptID   string
	Status      string
	StartedAt   time.Time
	FinishedAt  time.Time
	Error       string
	LogFile     string
	ArchiveDir  string
	WorkerID    string
	TriggerType string
}

type Run struct {
	engine *Engine
	ref    RunRef
	mode   ExecutionMode

	cancel context.CancelFunc
	done   chan struct{}

	doneOnce  sync.Once
	doneErrMu sync.RWMutex
	doneErr   error

	agent *agent.Agent
	dag   *core.DAG

	coordinator coordinator.Client
}

type WorkerOptions struct {
	ID            string
	MaxActiveRuns int
	Labels        map[string]string
	Coordinators  []string
	TLS           TLSOptions
	HealthPort    int
}

type Worker struct {
	inner *worker.Worker
}

type localPreparation struct {
	attempt coreexec.DAGRunAttempt
	proc    coreexec.ProcHandle
}
