// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

const askInteractionPrefix = "ask-"

// ErrAskRejected reports that a person rejected an ask operation.
var ErrAskRejected = errors.New("the input request was rejected")

// AskAnswer is a response to an ask operation that the step has not
// applied.
type AskAnswer struct {
	InteractionID string
	Rejected      bool
}

// askInteractionID names the interaction for the ask operation at index.
func askInteractionID(index, generation int) string {
	return fmt.Sprintf("%s%d-%d", askInteractionPrefix, index, generation)
}

// parseAskInteractionID returns the operation index and generation encoded
// in an ask interaction ID.
func parseAskInteractionID(id string) (index, generation int, ok bool) {
	rest, found := strings.CutPrefix(id, askInteractionPrefix)
	if !found {
		return 0, 0, false
	}
	indexText, generationText, found := strings.Cut(rest, "-")
	if !found {
		return 0, 0, false
	}
	index, err := strconv.Atoi(indexText)
	if err != nil {
		return 0, 0, false
	}
	generation, err = strconv.Atoi(generationText)
	if err != nil {
		return 0, 0, false
	}
	return index, generation, true
}

// AskInteraction is the pending question for the ask operation at index.
func AskInteraction(index, generation int, header, prompt string, deadline time.Time) ir.AgentInteraction {
	return ir.AgentInteraction{
		ID:     askInteractionID(index, generation),
		Kind:   ir.AgentInteractionQuestion,
		Status: ir.AgentInteractionPending,
		Questions: []ir.AgentQuestion{{
			Header:   header,
			Question: prompt,
			Custom:   true,
		}},
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ExpiresAt: deadline.UTC().Format(time.RFC3339Nano),
	}
}

// PendingAnswer returns the current generation's answered or rejected ask
// that has not been applied yet, for a session of provider.
func PendingAnswer(session *ir.AgentSession, provider string) (AskAnswer, bool) {
	if session == nil || session.Provider != provider {
		return AskAnswer{}, false
	}
	for _, interaction := range session.Interactions {
		_, generation, ok := parseAskInteractionID(interaction.ID)
		if !ok || generation != session.Generation || interaction.Applied {
			continue
		}
		switch interaction.Status {
		case ir.AgentInteractionRejected:
			return AskAnswer{InteractionID: interaction.ID, Rejected: true}, true
		case ir.AgentInteractionAnswered:
			return AskAnswer{InteractionID: interaction.ID}, true
		case ir.AgentInteractionPending:
		}
	}
	return AskAnswer{}, false
}

// AnsweredAsks returns the answers given in the current generation, by the
// index of their ask operation.
func AnsweredAsks(session *ir.AgentSession) map[int]string {
	answers := map[int]string{}
	for _, interaction := range session.Interactions {
		index, generation, ok := parseAskInteractionID(interaction.ID)
		if !ok || generation != session.Generation || interaction.Status != ir.AgentInteractionAnswered {
			continue
		}
		answer := ""
		if len(interaction.Answers) > 0 && len(interaction.Answers[0]) > 0 {
			answer = interaction.Answers[0][0]
		}
		answers[index] = answer
	}
	return answers
}

// MarkApplied records that the step used the answer of an interaction.
func MarkApplied(session *ir.AgentSession, interactionID string) {
	for i := range session.Interactions {
		if session.Interactions[i].ID == interactionID {
			session.Interactions[i].Applied = true
		}
	}
}
