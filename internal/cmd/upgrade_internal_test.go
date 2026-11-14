// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/upgrade"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restartAfterUpgrade changes or restarts the service only on a yes, and
// leans toward keeping runs in progress when the restart would end them.
func TestRestartAfterUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in service commands need a POSIX shell")
	}

	type prompt struct {
		text       string
		defaultYes bool
	}
	const (
		runItNow       = "Run it now?"
		restartNow     = "Restart Dagu now to use the new version?"
		restartEndRuns = "Restart Dagu now? Runs in progress will end."
	)

	tests := []struct {
		name         string
		stopsRuns    bool
		keepRunsFail bool
		answers      []bool
		wantPrompts  []prompt
		wantRan      []string
	}{
		{
			name:        "restart that keeps runs",
			answers:     []bool{true},
			wantPrompts: []prompt{{restartNow, true}},
			wantRan:     []string{"restart"},
		},
		{
			name:        "restart declined",
			answers:     []bool{false},
			wantPrompts: []prompt{{restartNow, true}},
		},
		{
			name:        "keep runs, then restart",
			stopsRuns:   true,
			answers:     []bool{true, true},
			wantPrompts: []prompt{{runItNow, true}, {restartNow, true}},
			wantRan:     []string{"keep-runs", "restart"},
		},
		{
			name:        "keeping runs declined",
			stopsRuns:   true,
			answers:     []bool{false, false},
			wantPrompts: []prompt{{runItNow, true}, {restartEndRuns, false}},
		},
		{
			name:         "keeping runs fails",
			stopsRuns:    true,
			keepRunsFail: true,
			answers:      []bool{true, true},
			wantPrompts:  []prompt{{runItNow, true}, {restartEndRuns, false}},
			wantRan:      []string{"keep-runs", "restart"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranLog := filepath.Join(t.TempDir(), "ran")
			record := func(name string) string { return "echo " + name + " >> " + ranLog }
			ran := func() []string {
				data, err := os.ReadFile(ranLog) //nolint:gosec // test-owned file
				if os.IsNotExist(err) {
					return nil
				}
				require.NoError(t, err)
				return strings.Fields(string(data))
			}

			keepsRuns := &upgrade.Service{RestartCommand: record("restart")}
			service := keepsRuns
			if tt.stopsRuns {
				keepRunsCommand := record("keep-runs")
				if tt.keepRunsFail {
					keepRunsCommand += " && exit 1"
				}
				service = &upgrade.Service{
					RestartCommand:  record("restart"),
					StopsRuns:       true,
					KeepRunsCommand: keepRunsCommand,
				}
			}
			// The files always read as keeping runs once the command ran, as
			// when the drop-in was written but reloading systemd failed.
			detect := func() *upgrade.Service { return keepsRuns }

			var prompts []prompt
			ask := func(text string, defaultYes bool) bool {
				prompts = append(prompts, prompt{text, defaultYes})
				require.Less(t, len(prompts)-1, len(tt.answers), "unexpected prompt %q", text)
				return tt.answers[len(prompts)-1]
			}

			restartAfterUpgrade(t.Context(), service, ask, detect)

			assert.Equal(t, tt.wantPrompts, prompts)
			assert.Equal(t, tt.wantRan, ran())
		})
	}
}
