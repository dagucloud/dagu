// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// gmailRequestTimeout bounds each Gmail API request, as the idle bound does an
// IMAP connection.
const gmailRequestTimeout = 2 * time.Minute

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

var errNoLabel = errors.New("no such label")

// Gmail is a mailbox reached through the Gmail API.
type Gmail struct {
	ctx   context.Context
	users *gmail.UsersService
	// labels maps label names to IDs once listed.
	labels map[string]string
}

// DialGmail signs in to the account's mailbox through the Gmail API with the
// account's OAuth token. Canceling ctx ends any request.
func DialGmail(ctx context.Context, account Account) (*Gmail, error) {
	return dialGmail(ctx, account)
}

func dialGmail(ctx context.Context, account Account, options ...option.ClientOption) (*Gmail, error) {
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
	client := &http.Client{
		Timeout: gmailRequestTimeout,
		Transport: &oauth2.Transport{
			Source: tokenSource{ctx: ctx, token: account.Token},
			Base:   http.DefaultTransport,
		},
	}
	service, err := gmail.NewService(ctx, append([]option.ClientOption{option.WithHTTPClient(client)}, options...)...)
	if err != nil {
		return nil, err
	}
	return &Gmail{ctx: ctx, users: service.Users}, nil
}

// Close ends nothing: the Gmail API keeps no session.
func (*Gmail) Close() error {
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

// folderLabel is labelID, creating the label when there is none yet.
func (g *Gmail) folderLabel(folder string) (string, error) {
	id, err := g.labelID(folder)
	if !errors.Is(err, errNoLabel) {
		return id, err
	}
	label, err := g.users.Labels.Create(gmailUser, &gmail.Label{Name: folder}).Context(g.ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create folder %q: %w", folder, gmailError(err))
	}
	g.labels[folder] = label.Id
	return label.Id, nil
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

// gmailNotFound reports whether the API answered that an email does not exist.
func gmailNotFound(err error) bool {
	apiErr, ok := errors.AsType[*googleapi.Error](err)
	return ok && apiErr.Code == http.StatusNotFound
}
