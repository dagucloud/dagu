// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
)

type startupResolution struct {
	env               runtimeenv.Result
	envErr            error
	secrets           []string
	secretErr         error
	profileValues     resolvedProfileValues
	profileErr        error
	profileName       string
	profileResolvedAt string
	profileEntries    []ir.RuntimeProfileEntry
	panicValue        any
	panicStack        []byte
}

func (a *Agent) resolveStartup(ctx context.Context) (startupResolution, error) {
	if err := ctx.Err(); err != nil {
		return startupResolution{}, err
	}
	// ponytail: Providers can outlive cancellation. Results must not mutate the run.
	snapshot := &Agent{
		dag:                     a.dag.Clone(),
		profileName:             a.profileName,
		profileResolver:         a.profileResolver,
		profileStore:            a.profileStore,
		secretStore:             a.secretStore,
		secretReferenceResolver: a.secretReferenceResolver,
	}
	resolved := make(chan startupResolution, 1)
	go func() {
		var result startupResolution
		defer func() {
			result.panicValue = recover()
			if result.panicValue != nil {
				result.panicStack = debug.Stack()
			}
			resolved <- result
		}()
		result.env, result.envErr = runtimeenv.Resolve(ctx, snapshot.dag)
		snapshot.dag.Env = result.env.Env
		snapshot.dag.RuntimeResolved = true
		if ctx.Err() != nil {
			return
		}
		result.profileValues, result.profileErr = snapshot.resolveProfile(ctx)
		result.profileName = snapshot.profileName
		result.profileResolvedAt = snapshot.profileResolvedAt
		result.profileEntries = snapshot.profileEntries
		// Secrets read profile entries. After a profile failure they are
		// skipped, so the profile error is the one reported.
		if result.profileErr != nil || ctx.Err() != nil {
			return
		}
		result.secrets, result.secretErr = snapshot.resolveSecrets(ctx, result.profileValues)
	}()
	select {
	case result := <-resolved:
		if err := ctx.Err(); err != nil {
			return startupResolution{}, err
		}
		if result.panicValue != nil {
			logRecoveredPanic(ctx, result.panicValue, result.panicStack)
			panic(result.panicValue)
		}
		return result, nil
	case <-ctx.Done():
		return startupResolution{}, ctx.Err()
	}
}

func (a *Agent) abortStartup(ctx context.Context, attempt runstate.Attempt) (runErr error) {
	defer a.finished.Store(true)
	ctx = context.WithoutCancel(ctx)
	if !a.dry {
		if attempt == nil {
			if err := a.checkIsAlreadyRunning(ctx); err != nil {
				return err
			}
			var err error
			attempt, err = a.setupAttempt(ctx)
			if err != nil {
				return fmt.Errorf("failed to setup execution history: %w", err)
			}
		}
		a.lock.Lock()
		a.dagRunAttemptID = attempt.ID()
		a.lock.Unlock()
		if err := attempt.Open(ctx); err != nil {
			return fmt.Errorf("failed to open execution history: %w", err)
		}
		defer func() { runErr = errors.Join(runErr, attempt.Close(ctx)) }()
	}
	return a.recordStartupAbort(ctx, attempt)
}

func (a *Agent) recordStartupAbort(ctx context.Context, attempt runstate.Attempt) error {
	a.lock.Lock()
	a.startupCancel = nil
	a.startupFinishedAt = time.Now()
	a.lock.Unlock()
	a.finished.Store(true)
	if a.dry {
		return nil
	}
	return a.writeStatus(context.WithoutCancel(ctx), attempt, a.Status(ctx))
}

func bindStartupContext(ctx, startupCtx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(startupCtx, cancel)
	if startupCtx.Err() != nil {
		cancel()
	}
	return ctx, func() {
		stop()
		cancel()
	}
}
