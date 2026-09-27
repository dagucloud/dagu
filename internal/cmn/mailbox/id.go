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

// ValidID reports whether id is a well-formed email ID.
func ValidID(id string) bool {
	_, err := parseID(id)
	return err == nil
}
