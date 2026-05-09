// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
)

const redisTestImage = "redis:7-alpine"

type redisTest struct {
	name            string
	dagConfigFunc   func(port int) string
	expectedOutputs map[string]any
}

func TestDAGLevelRedis(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()
	requireLinuxContainerRuntime(t)

	tests := []redisTest{
		{
			name: "BasicPing",
			dagConfigFunc: func(port int) string {
				return fmt.Sprintf(`
container:
  image: %s
  startup: entrypoint
  ports:
    - "%d:6379"

redis:
  host: localhost
  port: %d

steps:
  - name: ping
    action: redis.ping
    output: REDIS_OUT
`, redisTestImage, port, port)
			},
			expectedOutputs: map[string]any{
				"REDIS_OUT": "\"PONG\"",
			},
		},
		{
			name: "SetAndGet",
			dagConfigFunc: func(port int) string {
				return fmt.Sprintf(`
type: graph
container:
  image: %s
  startup: entrypoint
  ports:
    - "%d:6379"

redis:
  host: localhost
  port: %d

steps:
  - name: set-value
    action: redis.set
    with:
      key: test-key
      value: hello
  - name: get-value
    depends:
      - set-value
    action: redis.get
    with:
      key: test-key
    output: REDIS_GET_OUT
`, redisTestImage, port, port)
			},
			expectedOutputs: map[string]any{
				"REDIS_GET_OUT": "\"hello\"",
			},
		},
		{
			name: "StepOverridesDB",
			dagConfigFunc: func(port int) string {
				return fmt.Sprintf(`
type: graph
container:
  image: %s
  startup: entrypoint
  ports:
    - "%d:6379"

redis:
  host: localhost
  port: %d
  db: 0

steps:
  - name: set-in-db1
    action: redis.set
    with:
      key: db-test-key
      value: in-db1
      db: 1
  - name: get-from-db1
    depends:
      - set-in-db1
    action: redis.get
    with:
      key: db-test-key
      db: 1
    output: REDIS_DB1_OUT
`, redisTestImage, port, port)
			},
			expectedOutputs: map[string]any{
				"REDIS_DB1_OUT": "\"in-db1\"",
			},
		},
		{
			name: "Pipeline",
			dagConfigFunc: func(port int) string {
				return fmt.Sprintf(`
container:
  image: %s
  startup: entrypoint
  ports:
    - "%d:6379"

redis:
  host: localhost
  port: %d

steps:
  - name: run-pipeline
    action: redis.pipeline
    with:
      pipeline:
        - command: SET
          key: pipe-key1
          value: value1
        - command: SET
          key: pipe-key2
          value: value2
        - command: GET
          key: pipe-key1
        - command: GET
          key: pipe-key2
`, redisTestImage, port, port)
			},
			expectedOutputs: nil,
		},
		{
			name: "ListOperations",
			dagConfigFunc: func(port int) string {
				return fmt.Sprintf(`
type: graph
container:
  image: %s
  startup: entrypoint
  ports:
    - "%d:6379"

redis:
  host: localhost
  port: %d

steps:
  - name: lpush-items
    action: redis.lpush
    with:
      key: mylist
      values:
        - item1
        - item2
        - item3
  - name: llen
    depends:
      - lpush-items
    action: redis.llen
    with:
      key: mylist
    output: LIST_LEN
`, redisTestImage, port, port)
			},
			expectedOutputs: map[string]any{
				"LIST_LEN": "3",
			},
		},
		{
			name: "HashOperations",
			dagConfigFunc: func(port int) string {
				return fmt.Sprintf(`
type: graph
container:
  image: %s
  startup: entrypoint
  ports:
    - "%d:6379"

redis:
  host: localhost
  port: %d

steps:
  - name: hset-fields
    action: redis.hset
    with:
      key: myhash
      fields:
        field1: value1
        field2: value2
  - name: hget-single
    depends:
      - hset-fields
    action: redis.hget
    with:
      key: myhash
      field: field1
    output: HASH_OUT
`, redisTestImage, port, port)
			},
			expectedOutputs: map[string]any{
				"HASH_OUT": "\"value1\"",
			},
		},
		{
			name: "SetWithTTL",
			dagConfigFunc: func(port int) string {
				return fmt.Sprintf(`
type: graph
container:
  image: %s
  startup: entrypoint
  ports:
    - "%d:6379"

redis:
  host: localhost
  port: %d

steps:
  - name: set-with-ttl
    action: redis.set
    with:
      key: ttl-key
      value: ttl-value
      ttl: 3600
  - name: exists-check
    depends:
      - set-with-ttl
    action: redis.exists
    with:
      key: ttl-key
    output: EXISTS_OUT
`, redisTestImage, port, port)
			},
			expectedOutputs: map[string]any{
				"EXISTS_OUT": "1",
			},
		},
	}

	// Base port for parallel test execution - each test gets a unique port
	basePort := 16379

	for i, tt := range tests {
		port := basePort + i
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			th := test.Setup(t)
			dag := th.DAG(t, tt.dagConfigFunc(port))
			dag.Agent().RunSuccess(t)
			dag.AssertLatestStatus(t, core.Succeeded)
			if tt.expectedOutputs != nil {
				dag.AssertOutputs(t, tt.expectedOutputs)
			}
		})
	}
}
