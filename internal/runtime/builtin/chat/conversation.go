// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
)

// conversation is the message history of one model attempt. It keeps the
// provider's record of each assistant turn next to the message, so the turn
// is sent back as the provider produced it. The records stay out of the
// saved session: they are bound to this conversation and this model.
type conversation struct {
	messages []ir.LLMMessage
	// states maps a message index to the provider state of that turn.
	states map[int]*llmpkg.ProviderState
}

// newConversation starts a conversation from a copy of messages, so adding
// turns never writes into the caller's slice.
func newConversation(messages []ir.LLMMessage) *conversation {
	return &conversation{
		messages: slices.Clone(messages),
		states:   make(map[int]*llmpkg.ProviderState),
	}
}

// add appends a message together with the provider state of the turn it
// records, which may be nil.
func (c *conversation) add(msg ir.LLMMessage, state *llmpkg.ProviderState) {
	if state != nil {
		c.states[len(c.messages)] = state
	}
	c.messages = append(c.messages, msg)
}

// request returns the history in provider form with secrets masked.
func (c *conversation) request(ctx context.Context) []llmpkg.Message {
	msgs := toLLMMessages(maskSecretsForProvider(ctx, c.messages))
	for i, state := range c.states {
		msgs[i].ProviderState = state
	}
	return msgs
}
