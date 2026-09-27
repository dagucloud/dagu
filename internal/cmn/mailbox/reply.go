// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
)

// ErrEmailGone means an email is no longer in the folder its ID names.
var ErrEmailGone = errors.New("the email is no longer in its folder")

// ReplyInfo is what a reply needs from the email it answers. Message-IDs are
// without angle brackets.
type ReplyInfo struct {
	MessageID  string
	References []string
	Subject    string
	// ReplyTo is the Reply-To address, or the sender when there is none.
	ReplyTo string
}

// ReplyInfo reads what a reply to the email with id needs. The folder is
// opened read-only.
func (c *Client) ReplyInfo(id string) (*ReplyInfo, error) {
	ref, err := parseID(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", err, id)
	}
	selected, err := c.imap.Select(ref.folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		if folderMissing(err) {
			return nil, ErrEmailGone
		}
		return nil, fmt.Errorf("open folder %q: %w", ref.folder, err)
	}
	if selected.UIDValidity != ref.uidValidity {
		return nil, ErrEmailGone
	}

	section := &imap.FetchItemBodySection{
		Specifier:    imap.PartSpecifierHeader,
		HeaderFields: []string{"References"},
		Peek:         true,
	}
	buffers, err := c.imap.Fetch(imap.UIDSetNum(ref.uid), &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch email: %w", err)
	}
	for _, buf := range buffers {
		if buf.UID != ref.uid || buf.Envelope == nil {
			continue
		}
		env := buf.Envelope
		return &ReplyInfo{
			MessageID:  env.MessageID,
			References: parseReferences(buf.FindBodySection(section)),
			Subject:    env.Subject,
			ReplyTo:    replyAddress(env),
		}, nil
	}
	return nil, ErrEmailGone
}

func replyAddress(env *imap.Envelope) string {
	for _, list := range [][]imap.Address{env.ReplyTo, env.From} {
		for _, address := range list {
			if addr := address.Addr(); addr != "" {
				return addr
			}
		}
	}
	return ""
}

// parseReferences reads the Message-IDs of a References header block.
func parseReferences(raw []byte) []string {
	header, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil
	}
	mailHeader := gomail.Header{Header: message.Header{Header: header}}
	ids, err := mailHeader.MsgIDList("References")
	if err != nil {
		return nil
	}
	return ids
}
