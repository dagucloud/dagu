// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"bytes"
	"io"
)

// secretMask reads a body and replaces each copy of a secret with as many
// '*'. The body keeps its length, so its content-length stays true.
type secretMask struct {
	src     io.Reader
	secrets [][]byte
	// hold is the length of the longest secret minus one: a tail that short
	// can still become a copy with the next bytes, so it waits for them.
	hold  int
	chunk []byte
	// pending holds the bytes read and masked, not returned yet.
	pending []byte
	// err is the error of src, returned once pending is empty.
	err error
}

// maskSecrets returns a reader of src that masks each non-empty secret.
func maskSecrets(src io.Reader, secrets []string) io.Reader {
	m := &secretMask{src: src}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		m.secrets = append(m.secrets, []byte(s))
		m.hold = max(m.hold, len(s)-1)
	}
	if len(m.secrets) == 0 {
		return src
	}
	m.chunk = make([]byte, ChunkBytes)
	return m
}

func (m *secretMask) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		ready := len(m.pending)
		if m.err == nil {
			ready -= m.hold
		}
		if ready > 0 {
			n := copy(p, m.pending[:ready])
			m.pending = m.pending[n:]
			return n, nil
		}
		if m.err != nil {
			return 0, m.err
		}
		n, err := m.src.Read(m.chunk)
		m.pending = append(m.pending, m.chunk[:n]...)
		for _, secret := range m.secrets {
			maskAll(m.pending, secret)
		}
		m.err = err
	}
}

// maskAll replaces each copy of secret in buf with as many '*'.
func maskAll(buf, secret []byte) {
	for start := 0; ; {
		i := bytes.Index(buf[start:], secret)
		if i < 0 {
			return
		}
		start += i
		for j := range secret {
			buf[start+j] = '*'
		}
		start += len(secret)
	}
}
