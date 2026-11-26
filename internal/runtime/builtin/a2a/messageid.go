// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"context"
	"encoding/json"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/google/uuid"
)

// messageNamespace scopes the UUIDv5 message IDs derived by a2a.send.
var messageNamespace = uuid.MustParse("7c2f6a0e-3b4d-5c8a-9e1f-4d2a6b8c0e13")

// defaultMessageID derives the ID of one logical step invocation. A
// retry_policy retry keeps the attempt, step, repeat index, and text, so it
// resends the same ID; the other executions change one of them.
func defaultMessageID(ctx context.Context, text string) string {
	env := runtime.GetEnv(ctx)
	var name, repeat string
	if env.Scope != nil {
		name, _ = env.Scope.Get(runenv.EnvKeyDAGRunStepName)
		repeat, _ = env.Scope.Get(runenv.EnvKeyDAGRunStepRepeatIndex)
	}
	if repeat == "" {
		repeat = "0"
	}
	// A JSON array is length-delimited, so no field boundary is ambiguous.
	// The foreach item keeps the iterations of a foreach body apart.
	key, _ := json.Marshal([]string{runtime.GetDAGContext(ctx).AttemptID, name, repeat, text, env.ForeachItem})
	return uuid.NewSHA1(messageNamespace, key).String()
}

func messageID(ctx context.Context, cfg sendConfig) string {
	if cfg.MessageID != nil {
		return *cfg.MessageID
	}
	return defaultMessageID(ctx, cfg.Message)
}
