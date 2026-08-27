// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCacheMode_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode  CacheMode
		valid bool
	}{
		{CacheModeLow, true},
		{CacheModeNormal, true},
		{CacheModeHigh, true},
		{CacheMode("invalid"), false},
		{CacheMode(""), false},
		{CacheMode("LOW"), false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.mode.IsValid())
		})
	}
}

func TestCacheMode_Limits_Low(t *testing.T) {
	t.Parallel()

	limits := CacheModeLow.Limits()

	assert.Equal(t, 500, limits.DAG.Limit)
	assert.Equal(t, 12*time.Hour, limits.DAG.TTL)

	assert.Equal(t, 1000, limits.DAGRun.Limit)
	assert.Equal(t, time.Hour, limits.DAGRun.TTL)

	assert.Equal(t, 100, limits.APIKey.Limit)
	assert.Equal(t, 15*time.Minute, limits.APIKey.TTL)

	assert.Equal(t, 100, limits.Webhook.Limit)
	assert.Equal(t, 15*time.Minute, limits.Webhook.TTL)
	assert.Equal(t, 8<<20, limits.EventStoreBytes)
}

func TestCacheMode_Limits_Normal(t *testing.T) {
	t.Parallel()

	limits := CacheModeNormal.Limits()

	assert.Equal(t, 1000, limits.DAG.Limit)
	assert.Equal(t, 12*time.Hour, limits.DAG.TTL)

	assert.Equal(t, 2000, limits.DAGRun.Limit)
	assert.Equal(t, time.Hour, limits.DAGRun.TTL)

	assert.Equal(t, 500, limits.APIKey.Limit)
	assert.Equal(t, 15*time.Minute, limits.APIKey.TTL)

	assert.Equal(t, 500, limits.Webhook.Limit)
	assert.Equal(t, 15*time.Minute, limits.Webhook.TTL)
	assert.Equal(t, 16<<20, limits.EventStoreBytes)
}

func TestCacheMode_Limits_High(t *testing.T) {
	t.Parallel()

	limits := CacheModeHigh.Limits()

	assert.Equal(t, 5000, limits.DAG.Limit)
	assert.Equal(t, 12*time.Hour, limits.DAG.TTL)

	assert.Equal(t, 5000, limits.DAGRun.Limit)
	assert.Equal(t, time.Hour, limits.DAGRun.TTL)

	assert.Equal(t, 1000, limits.APIKey.Limit)
	assert.Equal(t, 15*time.Minute, limits.APIKey.TTL)

	assert.Equal(t, 1000, limits.Webhook.Limit)
	assert.Equal(t, 15*time.Minute, limits.Webhook.TTL)
	assert.Equal(t, 32<<20, limits.EventStoreBytes)
}

func TestCacheMode_Limits_Invalid_DefaultsToNormal(t *testing.T) {
	t.Parallel()

	// Invalid mode should return normal limits
	limits := CacheMode("invalid").Limits()
	normalLimits := CacheModeNormal.Limits()

	assert.Equal(t, normalLimits.DAG.Limit, limits.DAG.Limit)
	assert.Equal(t, normalLimits.DAGRun.Limit, limits.DAGRun.Limit)
	assert.Equal(t, normalLimits.APIKey.Limit, limits.APIKey.Limit)
	assert.Equal(t, normalLimits.Webhook.Limit, limits.Webhook.Limit)
	assert.Equal(t, normalLimits.EventStoreBytes, limits.EventStoreBytes)
}

func TestCacheMode_Constants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, CacheMode("low"), CacheModeLow)
	assert.Equal(t, CacheMode("normal"), CacheModeNormal)
	assert.Equal(t, CacheMode("high"), CacheModeHigh)
}
