// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// gmailUser names the signed-in account in Gmail API paths.
const gmailUser = "me"

// Labels every Gmail mailbox has.
const (
	labelInbox   = "INBOX"
	labelUnread  = "UNREAD"
	labelStarred = "STARRED"
	labelSpam    = "SPAM"
	labelTrash   = "TRASH"
)

// gmailFolders maps the folders Gmail shows over IMAP, after one of
// gmailFolderPrefixes, to their labels, so a workflow written for Gmail over
// IMAP keeps working. All Mail is every email, which no label marks.
var gmailFolders = map[string]string{
	"All Mail":  "",
	"Sent Mail": "SENT",
	"Drafts":    "DRAFT",
	"Starred":   labelStarred,
	"Important": "IMPORTANT",
	"Spam":      labelSpam,
	"Trash":     labelTrash,
	"Bin":       labelTrash,
}

var gmailFolderPrefixes = []string{"[Gmail]/", "[Google Mail]/"}

// ErrGmailScope means the account's sign-in does not grant access to Gmail.
var ErrGmailScope = errors.New("the sign-in does not grant Gmail access " +
	"(needs https://www.googleapis.com/auth/gmail.modify or https://mail.google.com/)")

var (
	errNoLabel       = errors.New("no such label")
	errReservedLabel = errors.New("the name is reserved for Gmail's own folders")
)

// Gmail is a mailbox reached through the Gmail API.
type Gmail struct {
	ctx       context.Context
	users     *gmail.UsersService
	transport *http.Transport
	// labels maps label names to IDs once listed.
	labels map[string]string
}

// DialGmail signs in to the account's mailbox through the Gmail API with the
// account's OAuth token. Canceling ctx ends any request.
func DialGmail(ctx context.Context, account Account) (*Gmail, error) {
	return dialGmail(ctx, account, ioIdleTimeout)
}

func dialGmail(ctx context.Context, account Account, idle time.Duration) (*Gmail, error) {
	if account.Token == nil {
		return nil, errors.New("the Gmail API needs an OAuth token source")
	}
	// A token obtained up front reports a revoked sign-in before any request.
	token, err := account.Token(ctx)
	if err != nil {
		return nil, err
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("OAuth provider returned an empty access token")
	}
	// A request fails when its connection makes no progress for idle, as an
	// IMAP connection does, so a large message on a slow link still arrives.
	dialer := &net.Dialer{Timeout: dialTimeout}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &idleConn{Conn: conn, timeout: idle}, nil
		},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: dialTimeout,
		IdleConnTimeout:     idle,
	}
	client := &http.Client{Transport: &oauth2.Transport{
		Source: tokenSource{ctx: ctx, token: account.Token},
		Base:   transport,
	}}
	options := []option.ClientOption{option.WithHTTPClient(client)}
	if account.GmailEndpoint != "" {
		options = append(options, option.WithEndpoint(account.GmailEndpoint))
	}
	service, err := gmail.NewService(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &Gmail{ctx: ctx, users: service.Users, transport: transport}, nil
}

// Close releases the connections kept for later requests; the Gmail API keeps
// no session.
func (g *Gmail) Close() error {
	g.transport.CloseIdleConnections()
	return nil
}

// Send sends raw, an RFC 5322 message, from the mailbox. A thread ID files the
// message in that conversation.
func (g *Gmail) Send(raw []byte, threadID string) error {
	_, err := g.users.Messages.Send(gmailUser, &gmail.Message{ThreadId: threadID}).
		Media(bytes.NewReader(raw), googleapi.ContentType("message/rfc822"), googleapi.ChunkSize(0)).
		Context(g.ctx).
		Do()
	if err != nil {
		return gmailError(err)
	}
	return nil
}

// labelID returns the label a folder stands for: a label's name, or a folder
// Gmail shows over IMAP. All Mail is the empty label.
func (g *Gmail) labelID(folder string) (string, error) {
	if strings.EqualFold(folder, labelInbox) {
		return labelInbox, nil
	}
	for _, prefix := range gmailFolderPrefixes {
		if name, ok := strings.CutPrefix(folder, prefix); ok {
			if label, known := gmailFolders[name]; known {
				return label, nil
			}
		}
	}
	if g.labels == nil {
		list, err := g.users.Labels.List(gmailUser).Context(g.ctx).Do()
		if err != nil {
			return "", fmt.Errorf("list labels: %w", gmailError(err))
		}
		g.labels = make(map[string]string, len(list.Labels))
		for _, label := range list.Labels {
			g.labels[label.Name] = label.Id
		}
	}
	if id, ok := g.labels[folder]; ok {
		return id, nil
	}
	return "", errNoLabel
}

// folderLabel is labelID, creating the label when there is none yet. Gmail's
// own folders are never created.
func (g *Gmail) folderLabel(folder string) (string, error) {
	id, err := g.labelID(folder)
	if !errors.Is(err, errNoLabel) {
		return id, err
	}
	for _, prefix := range gmailFolderPrefixes {
		if strings.HasPrefix(folder, prefix) {
			return "", fmt.Errorf("create folder %q: %w", folder, errReservedLabel)
		}
	}
	label, err := g.users.Labels.Create(gmailUser, &gmail.Label{Name: folder}).Context(g.ctx).Do()
	if gmailStatus(err) == http.StatusConflict {
		// Another run created the label after this one listed the labels.
		g.labels = nil
		if id, listErr := g.labelID(folder); listErr == nil {
			return id, nil
		}
	}
	if err != nil {
		return "", fmt.Errorf("create folder %q: %w", folder, gmailError(err))
	}
	g.labels[folder] = label.Id
	return label.Id, nil
}

// present reports whether an email carrying labels is still where ref found
// it: under ref's label, and in Spam or Trash only when found there.
func (ref gmailRef) present(labels []string) bool {
	if ref.label != "" && !slices.Contains(labels, ref.label) {
		return false
	}
	for _, hidden := range []string{labelSpam, labelTrash} {
		if ref.label != hidden && slices.Contains(labels, hidden) {
			return false
		}
	}
	return true
}

// tokenSource hands each request the account's current access token.
type tokenSource struct {
	ctx   context.Context
	token func(context.Context) (*oauth2.Token, error)
}

func (s tokenSource) Token() (*oauth2.Token, error) {
	return s.token(s.ctx)
}

// gmailError states a missing Gmail scope plainly and keeps Google's own
// message for other refusals, such as a Gmail API that is not enabled.
func gmailError(err error) error {
	apiErr, ok := errors.AsType[*googleapi.Error](err)
	if !ok {
		return err
	}
	if apiErr.Code == http.StatusForbidden {
		for _, item := range apiErr.Errors {
			if item.Reason == "insufficientPermissions" {
				return ErrGmailScope
			}
		}
	}
	if apiErr.Message == "" {
		return fmt.Errorf("gmail: %w", apiErr)
	}
	return fmt.Errorf("gmail: %s", apiErr.Message)
}

// gmailStatus returns the HTTP status of an API refusal, or 0 for any other
// outcome.
func gmailStatus(err error) int {
	if apiErr, ok := errors.AsType[*googleapi.Error](err); ok {
		return apiErr.Code
	}
	return 0
}
