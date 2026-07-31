// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagu_test

import (
	"context"
	"log"

	"github.com/dagucloud/dagu/v2"
)

func Example() {
	ctx := context.Background()

	engine, err := dagu.New(ctx, dagu.Options{
		HomeDir: "/var/lib/myapp/dagu",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := engine.Close(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()

	run, err := engine.RunYAML(ctx, []byte(`
name: embedded
steps:
  - name: hello
    command: echo "$MESSAGE"
`), dagu.WithParams(map[string]string{"MESSAGE": "hello"}))
	if err != nil {
		log.Fatal(err)
	}

	status, err := run.Wait(ctx)
	if err != nil {
		log.Fatal(err)
	}
	_ = status
}

func Example_distributed() {
	ctx := context.Background()

	engine, err := dagu.New(ctx, dagu.Options{
		HomeDir:     "/var/lib/myapp/dagu-worker",
		DefaultMode: dagu.ExecutionModeDistributed,
		Distributed: &dagu.DistributedOptions{
			Coordinators: []string{"127.0.0.1:50055"},
			TLS:          dagu.TLSOptions{Insecure: true},
			WorkerSelector: map[string]string{
				"pool": "default",
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := engine.Close(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()

	worker, err := engine.NewWorker(dagu.WorkerOptions{
		Labels: map[string]string{"pool": "default"},
	})
	if err != nil {
		log.Fatal(err)
	}

	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	go func() {
		if err := worker.Start(workerCtx); err != nil {
			log.Print(err)
		}
	}()
	if err := worker.WaitReady(ctx); err != nil {
		log.Fatal(err)
	}

	run, err := engine.RunFile(ctx, "daily-report.yaml")
	if err != nil {
		log.Fatal(err)
	}
	status, err := run.Wait(ctx)
	if err != nil {
		log.Fatal(err)
	}
	_ = status
}
