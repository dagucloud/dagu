// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"crypto/tls"
	"errors"
	"io"

	"golang.org/x/crypto/cryptobyte"
)

// The parts of TLS records and handshake messages the guard reads, as RFC
// 8446 lays them out.
const (
	recordHeaderLen            = 5
	recordTypeAlert            = 21
	recordTypeHandshake        = 22
	handshakeHeaderLen         = 4
	handshakeTypeClientHello   = 1
	handshakeTypeServerHello   = 2
	extensionSupportedVersions = 43
	// helloRandomLen is the length of a hello's random value.
	helloRandomLen = 32
	// maxRecordLen is the longest record payload TLS allows: 2^14 bytes,
	// which TLS 1.2 lets encryption grow by 2048.
	maxRecordLen = 1<<14 + 2048
	// maxHelloBytes bounds the records read for one hello. Chrome's, with
	// its post-quantum key share, takes about 2 KiB.
	maxHelloBytes = 64 << 10
)

// errNotHello reports bytes that are not the hello the guard waits for.
var errNotHello = errors.New("not the expected TLS hello")

// hello is a TLS hello as read off a connection.
type hello struct {
	// records are the bytes read, whole records, to pass on unchanged.
	records []byte
	// message is the hello's handshake message, without its header.
	message []byte
}

// readHello reads whole records from r until they hold a complete handshake
// message of kind, which must be the first one; alerts before it are kept
// in the records. What was read is returned on failure too, so that it can
// be replayed.
func readHello(r io.Reader, kind uint8) (hello, error) {
	var h hello
	var handshake []byte
	for {
		header, err := h.read(r, recordHeaderLen)
		if err != nil {
			return h, err
		}
		length := int(header[3])<<8 | int(header[4])
		if length > maxRecordLen || len(h.records)+length > maxHelloBytes {
			return h, errNotHello
		}
		recordType := header[0]
		payload, err := h.read(r, length)
		if err != nil {
			return h, err
		}
		switch recordType {
		case recordTypeAlert:
			continue
		case recordTypeHandshake:
			handshake = append(handshake, payload...)
		default:
			return h, errNotHello
		}
		if len(handshake) < handshakeHeaderLen {
			continue
		}
		if handshake[0] != kind {
			return h, errNotHello
		}
		size := handshakeHeaderLen + (int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3]))
		if size > maxHelloBytes {
			return h, errNotHello
		}
		if len(handshake) >= size {
			h.message = handshake[handshakeHeaderLen:size]
			return h, nil
		}
	}
}

// read appends n bytes from r to the records and returns them.
func (h *hello) read(r io.Reader, n int) ([]byte, error) {
	start := len(h.records)
	h.records = append(h.records, make([]byte, n)...)
	read, err := io.ReadFull(r, h.records[start:])
	h.records = h.records[:start+read]
	return h.records[start:], err
}

// selectsTLS13 reports whether a ServerHello or HelloRetryRequest message
// selects TLS 1.3, which only its supported_versions extension can do.
func selectsTLS13(message []byte) bool {
	s := cryptobyte.String(message)
	var sessionID, extensions cryptobyte.String
	// legacy_version, random, legacy_session_id_echo, cipher_suite,
	// legacy_compression_method, extensions.
	if !s.Skip(2+helloRandomLen) || !s.ReadUint8LengthPrefixed(&sessionID) || !s.Skip(2+1) ||
		!s.ReadUint16LengthPrefixed(&extensions) || !s.Empty() {
		return false
	}
	for !extensions.Empty() {
		var kind uint16
		var data cryptobyte.String
		if !extensions.ReadUint16(&kind) || !extensions.ReadUint16LengthPrefixed(&data) {
			return false
		}
		if kind == extensionSupportedVersions {
			var version uint16
			return data.ReadUint16(&version) && data.Empty() && version == tls.VersionTLS13
		}
	}
	return false
}
