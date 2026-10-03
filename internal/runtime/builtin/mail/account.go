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

// mailboxClient is a signed-in mailbox, over IMAP or the Gmail API.
type mailboxClient interface {
	Search(mailbox.SearchOptions) ([]mailbox.Message, bool, error)
	Organize(mailbox.OrganizeOptions) (*mailbox.OrganizeResult, error)
	ReplyInfo(id string) (*mailbox.ReplyInfo, error)
	Close() error
}

// mailboxAccount is a resolved mail account and how its mailbox is reached.
type mailboxAccount struct {
	account mailbox.Account
	gmail   bool
}

func newMailboxAccount(address string, account *ir.MailAccount) (mailboxAccount, error) {
	token, err := tokenSource(address, account)
	if err != nil {
		return mailboxAccount{}, err
	}
	resolved := mailboxAccount{
		account: mailbox.Account{Username: account.Username, Password: account.Password, Token: token},
		gmail:   account.GmailAPI(),
	}
	if account.IMAP != nil {
		resolved.account.Server = mailbox.Server{
			Host:          account.IMAP.Host,
			Port:          account.IMAP.Port,
			Security:      account.IMAP.Security,
			SkipTLSVerify: account.IMAP.SkipTLSVerify,
		}
	}
	return resolved, nil
}

// open signs in to the mailbox. Canceling ctx ends the session.
func (a mailboxAccount) open(ctx context.Context) (mailboxClient, error) {
	if a.gmail {
		client, err := mailbox.DialGmail(ctx, a.account)
		if err != nil {
			return nil, err
		}
		return client, nil
	}
	client, err := mailbox.Dial(ctx, a.account)
	if err != nil {
		return nil, err
	}
	return client, nil
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

// accountError names the account and states a revoked or expired sign-in,
// or one without Gmail access, plainly.
func accountError(address string, err error) error {
	if tokenErr, ok := errors.AsType[*oauth.TokenError](err); ok {
		if tokenErr.Code == "invalid_grant" {
			return fmt.Errorf("mail account %q: sign-in is no longer valid (invalid_grant)", address)
		}
		return fmt.Errorf("mail account %q: sign-in failed (%w)", address, tokenErr)
	}
	if errors.Is(err, mailbox.ErrGmailScope) {
		return fmt.Errorf("mail account %q: %w", address, mailbox.ErrGmailScope)
	}
	return fmt.Errorf("mail account %q: %w", address, err)
}
