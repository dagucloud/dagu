// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/gmail/v1"
)

const (
	// gmailPageSize is the most IDs one list call returns.
	gmailPageSize = 500
	// gmailParallelFetches bounds concurrent requests for email headers.
	gmailParallelFetches = 10
)

// Search returns the oldest matching emails, up to opts.Limit. Reading leaves
// every email's labels as they were.
func (g *Gmail) Search(opts SearchOptions) ([]Message, error) {
	folder := opts.Folder
	if folder == "" {
		folder = labelInbox
	}
	label, err := g.labelID(folder)
	if err != nil {
		return nil, fmt.Errorf("open folder %q: %w", folder, err)
	}

	now := time.Now()
	ids, err := g.listMessages(folder, label, opts, now)
	if err != nil {
		return nil, err
	}

	saver := &attachmentSaver{dir: opts.AttachmentsDir}
	messages := []Message{}
	for start := 0; start < len(ids) && len(messages) < opts.Limit; start += fetchBatch {
		batch := ids[start:min(start+fetchBatch, len(ids))]
		if opts.From != "" || opts.Subject != "" {
			if batch, err = g.matchHeaders(batch, opts.From, opts.Subject); err != nil {
				return nil, err
			}
		}
		for _, id := range batch {
			if len(messages) == opts.Limit {
				break
			}
			found, err := g.users.Messages.Get(gmailUser, id).Format("raw").Context(g.ctx).Do()
			if gmailNotFound(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("fetch email body: %w", gmailError(err))
			}
			if opts.Within > 0 && time.UnixMilli(found.InternalDate).Before(now.Add(-opts.Within)) {
				continue
			}
			msg, err := gmailMessage(found, folder, label, saver)
			if err != nil {
				return nil, err
			}
			if opts.HasAttachments && len(msg.Attachments) == 0 {
				continue
			}
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// listMessages returns the IDs of the emails under label that pass the
// filters Gmail can apply exactly, oldest first.
func (g *Gmail) listMessages(folder, label string, opts SearchOptions, now time.Time) ([]string, error) {
	call := g.users.Messages.List(gmailUser).MaxResults(gmailPageSize)
	var labels []string
	if label != "" {
		labels = append(labels, label)
	}
	if opts.Unread {
		labels = append(labels, labelUnread)
	}
	if len(labels) > 0 {
		call.LabelIds(labels...)
	}
	var query []string
	if opts.Within > 0 {
		// Whole seconds; the exact cutoff applies after fetching.
		query = append(query, "after:"+strconv.FormatInt(now.Add(-opts.Within).Unix(), 10))
	}
	if opts.HasAttachments {
		// Narrows the list; the attachments found when parsing decide.
		query = append(query, "has:attachment")
	}
	if len(query) > 0 {
		call.Q(strings.Join(query, " "))
	}
	if label == labelSpam || label == labelTrash {
		call.IncludeSpamTrash(true)
	}

	var ids []string
	err := call.Pages(g.ctx, func(page *gmail.ListMessagesResponse) error {
		for _, found := range page.Messages {
			ids = append(ids, found.Id)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search folder %q: %w", folder, gmailError(err))
	}
	// Gmail lists the newest first.
	slices.Reverse(ids)
	return ids, nil
}

// matchHeaders keeps the emails whose From and Subject contain from and
// subject, ignoring case. Gmail's own search matches whole words, so it cannot
// match part of a word as an IMAP server does.
func (g *Gmail) matchHeaders(ids []string, from, subject string) ([]string, error) {
	found := make([]*gmail.Message, len(ids))
	group, ctx := errgroup.WithContext(g.ctx)
	group.SetLimit(gmailParallelFetches)
	for i, id := range ids {
		group.Go(func() error {
			msg, err := g.users.Messages.Get(gmailUser, id).
				Format("metadata").
				MetadataHeaders("From", "Subject").
				Context(ctx).
				Do()
			if gmailNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("fetch emails: %w", gmailError(err))
			}
			found[i] = msg
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	var matched []string
	for _, msg := range found {
		if msg == nil {
			continue
		}
		header := metadataHeader(msg)
		if containsFold(header, "From", from) && containsFold(header, "Subject", subject) {
			matched = append(matched, msg.Id)
		}
	}
	return matched, nil
}

// ReplyInfo reads what a reply to the email with id needs, including its
// Gmail conversation.
func (g *Gmail) ReplyInfo(id string) (*ReplyInfo, error) {
	ref, err := parseGmailID(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", err, id)
	}
	found, err := g.users.Messages.Get(gmailUser, ref.message).
		Format("metadata").
		MetadataHeaders("Message-ID", "References", "Subject", "Reply-To", "From").
		Context(g.ctx).
		Do()
	if gmailNotFound(err) {
		return nil, ErrEmailGone
	}
	if err != nil {
		return nil, fmt.Errorf("fetch email: %w", gmailError(err))
	}

	header := metadataHeader(found)
	info := &ReplyInfo{ThreadID: found.ThreadId}
	info.MessageID, _ = header.MessageID()
	info.References, _ = header.MsgIDList("References")
	info.Subject, _ = header.Subject()
	for _, key := range []string{"Reply-To", "From"} {
		if list, err := header.AddressList(key); err == nil && len(list) > 0 && list[0].Address != "" {
			info.ReplyTo = list[0].Address
			break
		}
	}
	return info, nil
}

// gmailMessage turns an email fetched in raw form into a Message found in
// folder under label.
func gmailMessage(found *gmail.Message, folder, label string, saver *attachmentSaver) (Message, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(found.Raw, "="))
	if err != nil {
		return Message{}, fmt.Errorf("decode email %s: %w", found.Id, err)
	}
	msg := Message{
		ID:          gmailRef{message: found.Id, label: label}.id(),
		Folder:      folder,
		To:          []string{},
		Cc:          []string{},
		Unread:      slices.Contains(found.LabelIds, labelUnread),
		Flagged:     slices.Contains(found.LabelIds, labelStarred),
		Attachments: []Attachment{},
	}
	readHeaders(raw, &msg, time.UnixMilli(found.InternalDate))
	if err := parseBody(raw, &msg, saver); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// metadataHeader holds the headers the API returned with an email.
func metadataHeader(found *gmail.Message) gomail.Header {
	var fields textproto.Header
	if found.Payload != nil {
		for _, field := range found.Payload.Headers {
			fields.Add(field.Name, field.Value)
		}
	}
	return gomail.Header{Header: message.Header{Header: fields}}
}

// containsFold reports whether the decoded header field key contains term,
// ignoring case. An empty term matches every email.
func containsFold(header gomail.Header, key, term string) bool {
	if term == "" {
		return true
	}
	value, err := header.Text(key)
	if err != nil {
		value = header.Get(key)
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(term))
}
