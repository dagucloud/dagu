// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/diagnostic"
)

type diagnosticContextKey struct{}

type diagnosticContext struct {
	sink diagnostic.Sink
}

// WithDiagnosticSink returns a context that records value-resolution diagnostics.
func WithDiagnosticSink(ctx context.Context, sink diagnostic.Sink) context.Context {
	if sink == nil {
		return ctx
	}
	current, _ := ctx.Value(diagnosticContextKey{}).(diagnosticContext)
	current.sink = sink
	return context.WithValue(ctx, diagnosticContextKey{}, current)
}

func addUnresolvedReferenceDiagnostic(ctx context.Context, field, token string, err error) {
	diagCtx, ok := ctx.Value(diagnosticContextKey{}).(diagnosticContext)
	if !ok || diagCtx.sink == nil {
		return
	}
	refName := token
	if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
		refName = token[2 : len(token)-1]
	}
	message := fmt.Sprintf("%s was left unchanged because %s had no value when %s was evaluated.", token, refName, field)
	if field == "" {
		message = fmt.Sprintf("%s was left unchanged because %s had no value when the field was evaluated.", token, refName)
	}
	if err != nil {
		message += " " + err.Error() + "."
	}
	diagCtx.sink.AddDiagnostic(diagnostic.Diagnostic{
		Level:   diagnostic.LevelNotice,
		Code:    diagnostic.CodeValueReferenceUnresolved,
		Field:   field,
		Token:   token,
		Message: message,
	})
}
