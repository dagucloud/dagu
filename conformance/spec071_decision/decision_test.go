// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec071_decision_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const response = `{"model":"jev-test","provider":"TypeSafe","answers":{"department":{"type":"choice","choice":"billing","probabilities":{"billing":0.6,"other":0.4},"confidence":0.1},"urgency":{"type":"score","score":1.4,"legend":{"0":"Routine","1":"Soon","2":"Now"},"probabilities":{"0":0.1,"1":0.4,"2":0.5},"confidence":0.2},"refund":{"type":"noul","noul":0.95}},"usage":{"input_tokens":100,"output_tokens":25,"cost":0.0000042,"sequence":9007199254740993,"estimate":1.2300e+19}}`

func TestOutputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fixture, provider, path, want string }{
		{"automatic.yaml", "openrouter", "/root/decisions", "billing|1.4|0.95\n"},
		{"automatic.yaml", "typesafe", "/root/systemone", "billing|1.4|0.95\n"},
		{"raw.yaml", "openrouter", "/root/decisions", "billing|1.4|0.95\n"},
		{"selected.yaml", "openrouter", "/root/decisions", "billing\n"},
		{"stdout.yaml", "openrouter", "/root/decisions", "billing\n"},
		{"schema.yaml", "openrouter", "/root/decisions", "billing|1.4|0.95\n"},
		{"retry.yaml", "openrouter", "/root/decisions", "billing|1.4|0.95\n"},
	} {
		t.Run(tc.fixture+"/"+tc.provider, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, "Bearer decision-test-key", r.Header.Get("Authorization"))
				var body map[string]any
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Len(t, body, 3)
				assert.Equal(t, "jev-test", body["model"])
				assert.Equal(t, map[string]any{"message": "Refund please", "count": float64(2), "active": true}, body["state"])
				if calls.Add(1) == 1 && tc.fixture == "retry.yaml" {
					_, _ = io.WriteString(w, `{}`)
					return
				}
				_, _ = io.WriteString(w, response)
			}))
			defer srv.Close()
			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"ENDPOINT=" + srv.URL + "/root/", "PROVIDER=" + tc.provider, "DECISION_KEY=decision-test-key"}, "start", tc.fixture)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent("actual.txt", tc.want)
			if tc.fixture == "retry.yaml" {
				assert.EqualValues(t, 2, calls.Load())
			} else {
				assert.EqualValues(t, 1, calls.Load())
			}
			if tc.fixture == "automatic.yaml" {
				data, err := os.ReadFile(dagu.ProjectPath("named.json"))
				require.NoError(t, err)
				var expected map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(response), &expected))
				assert.JSONEq(t, string(expected["answers"]), string(data))
				// A reference reads the persisted value, so estimate arrives as
				// the plain decimal persistence wrote, not the provider's
				// exponent form.
				dagu.ExpectFileContent("numbers.txt", "4.2e-06|9007199254740993|12300000000000000000\n")
				usage, err := os.ReadFile(dagu.ProjectPath("usage.json"))
				require.NoError(t, err)
				var numbers map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(usage, &numbers))
				assert.Equal(t, "0.0000042", string(numbers["cost"]))
				assert.Equal(t, "9007199254740993", string(numbers["sequence"]))
				assert.Equal(t, "12300000000000000000", string(numbers["estimate"]))
			}
		})
	}
}

func TestFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fixture, body, wantErr string
		calls                        int32
	}{
		{"malformed", "automatic.yaml", `not json`, "decision response must be a JSON object", 1},
		{"mismatched", "automatic.yaml", strings.Replace(response, `"type":"noul"`, `"type":"score"`, 1), "missing or mismatched answer", 1},
		{"output_limit", "limited.yaml", response, "maximum size limit", 1},
		{"missing_key", "missing_key.yaml", response, "MISSING_DECISION_KEY", 0},
		{"timeout", "timeout.yaml", "", "timed out", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if tc.fixture == "timeout.yaml" {
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"ENDPOINT=" + srv.URL, "PROVIDER=openrouter", "DECISION_KEY=decision-test-key"}, "start", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.wantErr)
			assert.Equal(t, tc.calls, calls.Load())
		})
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{"invalid_provider.yaml", "missing_model.yaml", "invalid_criteria.yaml"} {
		t.Run(fixture, func(t *testing.T) {
			harness.NewRunner(t).Run("validate", fixture).ExpectNonZeroExitCode()
		})
	}
}
