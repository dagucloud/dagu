// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// idVersion prefixes every email ID so the format can change later.
const idVersion = "v1"

// emailRef locates an email: a UID is valid only within its folder while the
// folder's UIDVALIDITY is unchanged.
type emailRef struct {
	folder      string
	uidValidity uint32
	uid         imap.UID
}

var errMalformedID = errors.New("malformed email ID")

func (r emailRef) id() string {
	raw := strings.Join([]string{
		idVersion,
		strconv.FormatUint(uint64(r.uidValidity), 10),
		strconv.FormatUint(uint64(r.uid), 10),
		r.folder,
	}, "\x00")
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func parseID(id string) (emailRef, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(id))
	if err != nil {
		return emailRef{}, errMalformedID
	}
	parts := strings.SplitN(string(raw), "\x00", 4)
	if len(parts) != 4 || parts[0] != idVersion || parts[3] == "" {
		return emailRef{}, errMalformedID
	}
	uidValidity, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return emailRef{}, errMalformedID
	}
	uid, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil || uid == 0 {
		return emailRef{}, errMalformedID
	}
	return emailRef{folder: parts[3], uidValidity: uint32(uidValidity), uid: imap.UID(uid)}, nil
}

// gmailIDVersion prefixes the IDs of email found through the Gmail API.
const gmailIDVersion = "g1"

// gmailRef locates an email in a Gmail mailbox: its message ID, which never
// changes, and the label it was found under, which a move takes away. The
// empty label stands for All Mail.
type gmailRef struct {
	message string
	label   string
}

func (r gmailRef) id() string {
	raw := strings.Join([]string{gmailIDVersion, r.message, r.label}, "\x00")
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func parseGmailID(id string) (gmailRef, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(id))
	if err != nil {
		return gmailRef{}, errMalformedID
	}
	parts := strings.SplitN(string(raw), "\x00", 3)
	if len(parts) != 3 || parts[0] != gmailIDVersion || parts[1] == "" {
		return gmailRef{}, errMalformedID
	}
	return gmailRef{message: parts[1], label: parts[2]}, nil
}

// ValidID reports whether id is a well-formed email ID of an IMAP or a Gmail
// API mailbox. Each mailbox refuses the other kind as malformed.
func ValidID(id string) bool {
	if _, err := parseID(id); err == nil {
		return true
	}
	_, err := parseGmailID(id)
	return err == nil
}
