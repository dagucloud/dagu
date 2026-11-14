// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !(windows && (amd64 || arm64))

package desktop

import "io"

// OpenMarks reports that this system draws no marks.
func OpenMarks() (Marks, error) { return nil, ErrUnsupported }

// ServeMarks reports that this system draws no marks.
func ServeMarks(io.Reader, io.Writer) error { return errNoMarksHelper }
