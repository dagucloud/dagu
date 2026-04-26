// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"maps"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// SetupChatMessages exports setupChatMessages for testing.
func (r *Runner) SetupChatMessages(ctx context.Context, node *Node) {
	r.setupChatMessages(ctx, node)
}

// SetupPushBackConversation exports setupPushBackConversation for testing.
func (r *Runner) SetupPushBackConversation(ctx context.Context, node *Node) {
	r.setupPushBackConversation(ctx, node)
}

// SetApprovalIteration sets the approval iteration count for testing.
func (n *Node) SetApprovalIteration(iteration int) {
	n.Data.mu.Lock()
	defer n.Data.mu.Unlock()
	n.inner.State.ApprovalIteration = iteration
}

// SetPushBackInputs sets the latest push-back inputs for testing.
func (n *Node) SetPushBackInputs(inputs map[string]string) {
	n.Data.mu.Lock()
	defer n.Data.mu.Unlock()
	n.inner.State.PushBackInputs = maps.Clone(inputs)
}

// SetPushBackHistory sets push-back history for testing.
func (n *Node) SetPushBackHistory(history []exec.PushBackEntry) {
	n.Data.mu.Lock()
	defer n.Data.mu.Unlock()
	n.inner.State.PushBackHistory = exec.ClonePushBackHistory(history)
}
