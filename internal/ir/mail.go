// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"slices"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
)

// Mail account providers select default IMAP and SMTP servers.
const (
	MailProviderGoogle    = "google"
	MailProviderMicrosoft = "microsoft"
	MailProviderIMAP      = "imap"
)

// Mail server security modes. Every mail account connection uses TLS.
const (
	MailSecurityTLS      = "tls"
	MailSecurityStartTLS = "starttls"
)

// mail.search returns between MailSearchMinLimit and MailSearchMaxLimit emails.
const (
	MailSearchDefaultLimit = 20
	MailSearchMinLimit     = 1
	MailSearchMaxLimit     = 50
)

// MailMarks returns the flag changes mail.organize accepts.
func MailMarks() []string {
	return []string{"read", "unread", "flagged", "unflagged"}
}

// MailMoves returns the destinations mail.organize accepts.
func MailMoves() []string {
	return []string{"folder", "archive", "trash"}
}

// MailAccounts maps lowercase email addresses to the accounts mail actions use.
type MailAccounts map[string]*MailAccount

// MailAccount is a mailbox that mail actions read from and send through.
type MailAccount struct {
	Provider string
	IMAP     *MailServer
	SMTP     *MailServer
	// Username is the login name; the account's address when not configured.
	Username string
	Password string
	OAuth    *oauthconfig.Config
}

// GmailAPI reports whether the account reaches its mailbox through the Gmail
// API instead of IMAP and SMTP. Such an account has no servers.
func (a *MailAccount) GmailAPI() bool {
	return a.Provider == MailProviderGoogle && a.OAuth != nil
}

// MailServer is the IMAP or SMTP server of a mail account.
type MailServer struct {
	Host          string
	Port          string
	Security      string
	SkipTLSVerify bool
}

// Clone returns a deep copy of the accounts.
func (m MailAccounts) Clone() MailAccounts {
	if m == nil {
		return nil
	}
	cloned := make(MailAccounts, len(m))
	for address, account := range m {
		cloned[address] = account.Clone()
	}
	return cloned
}

// Clone returns a deep copy of the account.
func (a *MailAccount) Clone() *MailAccount {
	if a == nil {
		return nil
	}
	cloned := *a
	cloned.IMAP = a.IMAP.clone()
	cloned.SMTP = a.SMTP.clone()
	if a.OAuth != nil {
		oauth := *a.OAuth
		oauth.Scopes = slices.Clone(a.OAuth.Scopes)
		cloned.OAuth = &oauth
	}
	return &cloned
}

func (s *MailServer) clone() *MailServer {
	if s == nil {
		return nil
	}
	cloned := *s
	return &cloned
}
