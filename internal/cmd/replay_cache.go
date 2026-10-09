// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
)

// namedReplayCache is the replay cache of one step type, such as browser.
type namedReplayCache struct {
	kind  string
	store *replaycache.Store
}

// replayCaches lists the replay caches of every step type that records
// actions.
func replayCaches(ctx *Context) []namedReplayCache {
	return []namedReplayCache{
		{kind: browserhost.AgentProvider, store: browserReplayCache(ctx)},
		{kind: computerhost.AgentProvider, store: computerReplayCache(ctx)},
		{kind: xlsxCacheKind, store: xlsxReplayCache(ctx)},
	}
}

// clearReplayCache clears the recorded actions of a DAG's steps, or of the
// step named by --step, and reports what it removed.
// replayCacheOpFlag names one task of a step by its position in with.do,
// counted from 1 as the run log and the timeline do.
var replayCacheOpFlag = commandLineFlag{
	name:  "op",
	usage: "With --step: forget only the task at this position in with.do, counted from 1",
}

// clearReplayCacheOp forgets the recording of one task of a step, so the
// next run asks the model for that task and replays the rest.
func clearReplayCacheOp(ctx *Context, dagArg string, cache namedReplayCache, opArg string) error {
	dagName, err := extractDAGName(ctx, dagArg)
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}
	step, err := ctx.StringParam(replayCacheStepFlag.name)
	if err != nil {
		return err
	}
	if step == "" {
		return errors.New("--op needs --step")
	}
	position, err := strconv.Atoi(opArg)
	if err != nil || position < 1 {
		return fmt.Errorf("--op must be a position in with.do from 1, not %q", opArg)
	}
	removed, err := cache.store.Drop(ctx, dagName, step, func(entry json.RawMessage) bool {
		var recorded struct {
			Op int `json:"op"`
		}
		return json.Unmarshal(entry, &recorded) == nil && recorded.Op == position-1
	})
	if err != nil {
		return fmt.Errorf("failed to forget task %d of step %q of %q: %w", position, step, dagName, err)
	}
	if ctx.Quiet {
		return nil
	}
	out := ctx.Command.OutOrStdout()
	if removed == 0 {
		_, err = fmt.Fprintf(out, "No recording of task %d of step %q of DAG %q\n", position, step, dagName)
		return err
	}
	_, err = fmt.Fprintf(out, "Forgot the recording of task %d of step %q of DAG %q\n", position, step, dagName)
	return err
}

func clearReplayCache(ctx *Context, dagArg string, cache namedReplayCache) error {
	dagName, err := extractDAGName(ctx, dagArg)
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}
	step, err := ctx.StringParam(replayCacheStepFlag.name)
	if err != nil {
		return err
	}

	removed, err := cache.store.Clear(dagName, step)
	if err != nil {
		return fmt.Errorf("failed to clear %s replay cache for %q: %w", cache.kind, dagName, err)
	}
	if ctx.Quiet {
		return nil
	}

	out := ctx.Command.OutOrStdout()
	if len(removed) == 0 {
		if step != "" {
			_, err = fmt.Fprintf(out, "No %s replay cache for step %q of DAG %q\n", cache.kind, step, dagName)
		} else {
			_, err = fmt.Fprintf(out, "No %s replay cache for DAG %q\n", cache.kind, dagName)
		}
		return err
	}
	for _, s := range removed {
		if _, err := fmt.Fprintf(out, "Removed %s replay cache for step %q of DAG %q\n", cache.kind, s, dagName); err != nil {
			return err
		}
	}
	return nil
}
