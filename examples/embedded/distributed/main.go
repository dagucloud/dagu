// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2"
)

func main() {
	coordinators := coordinatorAddrs()
	if len(coordinators) == 0 {
		log.Fatal("set DAGU_COORDINATORS, for example: DAGU_COORDINATORS=127.0.0.1:50055")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	homeDir, err := os.MkdirTemp("", "dagu-embedded-distributed-*")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(homeDir); err != nil {
			log.Printf("remove temp home: %v", err)
		}
	}()

	engine, err := dagu.New(ctx, dagu.Options{
		HomeDir:     homeDir,
		DefaultMode: dagu.ExecutionModeDistributed,
		Distributed: &dagu.DistributedOptions{
			Coordinators:   coordinators,
			TLS:            dagu.TLSOptions{Insecure: true},
			PollInterval:   time.Second,
			WorkerSelector: map[string]string{"pool": "embedded-example"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := engine.Close(context.Background()); err != nil {
			log.Printf("close engine: %v", err)
		}
	}()

	worker, err := engine.NewWorker(dagu.WorkerOptions{
		ID:            "embedded-example-worker",
		MaxActiveRuns: 4,
		Labels:        map[string]string{"pool": "embedded-example"},
	})
	if err != nil {
		log.Fatal(err)
	}

	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	go func() {
		if err := worker.Start(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("worker stopped: %v", err)
		}
	}()
	if err := worker.WaitReady(ctx); err != nil {
		log.Fatal(err)
	}

	run, err := engine.RunFile(ctx, examplePath("workflow.yaml"))
	if err != nil {
		log.Fatal(err)
	}
	status, err := run.Wait(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s finished with %s on worker %s\n", status.Name, status.RunID, status.Status, status.WorkerID)
}

func coordinatorAddrs() []string {
	value := os.Getenv("DAGU_COORDINATORS")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if addr := strings.TrimSpace(part); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func examplePath(name string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("resolve example path")
	}
	return filepath.Join(filepath.Dir(file), name)
}
