// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // decodes non-UTF-8 charsets
	gomail "github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// TextLimit is the most characters of body text a message carries.
const TextLimit = 10000

const (
	fetchBatch    = 50
	maxPartBytes  = 25 << 20
	dispositionAt = "attachment"
)

// SearchOptions selects email in one folder. Empty filters match everything.
type SearchOptions struct {
	Folder         string
	Unread         bool
	From           string
	Subject        string
	Within         time.Duration
	HasAttachments bool
	Limit          int
	// AttachmentsDir receives attachment files when not empty.
	AttachmentsDir string
}

// Message is a found email. Every field sits at the top level so a loop can
// reach it.
type Message struct {
	ID string `json:"id"`
	// MessageID is the Message-ID header without angle brackets. Unlike ID, it
	// stays the same when the email moves.
	MessageID   string       `json:"message_id"`
	Folder      string       `json:"folder"`
	FromName    string       `json:"from_name"`
	FromAddress string       `json:"from_address"`
	To          []string     `json:"to"`
	Cc          []string     `json:"cc"`
	Subject     string       `json:"subject"`
	Date        string       `json:"date"`
	Unread      bool         `json:"unread"`
	Flagged     bool         `json:"flagged"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments"`
}

// Attachment describes an attachment; Path is set only when it was saved.
type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Path        string `json:"path,omitempty"`
}

// Search returns the oldest matching emails, up to opts.Limit. It opens the
// folder read-only, so no email's flags change. It examines every email in the
// folder, so its result is never partial.
func (c *Client) Search(opts SearchOptions) ([]Message, bool, error) {
	folder := opts.Folder
	if folder == "" {
		folder = "INBOX"
	}
	selected, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, false, fmt.Errorf("open folder %q: %w", folder, err)
	}

	now := time.Now()
	criteria := &imap.SearchCriteria{}
	if opts.Unread {
		criteria.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	if opts.From != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: opts.From})
	}
	if opts.Subject != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: opts.Subject})
	}
	if opts.Within > 0 {
		// SINCE compares dates only; the exact cutoff applies after fetching.
		criteria.Since = now.Add(-opts.Within).Add(-24 * time.Hour)
	}
	found, err := c.imap.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, false, fmt.Errorf("search folder %q: %w", folder, err)
	}
	uids := found.AllUIDs()
	slices.Sort(uids)

	var picked []*fetchedHeader
	for start := 0; start < len(uids) && len(picked) < opts.Limit; start += fetchBatch {
		batch := uids[start:min(start+fetchBatch, len(uids))]
		headers, err := c.fetchHeaders(batch)
		if err != nil {
			return nil, false, err
		}
		for _, h := range headers {
			if opts.Within > 0 && h.internalDate.Before(now.Add(-opts.Within)) {
				continue
			}
			if opts.HasAttachments && !h.hasAttachment {
				continue
			}
			picked = append(picked, h)
			if len(picked) == opts.Limit {
				break
			}
		}
	}

	saver := &attachmentSaver{dir: opts.AttachmentsDir}
	messages := make([]Message, 0, len(picked))
	for _, h := range picked {
		raw, err := c.fetchBody(h.uid)
		if err != nil {
			return nil, false, err
		}
		msg := h.message(emailRef{folder: folder, uidValidity: selected.UIDValidity, uid: h.uid})
		if err := parseBody(raw, &msg, saver); err != nil {
			return nil, false, err
		}
		messages = append(messages, msg)
	}
	return messages, false, nil
}

type fetchedHeader struct {
	uid           imap.UID
	flags         []imap.Flag
	envelope      *imap.Envelope
	internalDate  time.Time
	hasAttachment bool
}

func (c *Client) fetchHeaders(uids []imap.UID) ([]*fetchedHeader, error) {
	buffers, err := c.imap.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:           true,
		Flags:         true,
		Envelope:      true,
		InternalDate:  true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch emails: %w", err)
	}
	headers := make([]*fetchedHeader, 0, len(buffers))
	for _, buf := range buffers {
		headers = append(headers, &fetchedHeader{
			uid:           buf.UID,
			flags:         buf.Flags,
			envelope:      buf.Envelope,
			internalDate:  buf.InternalDate,
			hasAttachment: hasAttachment(buf.BodyStructure),
		})
	}
	slices.SortFunc(headers, func(a, b *fetchedHeader) int { return cmp.Compare(a.uid, b.uid) })
	return headers, nil
}

func (c *Client) fetchBody(uid imap.UID) ([]byte, error) {
	section := &imap.FetchItemBodySection{Peek: true}
	buffers, err := c.imap.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch email body: %w", err)
	}
	for _, buf := range buffers {
		if buf.UID == uid {
			return buf.FindBodySection(section), nil
		}
	}
	return nil, fmt.Errorf("fetch email body: email %d is gone", uid)
}

func hasAttachment(structure imap.BodyStructure) bool {
	if structure == nil {
		return false
	}
	found := false
	structure.Walk(func(_ []int, part imap.BodyStructure) bool {
		if disposition := part.Disposition(); disposition != nil && strings.EqualFold(disposition.Value, dispositionAt) {
			found = true
		}
		return !found
	})
	return found
}

func (h *fetchedHeader) message(ref emailRef) Message {
	msg := Message{
		ID:          ref.id(),
		Folder:      ref.folder,
		To:          []string{},
		Cc:          []string{},
		Unread:      !slices.Contains(h.flags, imap.FlagSeen),
		Flagged:     slices.Contains(h.flags, imap.FlagFlagged),
		Attachments: []Attachment{},
	}
	date := h.internalDate
	if env := h.envelope; env != nil {
		msg.MessageID = env.MessageID
		msg.Subject = env.Subject
		if len(env.From) > 0 {
			msg.FromName = env.From[0].Name
			msg.FromAddress = env.From[0].Addr()
		}
		msg.To = addresses(env.To)
		msg.Cc = addresses(env.Cc)
		if !env.Date.IsZero() {
			date = env.Date
		}
	}
	if !date.IsZero() {
		msg.Date = date.UTC().Format(time.RFC3339)
	}
	return msg
}

func addresses(list []imap.Address) []string {
	out := make([]string, 0, len(list))
	for _, address := range list {
		if addr := address.Addr(); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// readHeaders fills msg's header fields from the raw RFC 5322 message. The
// time the mailbox received it stands in for a missing or unreadable Date.
func readHeaders(raw []byte, msg *Message, received time.Time) {
	date := received
	if fields, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw))); err == nil {
		header := gomail.Header{Header: message.Header{Header: fields}}
		msg.MessageID, _ = header.MessageID()
		msg.Subject, _ = header.Subject()
		if from, err := header.AddressList("From"); err == nil && len(from) > 0 {
			msg.FromName, msg.FromAddress = from[0].Name, from[0].Address
		}
		msg.To = headerAddresses(header, "To")
		msg.Cc = headerAddresses(header, "Cc")
		if sent, err := header.Date(); err == nil && !sent.IsZero() {
			date = sent
		}
	}
	if !date.IsZero() {
		msg.Date = date.UTC().Format(time.RFC3339)
	}
}

func headerAddresses(header gomail.Header, key string) []string {
	list, _ := header.AddressList(key)
	out := make([]string, 0, len(list))
	for _, address := range list {
		if address.Address != "" {
			out = append(out, address.Address)
		}
	}
	return out
}

// parseBody fills msg's text and attachments from the raw RFC 5322 message.
func parseBody(raw []byte, msg *Message, saver *attachmentSaver) error {
	reader, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		// Unparsable headers: the text is whatever follows the first blank line.
		msg.Text = limitText(strings.ReplaceAll(string(bodyAfterHeaders(raw)), "\r\n", "\n"))
		return nil
	}
	var plain, htmlText string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if message.IsUnknownCharset(err) {
				continue
			}
			break
		}
		switch header := part.Header.(type) {
		case *gomail.InlineHeader:
			contentType, _, _ := header.ContentType()
			switch {
			case contentType == "text/plain" && plain == "":
				plain = readText(part.Body)
			case contentType == "text/html" && htmlText == "":
				htmlText = htmlToText(io.LimitReader(part.Body, maxPartBytes))
			}
		case *gomail.AttachmentHeader:
			attachment, err := saver.save(header, part.Body)
			if err != nil {
				return err
			}
			msg.Attachments = append(msg.Attachments, attachment)
		}
	}
	if plain == "" {
		plain = htmlText
	}
	msg.Text = limitText(plain)
	return nil
}

// bodyAfterHeaders returns what follows the first blank line, in either line
// ending, or raw when there is none.
func bodyAfterHeaders(raw []byte) []byte {
	end := -1
	for _, separator := range [][]byte{[]byte("\r\n\r\n"), []byte("\n\n")} {
		if i := bytes.Index(raw, separator); i >= 0 && (end < 0 || i+len(separator) <= end) {
			end = i + len(separator)
		}
	}
	if end < 0 {
		return raw
	}
	return raw[end:]
}

func readText(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, maxPartBytes))
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

func limitText(text string) string {
	return truncateRunes(strings.TrimSpace(text), TextLimit)
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// attachmentSaver numbers files so names stay unique within one search.
type attachmentSaver struct {
	dir   string
	count int
}

func (s *attachmentSaver) save(header *gomail.AttachmentHeader, body io.Reader) (Attachment, error) {
	name, _ := header.Filename()
	contentType, _, _ := header.ContentType()
	attachment := Attachment{Name: name, ContentType: contentType}
	if s.dir == "" {
		size, err := io.Copy(io.Discard, io.LimitReader(body, maxPartBytes))
		attachment.Size = size
		return attachment, err
	}

	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return attachment, fmt.Errorf("create attachments directory: %w", err)
	}
	// Loop iterations share the step's directory, so numbering skips names an
	// earlier search already used.
	var path string
	var file *os.File
	for {
		s.count++
		path = filepath.Join(s.dir, fmt.Sprintf("%02d-%s", s.count, safeFilename(name)))
		var err error
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is built from a sanitized name
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return attachment, fmt.Errorf("save attachment: %w", err)
		}
		break
	}
	size, copyErr := io.Copy(file, io.LimitReader(body, maxPartBytes))
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return attachment, fmt.Errorf("save attachment: %w", err)
	}
	attachment.Size = size
	attachment.Path = path
	return attachment, nil
}

// safeFilename keeps an attachment's extension while replacing unsafe characters.
func safeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		return "attachment"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem = "attachment"
	}
	safe := fileutil.SafeName(stem)
	if ext != "" {
		safe += "." + fileutil.SafeName(strings.TrimPrefix(ext, "."))
	}
	return safe
}
