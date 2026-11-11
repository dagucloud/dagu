// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

const (
	// ChunkBytes is the largest chunk of a request body or of a response body in
	// one message of the stream (Spec 094, "Limits and timeouts").
	ChunkBytes = 65536
	// MaxMessageBytes is the largest message that a receiver of the stream
	// accepts. A larger message closes the stream with ResourceExhausted.
	MaxMessageBytes = 1 << 20
)

// Limits are the limits and the timeouts of the gateway. They come from the block
// ai.relay of the configuration of the coordinator.
type Limits struct {
	MaxRequestBytes       int64
	MaxResponseBytes      int64
	MaxHeaderBytes        int64
	MaxStreamsPerRun      int
	MaxStreams            int
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	IdleTimeout           time.Duration
	TotalTimeout          time.Duration
}

// LimitsFromConfig reads the block ai.relay. The loader already checked the
// ranges, so no value changes here.
func LimitsFromConfig(c config.AIRelayConfig) Limits {
	return Limits{
		MaxRequestBytes:       int64(c.MaxRequestBytes),
		MaxResponseBytes:      int64(c.MaxResponseBytes),
		MaxHeaderBytes:        int64(c.MaxHeaderBytes),
		MaxStreamsPerRun:      c.MaxStreamsPerRun,
		MaxStreams:            c.MaxStreams,
		ConnectTimeout:        time.Duration(c.ConnectTimeoutSec) * time.Second,
		ResponseHeaderTimeout: time.Duration(c.ResponseHeaderTimeoutSec) * time.Second,
		IdleTimeout:           time.Duration(c.IdleTimeoutSec) * time.Second,
		TotalTimeout:          time.Duration(c.TotalTimeoutSec) * time.Second,
	}
}

// streamCounter counts the open streams of the coordinator and of each run.
type streamCounter struct {
	mu        sync.Mutex
	total     int
	perRun    map[string]int
	maxTotal  int
	maxPerRun int
}

func newStreamCounter(maxTotal, maxPerRun int) *streamCounter {
	return &streamCounter{perRun: map[string]int{}, maxTotal: maxTotal, maxPerRun: maxPerRun}
}

// acquire takes a slot for the run. It returns the function that gives the slot
// back, or a failure with the reason too_many_streams.
func (c *streamCounter) acquire(run string) (func(), *Failure) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total >= c.maxTotal {
		return nil, Fail(ReasonTooManyStreams, "the coordinator has %d open streams", c.total)
	}
	if c.perRun[run] >= c.maxPerRun {
		return nil, Fail(ReasonTooManyStreams, "the run has %d open streams", c.perRun[run])
	}
	c.total++
	c.perRun[run]++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.total--
			if c.perRun[run]--; c.perRun[run] <= 0 {
				delete(c.perRun, run)
			}
		})
	}, nil
}

// open returns the number of open streams of the coordinator.
func (c *streamCounter) open() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}
