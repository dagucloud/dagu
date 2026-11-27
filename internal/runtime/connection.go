// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// connectionTokenLabel keys a step's connection token among the values its
// output masks. No variable name holds a space, so it never shadows one.
const connectionTokenLabel = "connection token"

// stepConnection returns the name of the connection whose token step
// sends, or "".
func stepConnection(step ir.Step) string {
	if name := registry.ExecutorCapabilitiesFor(step.ExecutorConfig.Type).Connection; name != nil {
		return name(step)
	}
	return ""
}

// withConnectionToken gets the token of the connection that step uses
// before the step's output is set up, so that its logs and outputs mask the
// token from the start (Spec 107 §Masking). The run's connections keep the
// token, and the step's executor takes the same one. A failure is left to
// the executor, which reports it in the step's terms.
func withConnectionToken(ctx context.Context, step ir.Step) context.Context {
	name := stepConnection(step)
	rCtx, ok := LookupDAGContext(ctx)
	if name == "" || !ok || rCtx.Connections == nil {
		return ctx
	}
	conn, err := rCtx.Connections.Connection(ctx, name)
	if err != nil {
		return ctx
	}
	// A step's token is for the connection's only resource.
	token, _, err := rCtx.Connections.Token(ctx, conn, "", broker.StepMinTTL)
	if err != nil {
		return ctx
	}
	rCtx.EnvScope = rCtx.EnvScope.WithMaskedSecrets(map[string]string{connectionTokenLabel: token})
	return WithDAGContext(ctx, rCtx)
}
