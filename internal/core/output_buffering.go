// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// OutputBuffering controls how step output is buffered before being flushed to
// the log stream. It applies to both local file writers and gRPC log streaming.
type OutputBuffering string

const (
	// OutputBufferingBuffer uses buffered writes (default, backward-compatible).
	// In gRPC streaming mode, output is accumulated until a 32KB threshold is
	// reached before being flushed. In local mode, output goes through a
	// bufio.Writer with a 4KB buffer.
	OutputBufferingBuffer OutputBuffering = "buffer"

	// OutputBufferingLine flushes output on every newline character ('\n').
	// This mode is best for interactive CLI tools and real-time log streaming
	// where observing output line-by-line is important. Trailing data without a
	// newline is kept in the buffer until either a newline arrives or the writer
	// is closed.
	OutputBufferingLine OutputBuffering = "line"

	// OutputBufferingNone disables all buffering. Every Write call is flushed
	// immediately. Use with caution in distributed mode — this may generate
	// excessive gRPC messages and increase overhead. Best for short-running
	// commands where latency is critical.
	OutputBufferingNone OutputBuffering = "none"
)
