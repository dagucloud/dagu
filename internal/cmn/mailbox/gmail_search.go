// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"encoding/base64"
	"fmt"
	"net/http"
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
	// GmailScanLimit is the most emails one search of a Gmail API mailbox
	// examines: the newest that Gmail's own search lists for the folder,
	// unread, within, and has-attachments filters. Gmail lists the newest first
	// and cannot start from the oldest, so a larger folder would cost one
	// request per page.
	GmailScanLimit = 2000
	// gmailParallelFetches bounds concurrent requests for email headers, well
	// under Gmail's per-user rate of 50 message reads a second.
	gmailParallelFetches = 5
)

// Search returns the oldest matching emails, up to opts.Limit, and whether
// the folder held more emails than the search examined (GmailScanLimit).
// Reading leaves every email's labels as they were.
func (g *Gmail) Search(opts SearchOptions) ([]Message, bool, error) {
	folder := opts.Folder
	if folder == "" {
		folder = labelInbox
	}
	label, err := g.labelID(folder)
	if err != nil {
		return nil, false, fmt.Errorf("open folder %q: %w", folder, err)
	}

	now := time.Now()
	ids, partial, err := g.listMessages(folder, label, opts, now)
	if err != nil {
		return nil, false, err
	}

	saver := &attachmentSaver{dir: opts.AttachmentsDir}
	messages := []Message{}
	for start := 0; start < len(ids) && len(messages) < opts.Limit; start += fetchBatch {
		batch := ids[start:min(start+fetchBatch, len(ids))]
		if opts.From != "" || opts.Subject != "" {
			if batch, err = g.matchHeaders(batch, opts.From, opts.Subject); err != nil {
				return nil, false, err
			}
		}
		for _, id := range batch {
			if len(messages) == opts.Limit {
				break
			}
			found, err := g.users.Messages.Get(gmailUser, id).Format("raw").Context(g.ctx).Do()
			if gmailStatus(err) == http.StatusNotFound {
				continue
			}
			if err != nil {
				return nil, false, fmt.Errorf("fetch email body: %w", gmailError(err))
			}
			if opts.Within > 0 && time.UnixMilli(found.InternalDate).Before(now.Add(-opts.Within)) {
				continue
			}
			msg, err := gmailMessage(found, folder, label, saver)
			if err != nil {
				return nil, false, err
			}
			if opts.HasAttachments && len(msg.Attachments) == 0 {
				continue
			}
			messages = append(messages, msg)
		}
	}
	return messages, partial, nil
}

// listMessages returns the IDs of the newest emails under label, up to
// GmailScanLimit, that pass the filters Gmail applies itself, oldest first. It
// also reports whether more emails passed them.
func (g *Gmail) listMessages(folder, label string, opts SearchOptions, now time.Time) ([]string, bool, error) {
	call := g.users.Messages.List(gmailUser).MaxResults(min(gmailPageSize, GmailScanLimit)).Context(g.ctx)
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
		// Gmail compares whole seconds, so the query starts a second early to
		// keep the cutoff's own second; the exact cutoff applies after fetching.
		query = append(query, "after:"+strconv.FormatInt(now.Add(-opts.Within).Unix()-1, 10))
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
	partial := false
	for {
		page, err := call.Do()
		if err != nil {
			return nil, false, fmt.Errorf("search folder %q: %w", folder, gmailError(err))
		}
		for _, found := range page.Messages {
			ids = append(ids, found.Id)
		}
		if page.NextPageToken == "" {
			break
		}
		if len(ids) >= GmailScanLimit {
			partial = true
			break
		}
		call.PageToken(page.NextPageToken)
	}
	if len(ids) > GmailScanLimit {
		ids, partial = ids[:GmailScanLimit], true
	}
	// Gmail lists the newest first.
	slices.Reverse(ids)
	return ids, partial, nil
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
			if gmailStatus(err) == http.StatusNotFound {
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
// Gmail conversation. The email must still be where it was found.
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
	if gmailStatus(err) == http.StatusNotFound {
		return nil, ErrEmailGone
	}
	if err != nil {
		return nil, fmt.Errorf("fetch email: %w", gmailError(err))
	}
	if !ref.present(found.LabelIds) {
		return nil, ErrEmailGone
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
