// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"

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
	}
}

// clearReplayCache clears the recorded actions of a DAG's steps, or of the
// step named by --step, and reports what it removed.
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
