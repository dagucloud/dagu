// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailbox"
	"github.com/dagucloud/dagu/v2/internal/cmn/mailer"
	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauth"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// accountAddress is the lookup key and display form of a mailbox field.
func accountAddress(mailbox string) string {
	return strings.ToLower(strings.TrimSpace(mailbox))
}

func imapAccount(address string, account *ir.MailAccount) (mailbox.Account, error) {
	token, err := tokenSource(address, account)
	if err != nil {
		return mailbox.Account{}, err
	}
	return mailbox.Account{
		Server: mailbox.Server{
			Host:          account.IMAP.Host,
			Port:          account.IMAP.Port,
			Security:      account.IMAP.Security,
			SkipTLSVerify: account.IMAP.SkipTLSVerify,
		},
		Username: account.Username,
		Password: account.Password,
		Token:    token,
	}, nil
}

func smtpConfig(address string, account *ir.MailAccount) (mailer.Config, error) {
	if account.SMTP == nil {
		return mailer.Config{}, fmt.Errorf("mail account %q has no SMTP server", address)
	}
	token, err := tokenSource(address, account)
	if err != nil {
		return mailer.Config{}, err
	}
	return mailer.Config{
		Host:          account.SMTP.Host,
		Port:          account.SMTP.Port,
		Username:      account.Username,
		Password:      account.Password,
		Token:         token,
		Security:      account.SMTP.Security,
		SkipTLSVerify: account.SMTP.SkipTLSVerify,
	}, nil
}

func tokenSource(address string, account *ir.MailAccount) (func(context.Context) (*oauth2.Token, error), error) {
	if account.OAuth == nil {
		return nil, nil
	}
	token, err := oauth.NewRefreshTokenFunc(account.Username, account.OAuth)
	if err != nil {
		return nil, fmt.Errorf("mail account %q: %w", address, err)
	}
	return token, nil
}

// accountError names the account and states a revoked or expired sign-in
// plainly.
func accountError(address string, err error) error {
	if tokenErr, ok := errors.AsType[*oauth.TokenError](err); ok {
		if tokenErr.Code == "invalid_grant" {
			return fmt.Errorf("mail account %q: sign-in is no longer valid (invalid_grant)", address)
		}
		return fmt.Errorf("mail account %q: sign-in failed (%w)", address, tokenErr)
	}
	return fmt.Errorf("mail account %q: %w", address, err)
}
