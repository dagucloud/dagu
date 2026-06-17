// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	legacymodel "github.com/dagucloud/dagu/internal/persis/legacy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ exec.DAGStore = (*mockDAGStore)(nil)

// mockDAGStore implements models.DAGStore for testing
type mockDAGStore struct {
	dags map[string]*core.DAG
}

func (m *mockDAGStore) GetDetails(_ context.Context, path string, _ ...spec.LoadOption) (*core.DAG, error) {
	if dag, ok := m.dags[path]; ok {
		return dag, nil
	}
	return nil, os.ErrNotExist
}

func (m *mockDAGStore) List(_ context.Context, _ exec.ListDAGsOptions) (exec.PaginatedResult[*core.DAG], []string, error) {
	var dags []*core.DAG
	for _, dag := range m.dags {
		dags = append(dags, dag)
	}
	return exec.PaginatedResult[*core.DAG]{
		Items:      dags,
		TotalCount: len(dags),
	}, nil, nil
}

func (m *mockDAGStore) FindByName(_ context.Context, name string) (*core.DAG, error) {
	for _, dag := range m.dags {
		if dag.Name == name {
			return dag, nil
		}
	}
	return nil, os.ErrNotExist
}

func (m *mockDAGStore) Create(_ context.Context, _ string, _ []byte) error {
	return nil
}

func (m *mockDAGStore) Delete(_ context.Context, _ string) error {
	return nil
}

func (m *mockDAGStore) Update(_ context.Context, _ string, _ []byte) error {
	return nil
}

func (m *mockDAGStore) Rename(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockDAGStore) GetSpec(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockDAGStore) IsSuspended(_ context.Context, _ string) bool {
	return false
}

func (m *mockDAGStore) ToggleSuspend(_ context.Context, _ string, _ bool) error {
	return nil
}

func (m *mockDAGStore) GetMetadata(_ context.Context, _ string) (*core.DAG, error) {
	return nil, nil
}

func (m *mockDAGStore) Grep(_ context.Context, _ string) ([]*exec.GrepDAGsResult, []string, error) {
	return nil, nil, nil
}

func (m *mockDAGStore) SearchCursor(_ context.Context, _ exec.SearchDAGsOptions) (*exec.CursorResult[exec.SearchDAGResult], []string, error) {
	return &exec.CursorResult[exec.SearchDAGResult]{Items: []exec.SearchDAGResult{}}, nil, nil
}

func (m *mockDAGStore) SearchMatches(_ context.Context, _ string, _ exec.SearchDAGMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	return &exec.CursorResult[*exec.Match]{Items: []*exec.Match{}}, nil
}

func (m *mockDAGStore) UpdateSpec(_ context.Context, _ string, _ []byte) error {
	return nil
}

func (m *mockDAGStore) LoadSpec(_ context.Context, _ []byte, _ ...spec.LoadOption) (*core.DAG, error) {
	return nil, nil
}

func (m *mockDAGStore) LabelList(_ context.Context) ([]string, []string, error) {
	return nil, nil, nil
}

func TestExtractDAGName(t *testing.T) {
	tests := []struct {
		name     string
		dirName  string
		expected string
	}{
		{
			name:     "DirectoryWithHash",
			dirName:  "my-dag-a1b2c3d4",
			expected: "my-dag",
		},
		{
			name:     "DirectoryWithLongerHash",
			dirName:  "test-workflow-deadbeef1234",
			expected: "test-workflow",
		},
		{
			name:     "DirectoryWithoutHash",
			dirName:  "simple-dag",
			expected: "simple-dag",
		},
		{
			name:     "DirectoryWithMultipleHyphens",
			dirName:  "my-complex-dag-name-abc123",
			expected: "my-complex-dag-name",
		},
		{
			name:     "DirectoryWithNonHexSuffix",
			dirName:  "dag-with-suffix-xyz",
			expected: "dag-with-suffix-xyz",
		},
	}

	m := &historyMigrator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.extractDAGName(tt.dirName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReadLegacyStatusFile(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create a test status file with multiple lines
	testFile := filepath.Join(tempDir, "test.dat")
	status1 := legacymodel.Status{
		RequestID: "req123",
		Name:      "test-dag",
		Status:    core.Running,
		StartedAt: "2024-01-01T10:00:00Z",
	}
	status2 := legacymodel.Status{
		RequestID:  "req123",
		Name:       "test-dag",
		Status:     core.Succeeded,
		StartedAt:  "2024-01-01T10:00:00Z",
		FinishedAt: "2024-01-01T10:05:00Z",
	}

	// Write multiple status lines
	f, err := os.Create(testFile)
	require.NoError(t, err)

	data1, _ := json.Marshal(status1)
	data2, _ := json.Marshal(status2)
	_, err = f.WriteString(string(data1) + "\n")
	require.NoError(t, err)
	_, err = f.WriteString(string(data2) + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Test reading the file
	m := &historyMigrator{}
	statusFile, err := m.readLegacyStatusFile(testFile)
	require.NoError(t, err)
	require.NotNil(t, statusFile)

	// Should get the last status (finished one)
	assert.Equal(t, "req123", statusFile.Status.RequestID)
	assert.Equal(t, core.Succeeded, statusFile.Status.Status)
	assert.Equal(t, "2024-01-01T10:05:00Z", statusFile.Status.FinishedAt)
}

func TestNeedsMigration(t *testing.T) {
	ctx := context.Background()

	t.Run("NoHistoryDirectory", func(t *testing.T) {
		tempDir := t.TempDir()
		m := &historyMigrator{dataDir: tempDir}

		needsMigration, err := m.NeedsMigration(ctx)
		assert.NoError(t, err)
		assert.False(t, needsMigration)
	})

	t.Run("HistoryDirectoryWithDatFiles", func(t *testing.T) {
		tempDir := t.TempDir()

		// Create legacy structure
		dagDir := filepath.Join(tempDir, "my-dag-abc123")
		require.NoError(t, os.MkdirAll(dagDir, 0750))

		datFile := filepath.Join(dagDir, "my-dag.20240101.100000.req123.dat")
		require.NoError(t, os.WriteFile(datFile, []byte(`{"RequestId":"req123"}`), 0600))

		m := &historyMigrator{dataDir: tempDir}
		needsMigration, err := m.NeedsMigration(ctx)
		assert.NoError(t, err)
		assert.True(t, needsMigration)
	})

	t.Run("HistoryDirectoryWithoutDatFiles", func(t *testing.T) {
		tempDir := t.TempDir()

		// Create directory without .dat files
		dagDir := filepath.Join(tempDir, "my-dag-abc123")
		require.NoError(t, os.MkdirAll(dagDir, 0750))

		otherFile := filepath.Join(dagDir, "other.txt")
		require.NoError(t, os.WriteFile(otherFile, []byte("test"), 0600))

		m := &historyMigrator{dataDir: tempDir}
		needsMigration, err := m.NeedsMigration(ctx)
		assert.NoError(t, err)
		assert.False(t, needsMigration)
	})
}

func TestConvertStatus(t *testing.T) {
	legacy := &legacymodel.Status{
		RequestID:  "req123",
		Name:       "test-dag",
		Status:     core.Succeeded,
		PID:        legacymodel.PID(12345),
		Log:        "test log",
		StartedAt:  "2024-01-01T10:00:00Z",
		FinishedAt: "2024-01-01T10:05:00Z",
		Params:     "param1=value1",
		ParamsList: []string{"param1", "value1"},
		Nodes: []*legacymodel.Node{
			{
				Step: core.Step{
					Name: "step1",
				},
				Status:     core.NodeSucceeded,
				StartedAt:  "2024-01-01T10:01:00Z",
				FinishedAt: "2024-01-01T10:02:00Z",
				Log:        "step log",
			},
		},
		OnSuccess: &legacymodel.Node{
			Step: core.Step{
				Name: "on_success",
			},
			Status: core.NodeSucceeded,
		},
	}

	dag := &core.DAG{
		Name: "test-dag",
		Preconditions: []*core.Condition{
			{Condition: "test condition"},
		},
	}

	m := &historyMigrator{}
	result := m.convertStatus(legacy, dag)

	assert.Equal(t, "test-dag", result.Name)
	assert.Equal(t, "req123", result.DAGRunID)
	assert.Equal(t, core.Succeeded, result.Status)
	assert.Equal(t, exec.PID(12345), result.PID)
	assert.Equal(t, "test log", result.Log)
	assert.Equal(t, "param1=value1", result.Params)
	assert.Equal(t, []string{"param1", "value1"}, result.ParamsList)
	assert.NotEmpty(t, result.StartedAt)
	assert.NotEmpty(t, result.FinishedAt)
	assert.Len(t, result.Nodes, 1)
	assert.NotNil(t, result.OnSuccess)
	assert.Equal(t, 1, len(result.Preconditions))
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		name      string
		timeStr   string
		shouldErr bool
	}{
		{
			name:      "RFC3339Format",
			timeStr:   "2024-01-01T10:00:00Z",
			shouldErr: false,
		},
		{
			name:      "RFC3339WithTimezone",
			timeStr:   "2024-01-01T10:00:00+09:00",
			shouldErr: false,
		},
		{
			name:      "SpaceSeparatedFormat",
			timeStr:   "2024-01-01 10:00:00",
			shouldErr: false,
		},
		{
			name:      "EmptyString",
			timeStr:   "",
			shouldErr: true,
		},
		{
			name:      "DashOnly",
			timeStr:   "-",
			shouldErr: true,
		},
		{
			name:      "InvalidFormat",
			timeStr:   "not a date",
			shouldErr: true,
		},
	}

	m := &historyMigrator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.parseTime(tt.timeStr)
			if tt.shouldErr {
				assert.Error(t, err)
				assert.True(t, result.IsZero())
			} else {
				assert.NoError(t, err)
				assert.False(t, result.IsZero())
			}
		})
	}
}

func TestMoveLegacyData(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Create legacy directories with .dat files
	legacyDir1 := filepath.Join(tempDir, "dag1-abc123")
	require.NoError(t, os.MkdirAll(legacyDir1, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir1, "test.dat"), []byte("data"), 0600))

	legacyDir2 := filepath.Join(tempDir, "dag2-def456")
	require.NoError(t, os.MkdirAll(legacyDir2, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir2, "test.dat"), []byte("data"), 0600))

	// Create a non-legacy directory
	otherDir := filepath.Join(tempDir, "other-dir")
	require.NoError(t, os.MkdirAll(otherDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(otherDir, "test.txt"), []byte("data"), 0600))

	m := &historyMigrator{dataDir: tempDir}
	err := m.MoveLegacyData(ctx)
	require.NoError(t, err)

	// Check that legacy directories were moved
	_, err = os.Stat(legacyDir1)
	assert.True(t, os.IsNotExist(err), "legacy directory 1 should be moved")

	_, err = os.Stat(legacyDir2)
	assert.True(t, os.IsNotExist(err), "legacy directory 2 should be moved")

	// Check that non-legacy directory still exists
	_, err = os.Stat(otherDir)
	assert.NoError(t, err, "non-legacy directory should still exist")

	// Check that archive directory was created
	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	archiveFound := false
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) > 17 && entry.Name()[:17] == "history_migrated_" {
			archiveFound = true

			// Check archive contents
			archiveDir := filepath.Join(tempDir, entry.Name())
			archiveEntries, err := os.ReadDir(archiveDir)
			require.NoError(t, err)
			assert.Len(t, archiveEntries, 2, "should have 2 moved directories")
		}
	}
	assert.True(t, archiveFound, "archive directory should be created")
}

func TestLoadDAGForMigration(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Create test DAG files
	dag1Path := filepath.Join(tempDir, "test-dag.yaml")
	dag2Path := filepath.Join(tempDir, "test-dag-v2.yml")

	dag1 := &core.DAG{Name: "test-dag", Location: dag1Path}
	dag2 := &core.DAG{Name: "test-dag-v2", Location: dag2Path}

	mockStore := &mockDAGStore{
		dags: map[string]*core.DAG{
			dag1Path: dag1,
			dag2Path: dag2,
		},
	}

	m := &historyMigrator{
		dagStore: mockStore,
		dagsDir:  tempDir,
	}

	// Create the actual files
	require.NoError(t, os.WriteFile(dag1Path, []byte("test"), 0600))
	require.NoError(t, os.WriteFile(dag2Path, []byte("test"), 0600))

	t.Run("FindByStatusName", func(t *testing.T) {
		result, err := m.loadDAGForMigration(ctx, "test-dag", "other-name")
		require.NoError(t, err)
		assert.Equal(t, "test-dag", result.Name)
	})

	t.Run("FindByDirectoryName", func(t *testing.T) {
		result, err := m.loadDAGForMigration(ctx, "not-found", "test-dag-v2")
		require.NoError(t, err)
		assert.Equal(t, "test-dag-v2", result.Name)
	})

	t.Run("NotFoundCreateMinimal", func(t *testing.T) {
		result, err := m.loadDAGForMigration(ctx, "not-found", "also-not-found")
		require.NoError(t, err)
		assert.Equal(t, "not-found", result.Name)
	})
}

func TestFullMigration(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Set up directories - use subdirectories to avoid counting them as legacy dirs
	dataDir := filepath.Join(tempDir, "data")
	dagRunsDir := filepath.Join(tempDir, "runs")
	dagsDir := filepath.Join(tempDir, "dags")

	require.NoError(t, os.MkdirAll(dataDir, 0750))
	require.NoError(t, os.MkdirAll(dagRunsDir, 0750))
	require.NoError(t, os.MkdirAll(dagsDir, 0750))

	// Create legacy data
	legacyDagDir := filepath.Join(dataDir, "test-dag-abc123")
	require.NoError(t, os.MkdirAll(legacyDagDir, 0750))

	// Create legacy status
	legacyStatus := legacymodel.Status{
		RequestID:  "req123",
		Name:       "test-dag",
		Status:     core.Succeeded,
		StartedAt:  time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		FinishedAt: time.Now().Add(-30 * time.Minute).Format(time.RFC3339),
		Nodes: []*legacymodel.Node{
			{
				Step:   core.Step{Name: "step1"},
				Status: core.NodeSucceeded,
			},
		},
	}

	// Write legacy data file
	statusData, _ := json.Marshal(legacyStatus)
	datFile := filepath.Join(legacyDagDir, "test-dag.20240101.100000.req123.dat")
	require.NoError(t, os.WriteFile(datFile, statusData, 0600))

	// Create DAG file
	dagPath := filepath.Join(dagsDir, "test-dag.yaml")
	testDAG := &core.DAG{
		Name:     "test-dag",
		Location: dagPath,
	}
	require.NoError(t, os.WriteFile(dagPath, []byte("name: test-dag"), 0600))

	// Set up stores
	dagRunStore := dagrun.New(dagRunsDir)
	dagStore := &mockDAGStore{
		dags: map[string]*core.DAG{
			dagPath: testDAG,
		},
	}

	// Create migrator
	migrator := newHistoryMigrator(dagRunStore, dagStore, dataDir, dagsDir)

	// Check migration is needed
	needsMigration, err := migrator.NeedsMigration(ctx)
	require.NoError(t, err)
	assert.True(t, needsMigration)

	// Run migration
	result, err := migrator.Migrate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalDAGs)
	assert.Equal(t, 1, result.TotalRuns)
	assert.Equal(t, 1, result.MigratedRuns)
	assert.Equal(t, 0, result.FailedRuns)

	// Verify migration
	attempt, err := dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef("test-dag", "req123"))
	require.NoError(t, err)
	require.NotNil(t, attempt)

	dagRunStatus, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, "req123", dagRunStatus.DAGRunID)
	assert.Equal(t, "test-dag", dagRunStatus.Name)
	assert.Equal(t, core.Succeeded, dagRunStatus.Status)
	assert.Len(t, dagRunStatus.Nodes, 1)

	// Move legacy data
	err = migrator.MoveLegacyData(ctx)
	require.NoError(t, err)

	// Verify legacy directory was moved
	_, err = os.Stat(legacyDagDir)
	assert.True(t, os.IsNotExist(err))
}
