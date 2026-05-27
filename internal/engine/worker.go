// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/dagucloud/dagu/internal/service/worker"
)

func (e *Engine) NewWorker(opts WorkerOptions) (*Worker, error) {
	coordinators := opts.Coordinators
	if len(coordinators) == 0 {
		coordinators = append([]string{}, e.distributed.Coordinators...)
	}
	if len(coordinators) == 0 {
		return nil, fmt.Errorf("worker requires at least one coordinator address")
	}
	tls := opts.TLS
	if tls == (TLSOptions{}) {
		tls = e.distributed.TLS
	}

	cfg := *e.cfg
	cfg.Worker.Coordinators = append([]string{}, coordinators...)
	cfg.Worker.HealthPort = opts.HealthPort
	cfg.Core.Peer.Insecure = tls.Insecure
	cfg.Core.Peer.CertFile = tls.CertFile
	cfg.Core.Peer.KeyFile = tls.KeyFile
	cfg.Core.Peer.ClientCaFile = tls.ClientCAFile
	cfg.Core.Peer.SkipTLSVerify = tls.SkipTLSVerify

	workerID := opts.ID
	if workerID == "" {
		hostname, err := os.Hostname()
		if err != nil || hostname == "" {
			hostname = "unknown"
		}
		workerID = fmt.Sprintf("%s@%d", hostname, os.Getpid())
	}
	maxActiveRuns := opts.MaxActiveRuns
	if maxActiveRuns <= 0 {
		maxActiveRuns = 100
	}
	client, err := e.coordinatorClient(DistributedOptions{
		Coordinators: coordinators,
		TLS:          tls,
	})
	if err != nil {
		return nil, err
	}
	labels := cloneStringMap(opts.Labels)
	w := worker.NewWorker(workerID, maxActiveRuns, client, labels, &cfg, worker.WithDAGRunStore(e.dagRunStore))
	w.SetHandler(worker.NewRemoteTaskHandler(worker.RemoteTaskHandlerConfig{
		WorkerID:           workerID,
		CoordinatorClient:  client,
		DAGRunStore:        e.dagRunStore,
		DAGStore:           e.dagStore,
		DAGRunMgr:          e.dagRunMgr,
		ServiceRegistry:    e.serviceRegistry,
		PeerConfig:         cfg.Core.Peer,
		Config:             &cfg,
		AgentStoresFactory: worker.AgentStoresFactory(e.agentStoresFactory),
	}))
	return &Worker{inner: w}, nil
}

func (w *Worker) Start(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return fmt.Errorf("worker is not initialized")
	}
	return w.inner.Start(ctx)
}

func (w *Worker) Stop(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return nil
	}
	return w.inner.Stop(ctx)
}

func (w *Worker) WaitReady(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return fmt.Errorf("worker is not initialized")
	}
	return w.inner.WaitReady(ctx)
}
