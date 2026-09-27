// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/google/jsonschema-go/jsonschema"
)

// configSchema covers every mail operation: send, search, and organize. Fields
// that take booleans or numbers also accept value references.
var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"mailbox": {Type: "string", Description: "Address of a mail account from mail_accounts"},
		"from":    {Type: "string", Description: "Sender email address (send), or a sender substring to match (search)"},
		"to":      {Description: "Recipient email address(es) - string or array of strings"},
		"subject": {Type: "string", Description: "Email subject line (send), or a subject substring to match (search)"},
		"message": {Type: "string", Description: "Email body content"},
		"attachments": {
			Type:        "array",
			Items:       &jsonschema.Schema{Type: "string"},
			Description: "File paths to attach",
		},
		"folder":           {Type: "string", Description: "Folder to search (default INBOX), or the destination of move: folder"},
		"unread":           {Description: "Return only unread email"},
		"within":           {Type: "string", Description: "Return email received within this duration, such as 24h or 7d"},
		"has_attachments":  {Description: "Return only email with attachments"},
		"save_attachments": {Description: "Save attachments into the run's artifacts"},
		"limit":            {Description: "Most emails to return, 1 to 50 (default 20)"},
		"emails":           {Description: "Email ID, email object, or a list of them"},
		"mark":             {Type: "string", Description: "read, unread, flagged, or unflagged"},
		"move":             {Type: "string", Description: "folder, archive, or trash"},
		"dry_run":          {Description: "Report what would change without changing anything"},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("mail", configSchema)
}
