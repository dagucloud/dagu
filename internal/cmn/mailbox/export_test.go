// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"context"

	"google.golang.org/api/option"
)

// DialGmailAt is DialGmail against the Gmail API at endpoint.
func DialGmailAt(ctx context.Context, account Account, endpoint string) (*Gmail, error) {
	return dialGmail(ctx, account, option.WithEndpoint(endpoint))
}
