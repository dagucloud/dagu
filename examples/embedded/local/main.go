// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dagucloud/dagu/v2"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	homeDir, err := os.MkdirTemp("", "dagu-embedded-local-*")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(homeDir); err != nil {
			log.Printf("remove temp home: %v", err)
		}
	}()

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: homeDir})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := engine.Close(context.Background()); err != nil {
			log.Printf("close engine: %v", err)
		}
	}()

	run, err := engine.RunFile(ctx, examplePath("workflow.yaml"),
		dagu.WithParams(map[string]string{
			"TARGET": "embedded Dagu",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	status, err := run.Wait(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s finished with %s\n", status.Name, status.RunID, status.Status)
}

func examplePath(name string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("resolve example path")
	}
	return filepath.Join(filepath.Dir(file), name)
}
