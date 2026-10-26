// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
)

// Keys of a paused request's body in a DevTools message. The browser sends
// the whole body, however large, both as text and as base64 entries.
const (
	postDataKey        = "postData"
	postDataEntriesKey = "postDataEntries"
	// postDataSizeKey is added after a cut postData with the size of the
	// whole body.
	postDataSizeKey = "postDataSize"
)

// maxKeyBytes bounds the strings remembered as the key of the value after
// them; longer ones cannot be the keys cutMessage looks for.
const maxKeyBytes = 64

var errUnterminated = errors.New("the browser sent a message that ends inside a string")

// cutMessage reads one DevTools message from r and returns it with every
// postData value cut to limit bytes, followed by a postDataSize key with
// the size of the whole body, and every postDataEntries value left empty.
// Memory stays bounded by the rest of the message, however large a body
// is.
func cutMessage(r io.Reader, limit int) ([]byte, error) {
	c := &cutter{in: bufio.NewReaderSize(r, 64<<10), limit: limit}
	if err := c.run(); err != nil {
		return nil, err
	}
	return c.out.Bytes(), nil
}

type cutter struct {
	in    *bufio.Reader
	out   bytes.Buffer
	limit int
}

func (c *cutter) run() error {
	// key is the last string read, while only white space followed it.
	var key []byte
	for {
		b, err := c.in.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch b {
		case '"':
			c.out.WriteByte(b)
			if key, err = c.copyString(); err != nil {
				return err
			}
		case ':':
			c.out.WriteByte(b)
			if err := c.value(string(key)); err != nil {
				return err
			}
			key = nil
		default:
			c.out.WriteByte(b)
			if !space(b) {
				key = nil
			}
		}
	}
}

// value handles the value of key, which follows the colon just read.
func (c *cutter) value(key string) error {
	if key != postDataKey && key != postDataEntriesKey {
		return nil
	}
	for {
		next, err := c.in.Peek(1)
		if err != nil {
			return nil
		}
		if !space(next[0]) {
			break
		}
		_, _ = c.in.ReadByte()
	}
	next, _ := c.in.Peek(1)
	switch {
	case key == postDataKey && next[0] == '"':
		_, _ = c.in.ReadByte()
		return c.cutString()
	case key == postDataEntriesKey && next[0] == '[':
		_, _ = c.in.ReadByte()
		c.out.WriteString("[]")
		return c.skipArray()
	}
	return nil
}

// copyString copies the rest of a string whose opening quote was read, and
// returns its raw content when it is short enough to be a key.
func (c *cutter) copyString() ([]byte, error) {
	start := c.out.Len()
	for {
		b, err := c.in.ReadByte()
		if err != nil {
			return nil, errUnterminated
		}
		if b == '"' {
			content := c.out.Bytes()[start:]
			c.out.WriteByte(b)
			if len(content) > maxKeyBytes {
				return nil, nil
			}
			return bytes.Clone(content), nil
		}
		c.out.WriteByte(b)
		if b == '\\' {
			escaped, err := c.in.ReadByte()
			if err != nil {
				return nil, errUnterminated
			}
			c.out.WriteByte(escaped)
		}
	}
}

// cutString copies at most limit bytes of a string whose opening quote was
// read, never splitting an escape, skips the rest, and adds the size of
// the whole string, counting each escape as one byte.
func (c *cutter) cutString() error {
	c.out.WriteByte('"')
	kept, size := 0, int64(0)
	for kept < c.limit {
		b, err := c.in.ReadByte()
		if err != nil {
			return errUnterminated
		}
		if b == '"' {
			c.writeSize(size)
			return nil
		}
		size++
		if b != '\\' {
			c.out.WriteByte(b)
			kept++
			continue
		}
		escape := []byte{b, 0}
		if escape[1], err = c.in.ReadByte(); err != nil {
			return errUnterminated
		}
		if escape[1] == 'u' {
			hex := make([]byte, 4)
			if _, err := io.ReadFull(c.in, hex); err != nil {
				return errUnterminated
			}
			escape = append(escape, hex...)
		}
		if kept+len(escape) > c.limit {
			break
		}
		c.out.Write(escape)
		kept += len(escape)
	}
	rest, err := c.skipString()
	if err != nil {
		return err
	}
	c.writeSize(size + rest)
	return nil
}

// writeSize closes a cut string and adds the size of the whole string as a
// key of its own.
func (c *cutter) writeSize(size int64) {
	c.out.WriteString(`","` + postDataSizeKey + `":` + strconv.FormatInt(size, 10))
}

// skipString skips the rest of a string, through its closing quote, a
// buffer at a time, and returns its size, counting each escape as one
// byte.
func (c *cutter) skipString() (int64, error) {
	var size int64
	afterBackslash, hexLeft := false, 0
	for {
		chunk, err := c.in.ReadSlice('"')
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return size, errUnterminated
		}
		for i := 0; i < len(chunk); {
			switch {
			case hexLeft > 0:
				n := min(hexLeft, len(chunk)-i)
				i += n
				hexLeft -= n
			case afterBackslash:
				afterBackslash = false
				if chunk[i] == 'u' {
					hexLeft = 4
				}
				i++
			default:
				j := bytes.IndexAny(chunk[i:], `\"`)
				if j < 0 {
					size += int64(len(chunk) - i)
					i = len(chunk)
					continue
				}
				size += int64(j)
				i += j
				if chunk[i] == '"' {
					return size, nil
				}
				size++
				afterBackslash = true
				i++
			}
		}
	}
}

// skipArray skips the rest of an array whose opening bracket was read.
func (c *cutter) skipArray() error {
	for depth := 1; depth > 0; {
		b, err := c.in.ReadByte()
		if err != nil {
			return errUnterminated
		}
		switch b {
		case '"':
			if _, err := c.skipString(); err != nil {
				return err
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
	}
	return nil
}

func space(b byte) bool { return b == ' ' || b == '\n' || b == '\r' || b == '\t' }
