// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/spec"
)

func TestMailActions(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - id: find
    action: mail.search
    with:
      mailbox: ops@example.com
      unread: true
      within: 7d
      limit: 50
  - id: file
    action: mail.organize
    with:
      mailbox: ops@example.com
      emails: ${steps.find.outputs.messages}
      mark: read
      move: folder
      folder: Done
  - action: mail.search
    with:
      mailbox: ops@example.com
      limit: ${LIMIT}
      unread: ${UNREAD}
`))
	require.NoError(t, err)

	require.Len(t, dag.Steps, 3)
	find := dag.Steps[0]
	assert.Equal(t, "mail", find.ExecutorConfig.Type)
	require.Len(t, find.Commands, 1)
	assert.Equal(t, "search", find.Commands[0].Command)
	assert.Equal(t, "organize", dag.Steps[1].Commands[0].Command)
	assert.Nil(t, dag.Artifacts, "no step saves attachments")
}

func TestMailActionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		step    string
		wantErr string
	}{
		{
			name:    "SearchWithoutMailbox",
			step:    "action: mail.search\n    with: {unread: true}",
			wantErr: "mail.search requires with.mailbox",
		},
		{
			name:    "LimitOutOfRange",
			step:    "action: mail.search\n    with: {mailbox: ops@example.com, limit: 51}",
			wantErr: "with.limit must be an integer from 1 to 50",
		},
		{
			name:    "BadWithin",
			step:    "action: mail.search\n    with: {mailbox: ops@example.com, within: soon}",
			wantErr: "with.within must be a duration such as 24h or 7d",
		},
		{
			name:    "UnknownSearchField",
			step:    "action: mail.search\n    with: {mailbox: ops@example.com, label: x}",
			wantErr: `unsupported mail.search field "label"`,
		},
		{
			name:    "OrganizeWithoutMarkOrMove",
			step:    "action: mail.organize\n    with: {mailbox: ops@example.com, emails: id}",
			wantErr: "mail.organize requires with.mark or with.move",
		},
		{
			name:    "UnknownMark",
			step:    "action: mail.organize\n    with: {mailbox: ops@example.com, emails: id, mark: starred}",
			wantErr: "with.mark must be one of read, unread, flagged, unflagged",
		},
		{
			name:    "UnknownMove",
			step:    "action: mail.organize\n    with: {mailbox: ops@example.com, emails: id, move: delete}",
			wantErr: "with.move must be one of folder, archive, trash",
		},
		{
			name:    "EmailsNotAnEmail",
			step:    "action: mail.organize\n    with: {mailbox: ops@example.com, emails: 42, mark: read}",
			wantErr: "with.emails must be an email ID, an email object, or a non-empty list of them",
		},
		{
			name:    "EmailsEmptyList",
			step:    "action: mail.organize\n    with: {mailbox: ops@example.com, emails: [], mark: read}",
			wantErr: "with.emails must be an email ID, an email object, or a non-empty list of them",
		},
		{
			name:    "OutputOverride",
			step:    "action: mail.search\n    output: EMAILS\n    with: {mailbox: ops@example.com}",
			wantErr: "mail.search actions have fixed outputs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte("steps:\n  - "+tt.step+"\n"))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestMailSearchSavingAttachmentsEnablesArtifacts(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - action: mail.search
    with:
      mailbox: ops@example.com
      save_attachments: true
`))
	require.NoError(t, err)
	require.NotNil(t, dag.Artifacts)
	assert.True(t, dag.Artifacts.Enabled)
}
