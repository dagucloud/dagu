// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dagucloud/dagu"
)

func main() {
	dagu.RegisterExecutor(
		"embedded_echo",
		func(_ context.Context, step dagu.Step) (dagu.Executor, error) {
			return &embeddedEchoExecutor{step: step}, nil
		},
		dagu.WithExecutorCapabilities(dagu.ExecutorCapabilities{Command: true}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	homeDir, err := os.MkdirTemp("", "dagu-embedded-custom-*")
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

	run, err := engine.RunFile(ctx, examplePath("workflow.yaml"))
	if err != nil {
		log.Fatal(err)
	}
	status, err := run.Wait(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s finished with %s\n", status.Name, status.Status)
}

type embeddedEchoExecutor struct {
	step   dagu.Step
	stdout io.Writer
	stderr io.Writer
}

func (e *embeddedEchoExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *embeddedEchoExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *embeddedEchoExecutor) Kill(os.Signal) error {
	return nil
}

func (e *embeddedEchoExecutor) Run(context.Context) error {
	out := e.stdout
	if out == nil {
		out = os.Stdout
	}
	command := e.step.Command
	if command == "" && len(e.step.Commands) > 0 {
		command = e.step.Commands[0].CmdWithArgs
	}
	if command == "" {
		if message, ok := e.step.ExecutorConfig.Config["message"].(string); ok {
			command = message
		}
	}
	_, err := fmt.Fprintf(out, "custom executor handled step %q with command %q\n", e.step.Name, command)
	return err
}

func examplePath(name string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("resolve example path")
	}
	return filepath.Join(filepath.Dir(file), name)
}
