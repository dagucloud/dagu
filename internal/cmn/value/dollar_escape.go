// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
)

type dollarEscapeToken struct {
	token string
}

type dollarEscapeKey struct{}

var dollarEscapeSeq uint64

// withDollarEscapes replaces \$ with sentinel tokens so Dagu expansion won't
// treat them as variable prefixes. Only an unescaped backslash directly
// preceding $ is treated as an escape; other backslashes are preserved.
func withDollarEscapes(ctx context.Context, input string) (context.Context, string) {
	if !strings.Contains(input, "\\$") {
		return ctx, input
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token := uniqueToken(input, "__DAGU_DOLLAR_ESC__")
	var b strings.Builder
	b.Grow(len(input))

	for i := 0; i < len(input); {
		if input[i] != '\\' {
			b.WriteByte(input[i])
			i++
			continue
		}

		start := i
		for i < len(input) && input[i] == '\\' {
			i++
		}
		count := i - start

		if i < len(input) && input[i] == '$' && count%2 == 1 {
			for range count - 1 {
				b.WriteByte('\\')
			}
			b.WriteString(token)
			i++ // consume $
			continue
		}

		for range count {
			b.WriteByte('\\')
		}
	}

	tokens := dollarEscapeToken{token: token}
	ctx = context.WithValue(ctx, dollarEscapeKey{}, tokens)
	return ctx, b.String()
}

// unescapeDollars restores $ from sentinel tokens.
func unescapeDollars(ctx context.Context, input string) string {
	if ctx == nil {
		return input
	}
	tokens, ok := ctx.Value(dollarEscapeKey{}).(dollarEscapeToken)
	if !ok {
		return input
	}
	if tokens.token == "" {
		return input
	}
	return strings.ReplaceAll(input, tokens.token, "$")
}

func uniqueToken(input, base string) string {
	const maxTokenAttempts = 1024
	for range maxTokenAttempts {
		id := atomic.AddUint64(&dollarEscapeSeq, 1)
		token := fmt.Sprintf("%s%d__", base, id)
		if !strings.Contains(input, token) {
			return token
		}
	}
	// Fallback to ensure termination even if input is pathological.
	id := atomic.AddUint64(&dollarEscapeSeq, 1)
	return fmt.Sprintf("%s%d__fallback__", base, id)
}
