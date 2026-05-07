// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filedag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/filedag/dagindex"
	"github.com/dagucloud/dagu/internal/persis/filedag/grep"
	"github.com/dagucloud/dagu/internal/workspace"
	indexv1 "github.com/dagucloud/dagu/proto/index/v1"
)

var _ exec.DAGStore = (*Storage)(nil)

const dagSearchCursorVersion = 1

// Option is a functional option for configuring the DAG repository
type Option func(*Options)

// Options contains configuration options for the DAG repository
type Options struct {
	FlagsBaseDir           string                     // Base directory for flag store
	FileCache              *fileutil.Cache[*core.DAG] // Optional cache for DAG objects
	SearchPaths            []string                   // Additional search paths for DAG files
	BaseConfigPath         string                     // Optional base config file applied when loading DAGs
	WorkspaceBaseConfigDir string                     // Optional directory containing workspace base configs
	SkipExamples           bool                       // Skip creating example DAGs
	SkipDirectoryCreation  bool                       // Skip creating base directory (for worker mode)
}

// WithFileCache returns a DAGRepositoryOption that sets the file cache for DAG objects
func WithFileCache(cache *fileutil.Cache[*core.DAG]) Option {
	return func(o *Options) {
		o.FileCache = cache
	}
}

// WithFlagsBaseDir returns a DAGRepositoryOption that sets the base directory for flag store
func WithFlagsBaseDir(dir string) Option {
	return func(o *Options) {
		o.FlagsBaseDir = dir
	}
}

// WithSearchPaths returns a DAGRepositoryOption that sets additional search paths for DAG files
func WithSearchPaths(paths []string) Option {
	return func(o *Options) {
		o.SearchPaths = paths
	}
}

// WithBaseConfig returns a DAGRepositoryOption that sets the base config file
// used when loading DAGs from the store.
func WithBaseConfig(path string) Option {
	return func(o *Options) {
		o.BaseConfigPath = path
	}
}

// WithWorkspaceBaseConfigDir returns a DAGRepositoryOption that sets the directory
// containing per-workspace base configs.
func WithWorkspaceBaseConfigDir(dir string) Option {
	return func(o *Options) {
		o.WorkspaceBaseConfigDir = dir
	}
}

// WithSkipExamples returns a DAGRepositoryOption that disables example DAG creation
func WithSkipExamples(skip bool) Option {
	return func(o *Options) {
		o.SkipExamples = skip
	}
}

// WithSkipDirectoryCreation returns a DAGRepositoryOption that skips base directory creation.
// This is used for worker mode where the DAGs directory should not be created.
func WithSkipDirectoryCreation(skip bool) Option {
	return func(o *Options) {
		o.SkipDirectoryCreation = skip
	}
}

// New creates a new DAG store implementation using the local filesystem
func New(baseDir string, opts ...Option) exec.DAGStore {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}
	if options.FlagsBaseDir == "" {
		options.FlagsBaseDir = filepath.Join(baseDir, "flags")
	}
	// Build search paths in deterministic order: baseDir first, then ".", then additional paths.
	seen := make(map[string]struct{})
	var searchPaths []string
	for _, p := range append([]string{baseDir, "."}, options.SearchPaths...) {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		searchPaths = append(searchPaths, p)
	}

	return &Storage{
		baseDir:                baseDir,
		flagsBaseDir:           options.FlagsBaseDir,
		fileCache:              options.FileCache,
		searchPaths:            searchPaths,
		baseConfigPath:         options.BaseConfigPath,
		workspaceBaseConfigDir: options.WorkspaceBaseConfigDir,
		baseConfigState:        describeBaseConfigStateSet(options.BaseConfigPath, options.WorkspaceBaseConfigDir),
		skipExamples:           options.SkipExamples,
		skipDirectoryCreation:  options.SkipDirectoryCreation,
	}
}

// Storage implements the DAGRepository interface using the local filesystem
type Storage struct {
	baseDir                string                     // Base directory for DAG storage
	flagsBaseDir           string                     // Base directory for flag store
	fileCache              *fileutil.Cache[*core.DAG] // Optional cache for DAG objects
	searchPaths            []string                   // Additional search paths for DAG files
	baseConfigPath         string                     // Optional base config file applied when loading DAGs
	workspaceBaseConfigDir string                     // Optional directory containing workspace base configs
	baseConfigState        string                     // Last observed base config state for cache/index invalidation
	skipExamples           bool                       // Skip creating example DAGs
	skipDirectoryCreation  bool                       // Skip creating base directory (for worker mode)
	baseConfigMu           sync.Mutex                 // Protects base config state refresh and invalidation
	indexMu                sync.Mutex                 // Protects index load/rebuild/invalidate
}

func (store *Storage) useCachedLoads() bool {
	return store.fileCache != nil
}

func (store *Storage) useIndexedMetadata() bool {
	return true
}

func (store *Storage) readSuspendFlags(ctx context.Context) dagindex.SuspendFlags {
	flags := make(dagindex.SuspendFlags)
	flagEntries, err := os.ReadDir(store.flagsBaseDir)
	if err != nil {
		// Flags directory may not exist yet (e.g., new installations).
		logger.Debug(ctx, "Could not read flags directory", tag.Dir(store.flagsBaseDir))
		return flags
	}
	for _, fe := range flagEntries {
		if !fe.IsDir() {
			flags[fe.Name()] = struct{}{}
		}
	}
	return flags
}

func (store *Storage) defaultLoadOptions(opts ...spec.LoadOption) []spec.LoadOption {
	loadOpts := make([]spec.LoadOption, 0, len(opts)+1)
	if store.baseConfigPath != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(store.baseConfigPath))
	}
	if store.workspaceBaseConfigDir != "" {
		loadOpts = append(loadOpts, spec.WithWorkspaceBaseConfigDir(store.workspaceBaseConfigDir))
	}
	loadOpts = append(loadOpts, opts...)
	return loadOpts
}

func (store *Storage) refreshBaseConfigState() {
	if store.baseConfigPath == "" && store.workspaceBaseConfigDir == "" {
		return
	}

	state := describeBaseConfigStateSet(store.baseConfigPath, store.workspaceBaseConfigDir)

	store.baseConfigMu.Lock()
	defer store.baseConfigMu.Unlock()

	if state == store.baseConfigState {
		return
	}

	if store.fileCache != nil {
		store.fileCache.InvalidateAll()
	}
	store.invalidateIndex()
	store.baseConfigState = state
}

func describeBaseConfigStateSet(basePath, workspaceDir string) string {
	parts := make([]string, 0, 2)
	if basePath != "" {
		parts = append(parts, "global="+describeBaseConfigState(basePath))
	}
	if workspaceDir != "" {
		parts = append(parts, "workspaces="+describeWorkspaceBaseConfigState(workspaceDir))
	}
	return strings.Join(parts, "|")
}

func describeBaseConfigState(path string) string {
	if path == "" {
		return ""
	}

	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing"
		}
		return "error:" + err.Error()
	}

	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

func describeWorkspaceBaseConfigState(dir string) string {
	if dir == "" {
		return ""
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing"
		}
		return "error:" + err.Error()
	}

	states := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		configPath := filepath.Join(dir, entry.Name(), workspace.BaseConfigFileName)
		fi, err := os.Stat(configPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			states = append(states, entry.Name()+":error:"+err.Error())
			continue
		}
		states = append(states, fmt.Sprintf("%s:%d:%d", entry.Name(), fi.Size(), fi.ModTime().UnixNano()))
	}
	sort.Strings(states)
	return strings.Join(states, ",")
}

// Initialize ensures the storage is ready and creates example DAGs if needed
func (store *Storage) Initialize() error {
	return store.ensureDirExist()
}

// GetMetadata retrieves the metadata of a DAG by its name.
func (store *Storage) GetMetadata(ctx context.Context, name string) (*core.DAG, error) {
	filePath, err := store.locateDAG(name)
	if err != nil {
		return nil, fmt.Errorf("failed to locate DAG %s in search paths (%v): %w", name, store.searchPaths, err)
	}
	store.refreshBaseConfigState()
	loadOpts := store.defaultLoadOptions(
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
	if !store.useCachedLoads() {
		return spec.Load(ctx, filePath, loadOpts...)
	}
	return store.fileCache.LoadLatest(filePath, func() (*core.DAG, error) {
		return spec.Load(ctx, filePath, loadOpts...)
	})
}

// GetDetails retrieves the details of a DAG by its name.
func (store *Storage) GetDetails(ctx context.Context, name string, opts ...spec.LoadOption) (*core.DAG, error) {
	filePath, err := store.locateDAG(name)
	if err != nil {
		return nil, fmt.Errorf("failed to locate DAG %s: %w", name, err)
	}
	loadOpts := store.defaultLoadOptions(opts...)
	loadOpts = append(loadOpts, spec.WithoutEval())

	dat, err := spec.Load(ctx, filePath, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load DAG %s: %w", name, err)
	}
	return dat, nil
}

// GetSpec retrieves the specification of a DAG by its name.
func (store *Storage) GetSpec(_ context.Context, name string) (string, error) {
	filePath, err := store.locateDAG(name)
	if err != nil {
		return "", exec.ErrDAGNotFound
	}
	dat, err := os.ReadFile(filePath) // nolint:gosec
	if err != nil {
		return "", err
	}
	return string(dat), nil
}

// FileMode used for newly created DAG files
const defaultPerm os.FileMode = 0600

func (store *Storage) LoadSpec(ctx context.Context, yamlSpec []byte, opts ...spec.LoadOption) (*core.DAG, error) {
	// Validate the spec before saving it.
	loadOpts := store.defaultLoadOptions(slices.Clone(opts)...)
	loadOpts = append(loadOpts, spec.WithoutEval())
	return spec.LoadYAML(ctx, yamlSpec, loadOpts...)
}

// UpdateSpec updates the specification of a DAG by its name.
func (store *Storage) UpdateSpec(ctx context.Context, name string, yamlSpec []byte) error {
	// Validate the spec before saving it.
	dag, err := spec.LoadYAML(ctx, yamlSpec, store.defaultLoadOptions(
		spec.WithName(name),
		spec.WithoutEval(),
	)...)
	if err != nil {
		return err
	}
	if err := dag.Validate(); err != nil {
		return err
	}
	filePath, err := store.locateDAG(name)
	if err != nil {
		return fmt.Errorf("failed to locate DAG %s: %w", name, err)
	}
	if err := fileutil.WriteFileAtomic(filePath, yamlSpec, defaultPerm); err != nil {
		return err
	}
	if store.fileCache != nil {
		store.fileCache.Invalidate(filePath)
	}
	store.invalidateIndex()
	return nil
}

// Create creates a new DAG with the given name and specification.
func (store *Storage) Create(_ context.Context, name string, spec []byte) error {
	if err := store.ensureDirExist(); err != nil {
		return fmt.Errorf("failed to create DAGs directory %s: %w", store.baseDir, err)
	}
	filePath := store.generateFilePath(name)
	if fileExists(filePath) {
		return exec.ErrDAGAlreadyExists
	}
	if err := fileutil.WriteFileAtomic(filePath, spec, defaultPerm); err != nil {
		return fmt.Errorf("failed to write DAG %s: %w", name, err)
	}
	store.invalidateIndex()
	return nil
}

// Delete deletes a DAG by its name.
func (store *Storage) Delete(_ context.Context, name string) error {
	filePath, err := store.locateDAG(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to locate DAG %s: %w", name, err)
	}
	if err := os.Remove(filePath); err != nil {
		return err
	}
	if store.fileCache != nil {
		store.fileCache.Invalidate(filePath)
	}
	store.invalidateIndex()
	return nil
}

// ensureDirExist ensures that the base directory exists.
func (store *Storage) ensureDirExist() error {
	// Skip directory creation for worker mode
	if store.skipDirectoryCreation {
		return nil
	}
	if !fileExists(store.baseDir) {
		if err := os.MkdirAll(store.baseDir, 0750); err != nil {
			return err
		}
		// Create example DAGs for first-time users
		_ = store.createExampleDAGs() // Errors are logged internally
	} else {
		// Check if directory is empty and create examples if needed
		if shouldCreateExamples(store.baseDir) {
			_ = store.createExampleDAGs() // Errors are logged internally
		}
	}
	return nil
}

// loadOrRebuildIndex returns validated index entries, rebuilding if necessary.
// Returns nil on any failure (caller falls back to direct scan).
func (store *Storage) loadOrRebuildIndex(ctx context.Context) []*indexv1.DAGIndexEntry {
	if !store.useIndexedMetadata() {
		return nil
	}

	store.refreshBaseConfigState()

	store.indexMu.Lock()
	defer store.indexMu.Unlock()

	entries, err := os.ReadDir(store.baseDir)
	if err != nil {
		return nil
	}

	// Collect YAML file metadata.
	var yamlFiles []dagindex.YAMLFileMeta
	for _, entry := range entries {
		if entry.IsDir() || !fileutil.IsYAMLFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		yamlFiles = append(yamlFiles, dagindex.YAMLFileMeta{
			Name:    entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		})
	}

	// Build suspend flags set.
	flags := store.readSuspendFlags(ctx)

	indexPath := filepath.Join(store.baseDir, dagindex.IndexFileName)

	// Try loading existing index.
	if cached := dagindex.Load(indexPath, yamlFiles, flags); cached != nil {
		return cached
	}

	// Rebuild.
	logger.Info(ctx, "Rebuilding DAG definition index", tag.Dir(store.baseDir))
	idx := dagindex.Build(ctx, store.baseDir, yamlFiles, flags, store.defaultLoadOptions()...)
	if err := dagindex.Write(indexPath, idx); err != nil {
		logger.Warn(ctx, "Failed to write DAG definition index", tag.Error(err))
	}
	return idx.Entries
}

// invalidateIndex removes the index file so the next read triggers a rebuild.
func (store *Storage) invalidateIndex() {
	store.indexMu.Lock()
	defer store.indexMu.Unlock()
	_ = os.Remove(filepath.Join(store.baseDir, dagindex.IndexFileName))
}

// List lists DAGs with pagination support.
func (store *Storage) List(ctx context.Context, opts exec.ListDAGsOptions) (exec.PaginatedResult[*core.DAG], []string, error) {
	var allDags []*core.DAG
	var errList []string

	if opts.Paginator == nil {
		p := exec.DefaultPaginator()
		opts.Paginator = &p
	}

	// Try index-accelerated path.
	if indexEntries := store.loadOrRebuildIndex(ctx); indexEntries != nil {
		for _, entry := range indexEntries {
			if ctx.Err() != nil {
				return exec.NewPaginatedResult([]*core.DAG{}, 0, *opts.Paginator), nil, ctx.Err()
			}

			dag := dagindex.DAGFromEntry(entry, store.baseDir)
			if opts.Name != "" && !matchesDAGListSearch(dag.Name, dag.FileName(), opts.Name) {
				continue
			}
			if len(opts.Labels) > 0 && !containsAllLabels(dag.Labels, opts.Labels) {
				continue
			}
			if !opts.WorkspaceFilter.MatchesLabels(dag.Labels) {
				continue
			}

			allDags = append(allDags, dag)
		}
	} else {
		// Fallback: direct filesystem scan.
		entries, err := os.ReadDir(store.baseDir)
		if err != nil {
			errList = append(errList, fmt.Sprintf("failed to read directory %s: %s", store.baseDir, err))
			return exec.NewPaginatedResult([]*core.DAG{}, 0, *opts.Paginator), errList, err
		}

		for _, entry := range entries {
			if ctx.Err() != nil {
				return exec.NewPaginatedResult([]*core.DAG{}, 0, *opts.Paginator), nil, ctx.Err()
			}

			if entry.IsDir() || !fileutil.IsYAMLFile(entry.Name()) {
				continue
			}

			baseName := path.Base(entry.Name())
			fileName := strings.TrimSuffix(baseName, path.Ext(baseName))

			filePath := filepath.Join(store.baseDir, entry.Name())
			dag, err := spec.Load(ctx, filePath, store.defaultLoadOptions(
				spec.OnlyMetadata(),
				spec.WithoutEval(),
				spec.SkipSchemaValidation(),
				spec.WithAllowBuildErrors(),
			)...)
			if err != nil {
				errList = append(errList, fmt.Sprintf("reading %s failed: %s", fileName, err))
				continue
			}

			if opts.Name != "" && !matchesDAGListSearch(dag.Name, fileName, opts.Name) {
				continue
			}

			if len(opts.Labels) > 0 && !containsAllLabels(dag.Labels, opts.Labels) {
				continue
			}
			if !opts.WorkspaceFilter.MatchesLabels(dag.Labels) {
				continue
			}

			allDags = append(allDags, dag)
		}
	}

	switch opts.Sort {
	case "nextRun":
		now := time.Now()
		if opts.Time != nil {
			now = *opts.Time
		}
		projectNextRun := opts.NextRunProjection
		if projectNextRun == nil {
			projectNextRun = func(dag *core.DAG, at time.Time) time.Time {
				return dag.NextRun(at)
			}
		}
		// Pre-calculate next run times to avoid recalculating on each comparison
		suspendFlags := store.readSuspendFlags(ctx)
		nextRunTimes := make(map[*core.DAG]time.Time, len(allDags))
		for _, dag := range allDags {
			if _, suspended := suspendFlags[fileName(dag.FileName())]; suspended {
				nextRunTimes[dag] = time.Time{}
				continue
			}
			nextRunTimes[dag] = projectNextRun(dag, now)
		}

		// Sort DAGs by next run time using cached values
		sort.Slice(allDags, func(i, j int) bool {
			// Default to ascending order
			ascending := opts.Order != "desc"

			nextRun1 := nextRunTimes[allDags[i]]
			nextRun2 := nextRunTimes[allDags[j]]

			if nextRun1.IsZero() && nextRun2.IsZero() {
				// If both are zero, sort by name (case-insensitive)
				if ascending {
					return strings.ToLower(allDags[i].Name) < strings.ToLower(allDags[j].Name)
				}
				return strings.ToLower(allDags[i].Name) > strings.ToLower(allDags[j].Name)
			}
			// Treat zero time as greater than any other time (push to end)
			if nextRun1.IsZero() {
				return false
			}
			if nextRun2.IsZero() {
				return true
			}

			// Both are non-zero, compare normally
			if ascending {
				return nextRun1.Before(nextRun2)
			}
			return nextRun2.Before(nextRun1)
		})
	default:
		// Default to sorting by name (includes "name" and empty sort field)
		sort.Slice(allDags, func(i, j int) bool {
			// Default to ascending order
			ascending := opts.Order != "desc"

			// Always sort by name (case-insensitive)
			if ascending {
				return strings.ToLower(allDags[i].Name) < strings.ToLower(allDags[j].Name)
			}
			return strings.ToLower(allDags[i].Name) > strings.ToLower(allDags[j].Name)
		})
	}

	totalCount := len(allDags)

	// Apply pagination
	var paginatedDags []*core.DAG
	start := opts.Paginator.Offset()
	end := start + opts.Paginator.Limit()

	if start < len(allDags) {
		if end > len(allDags) {
			end = len(allDags)
		}
		paginatedDags = allDags[start:end]
	}

	result := exec.NewPaginatedResult(
		paginatedDags, totalCount, *opts.Paginator,
	)

	return result, errList, nil
}

func dagSearchPattern(query string) string {
	return fmt.Sprintf("(?i)%s", regexp.QuoteMeta(query))
}

type dagSearchCursor struct {
	Version  int    `json:"v"`
	Query    string `json:"q"`
	Labels   string `json:"labels,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

type dagMatchCursor struct {
	Version  int    `json:"v"`
	Query    string `json:"q"`
	Labels   string `json:"labels,omitempty"`
	FileName string `json:"fileName"`
	Offset   int    `json:"offset"`
}

func normalizedDAGSearchLabels(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		if trimmed := strings.TrimSpace(strings.ToLower(label)); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	sort.Strings(normalized)
	return strings.Join(normalized, ",")
}

func dagSearchScopeKey(labels []string, filter *exec.WorkspaceFilter) string {
	parts := []string{normalizedDAGSearchLabels(labels)}
	if filter != nil && filter.Enabled {
		workspaces := append([]string(nil), filter.Workspaces...)
		sort.Strings(workspaces)
		parts = append(parts, strings.Join(workspaces, ","))
		if filter.IncludeUnlabelled {
			parts = append(parts, "unlabelled")
		}
	}
	return strings.Join(parts, "|")
}

func decodeDAGSearchCursor(raw, query, labels string) (dagSearchCursor, error) {
	if raw == "" {
		return dagSearchCursor{}, nil
	}
	var cursor dagSearchCursor
	if err := exec.DecodeSearchCursor(raw, &cursor); err != nil {
		return dagSearchCursor{}, err
	}
	if cursor.Version != dagSearchCursorVersion || cursor.Query != query || cursor.Labels != labels {
		return dagSearchCursor{}, exec.ErrInvalidCursor
	}
	return cursor, nil
}

func decodeDAGMatchCursor(raw, query, labels, fileName string) (dagMatchCursor, error) {
	if raw == "" {
		return dagMatchCursor{FileName: fileName}, nil
	}
	var cursor dagMatchCursor
	if err := exec.DecodeSearchCursor(raw, &cursor); err != nil {
		return dagMatchCursor{}, err
	}
	if cursor.Version != dagSearchCursorVersion || cursor.Query != query || cursor.Labels != labels || cursor.FileName != fileName || cursor.Offset < 0 {
		return dagMatchCursor{}, exec.ErrInvalidCursor
	}
	return cursor, nil
}

// Grep searches for a pattern in all DAGs.
func (store *Storage) Grep(ctx context.Context, pattern string) (
	ret []*exec.GrepDAGsResult, errs []string, err error,
) {
	if pattern == "" {
		// return empty result if pattern is empty
		return nil, nil, nil
	}
	if err = store.ensureDirExist(); err != nil {
		errs = append(
			errs, fmt.Sprintf("failed to create DAGs directory %s", store.baseDir),
		)
		return
	}

	entries, err := os.ReadDir(store.baseDir)
	if err != nil {
		logger.Error(ctx, "Failed to read directory",
			tag.Dir(store.baseDir),
			tag.Error(err))
	}

	for _, entry := range entries {
		if fileutil.IsYAMLFile(entry.Name()) {
			filePath := filepath.Join(store.baseDir, entry.Name())
			dat, err := os.ReadFile(filePath) //nolint:gosec
			if err != nil {
				logger.Error(ctx, "Failed to read DAG file",
					tag.File(entry.Name()),
					tag.Error(err))
				continue
			}
			matches, err := grep.Grep(dat, fmt.Sprintf("(?i)%s", pattern), grep.DefaultGrepOptions)
			if err != nil {
				if errors.Is(err, grep.ErrNoMatch) {
					continue
				}
				errs = append(errs, fmt.Sprintf("grep %s failed: %s", entry.Name(), err))
				continue
			}
			dag, err := spec.Load(ctx, filePath, store.defaultLoadOptions(
				spec.OnlyMetadata(),
				spec.WithoutEval(),
				spec.SkipSchemaValidation(),
			)...)
			if err != nil {
				errs = append(errs, fmt.Sprintf("check %s failed: %s", entry.Name(), err))
				continue
			}
			ret = append(ret, &exec.GrepDAGsResult{
				Name:    strings.TrimSuffix(entry.Name(), path.Ext(entry.Name())),
				DAG:     dag,
				Matches: matches,
			})
		}
	}
	return ret, errs, nil
}

// SearchCursor returns lightweight, cursor-based DAG search hits.
func (store *Storage) SearchCursor(ctx context.Context, opts exec.SearchDAGsOptions) (
	*exec.CursorResult[exec.SearchDAGResult], []string, error,
) {
	if opts.Query == "" {
		return &exec.CursorResult[exec.SearchDAGResult]{Items: []exec.SearchDAGResult{}}, nil, nil
	}
	if err := store.ensureDirExist(); err != nil {
		return nil, nil, fmt.Errorf("failed to create DAGs directory %s: %w", store.baseDir, err)
	}

	labelsKey := dagSearchScopeKey(opts.Labels, opts.WorkspaceFilter)
	cursor, err := decodeDAGSearchCursor(opts.Cursor, opts.Query, labelsKey)
	if err != nil {
		return nil, nil, err
	}

	entries, err := os.ReadDir(store.baseDir)
	if err != nil {
		logger.Error(ctx, "Failed to read directory", tag.Dir(store.baseDir), tag.Error(err))
		return nil, nil, fmt.Errorf("failed to read DAGs directory %s: %w", store.baseDir, err)
	}

	limit := max(opts.Limit, 1)
	matchLimit := max(opts.MatchLimit, 1)
	results := make([]exec.SearchDAGResult, 0, limit)
	pattern := dagSearchPattern(opts.Query)
	var errs []string
	var hasMore bool
	var nextCursor string

	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, errs, ctx.Err()
		}
		if !fileutil.IsYAMLFile(entry.Name()) {
			continue
		}

		fileName := strings.TrimSuffix(entry.Name(), path.Ext(entry.Name()))
		if cursor.FileName != "" && fileName <= cursor.FileName {
			continue
		}

		filePath := filepath.Join(store.baseDir, entry.Name())
		var dag *core.DAG
		if len(opts.Labels) > 0 || opts.WorkspaceFilter != nil {
			dag, err = spec.Load(ctx, filePath, store.defaultLoadOptions(
				spec.OnlyMetadata(),
				spec.WithoutEval(),
				spec.SkipSchemaValidation(),
				spec.WithAllowBuildErrors(),
			)...)
			if err != nil {
				errs = append(errs, fmt.Sprintf("reading %s failed: %s", fileName, err))
				continue
			}
			if !containsAllLabels(dag.Labels, opts.Labels) {
				continue
			}
			if !opts.WorkspaceFilter.MatchesLabels(dag.Labels) {
				continue
			}
		}
		dat, err := os.ReadFile(filePath) //nolint:gosec
		if err != nil {
			logger.Error(ctx, "Failed to read DAG file", tag.File(entry.Name()), tag.Error(err))
			errs = append(errs, fmt.Sprintf("read %s failed: %s", entry.Name(), err))
			continue
		}

		window, err := grep.GrepWindow(dat, pattern, grep.GrepOptions{
			IsRegexp: true,
			Before:   grep.DefaultGrepOptions.Before,
			After:    grep.DefaultGrepOptions.After,
			Limit:    matchLimit,
		})
		if err != nil {
			if errors.Is(err, grep.ErrNoMatch) {
				continue
			}
			errs = append(errs, fmt.Sprintf("grep %s failed: %s", entry.Name(), err))
			continue
		}

		workspaceName := ""
		if dag == nil {
			dag, err = spec.Load(ctx, filePath, store.defaultLoadOptions(
				spec.OnlyMetadata(),
				spec.WithoutEval(),
				spec.SkipSchemaValidation(),
				spec.WithAllowBuildErrors(),
			)...)
			if err != nil {
				errs = append(errs, fmt.Sprintf("reading %s metadata failed: %s", fileName, err))
			}
		}
		if dag != nil {
			if name, ok := exec.WorkspaceNameFromLabels(dag.Labels); ok {
				workspaceName = name
			}
		}

		if len(results) == limit {
			hasMore = true
			nextCursor = exec.EncodeSearchCursor(dagSearchCursor{
				Version:  dagSearchCursorVersion,
				Query:    opts.Query,
				Labels:   labelsKey,
				FileName: results[len(results)-1].FileName,
			})
			break
		}

		item := exec.SearchDAGResult{
			Name:           fileName,
			FileName:       fileName,
			Workspace:      workspaceName,
			Matches:        window.Matches,
			HasMoreMatches: window.HasMore,
		}
		if window.HasMore {
			item.NextMatchesCursor = exec.EncodeSearchCursor(dagMatchCursor{
				Version:  dagSearchCursorVersion,
				Query:    opts.Query,
				Labels:   labelsKey,
				FileName: fileName,
				Offset:   window.NextOffset,
			})
		}
		results = append(results, item)
	}

	return &exec.CursorResult[exec.SearchDAGResult]{
		Items:      results,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, errs, nil
}

// SearchMatches returns cursor-based snippets for one DAG definition.
func (store *Storage) SearchMatches(ctx context.Context, fileName string, opts exec.SearchDAGMatchesOptions) (
	*exec.CursorResult[*exec.Match], error,
) {
	if opts.Query == "" {
		return &exec.CursorResult[*exec.Match]{Items: []*exec.Match{}}, nil
	}

	labelsKey := dagSearchScopeKey(opts.Labels, opts.WorkspaceFilter)
	cursor, err := decodeDAGMatchCursor(opts.Cursor, opts.Query, labelsKey, fileName)
	if err != nil {
		return nil, err
	}

	filePath, err := store.locateDAG(fileName)
	if err != nil {
		return nil, exec.ErrDAGNotFound
	}

	dat, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, exec.ErrDAGNotFound
		}
		return nil, err
	}
	if len(opts.Labels) > 0 || opts.WorkspaceFilter != nil {
		dag, err := spec.Load(ctx, filePath, store.defaultLoadOptions(
			spec.OnlyMetadata(),
			spec.WithoutEval(),
			spec.SkipSchemaValidation(),
			spec.WithAllowBuildErrors(),
		)...)
		if err != nil {
			return nil, err
		}
		if !containsAllLabels(dag.Labels, opts.Labels) {
			return &exec.CursorResult[*exec.Match]{Items: []*exec.Match{}}, nil
		}
		if !opts.WorkspaceFilter.MatchesLabels(dag.Labels) {
			return &exec.CursorResult[*exec.Match]{Items: []*exec.Match{}}, nil
		}
	}

	window, err := grep.GrepWindow(dat, dagSearchPattern(opts.Query), grep.GrepOptions{
		IsRegexp: true,
		Before:   grep.DefaultGrepOptions.Before,
		After:    grep.DefaultGrepOptions.After,
		Offset:   cursor.Offset,
		Limit:    max(opts.Limit, 1),
	})
	if err != nil {
		if errors.Is(err, grep.ErrNoMatch) {
			return &exec.CursorResult[*exec.Match]{Items: []*exec.Match{}}, nil
		}
		return nil, err
	}

	result := &exec.CursorResult[*exec.Match]{
		Items:   window.Matches,
		HasMore: window.HasMore,
	}
	if window.HasMore {
		result.NextCursor = exec.EncodeSearchCursor(dagMatchCursor{
			Version:  dagSearchCursorVersion,
			Query:    opts.Query,
			Labels:   labelsKey,
			FileName: fileName,
			Offset:   window.NextOffset,
		})
	}
	return result, nil
}

// ToggleSuspend toggles the suspension state of a DAG.
func (store *Storage) ToggleSuspend(ctx context.Context, id string, suspend bool) error {
	var err error
	if suspend {
		err = store.createFlag(fileName(id))
	} else if store.IsSuspended(ctx, id) {
		err = store.deleteFlag(fileName(id))
	}
	if err == nil {
		store.invalidateIndex()
	}
	return err
}

// IsSuspended checks if a DAG is suspended.
func (store *Storage) IsSuspended(_ context.Context, id string) bool {
	return store.flagExists(fileName(id))
}

func fileName(id string) string {
	return dagindex.SuspendFlagName(id)
}

// Rename renames a DAG from oldID to newID.
func (store *Storage) Rename(_ context.Context, oldID, newID string) error {
	oldFilePath, err := store.locateDAG(oldID)
	if err != nil {
		return fmt.Errorf("failed to locate DAG %s: %w", oldID, err)
	}
	newFilePath := store.generateFilePath(newID)
	if fileExists(newFilePath) {
		return exec.ErrDAGAlreadyExists
	}
	if err := os.Rename(oldFilePath, newFilePath); err != nil {
		return err
	}
	store.invalidateIndex()
	return nil
}

// generateFilePath generates the file path for a DAG by its name.
// It uses filepath.Base to strip directory components and verifies
// the result stays inside baseDir to prevent path traversal.
func (store *Storage) generateFilePath(name string) string {
	safeName := filepath.Base(name)
	filePath := fileutil.EnsureYAMLExtension(path.Join(store.baseDir, safeName))
	filePath = filepath.Clean(filePath)
	// Verify the resolved path is inside baseDir.
	basePrefix := filepath.Clean(store.baseDir) + string(filepath.Separator)
	if !strings.HasPrefix(filePath, basePrefix) {
		return filepath.Join(store.baseDir, "_invalid.yaml")
	}
	return filePath
}

// locateDAG locates the DAG file by its name or path.
func (store *Storage) locateDAG(nameOrPath string) (string, error) {
	if strings.Contains(nameOrPath, string(filepath.Separator)) {
		foundPath, err := findDAGFile(nameOrPath)
		if err == nil {
			return foundPath, nil
		}
	}

	for _, dir := range store.searchPaths {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		candidatePath := filepath.Join(absDir, nameOrPath)
		foundPath, err := findDAGFile(candidatePath)
		if err == nil {
			return foundPath, nil
		}
	}

	// DAG not found
	return "", fmt.Errorf("DAG %s not found: %w", nameOrPath, os.ErrNotExist)
}

// LabelList lists all unique labels from the DAGs.
func (store *Storage) LabelList(ctx context.Context) ([]string, []string, error) {
	var (
		errList  []string
		labelSet = make(map[string]struct{})
	)

	// Try index-accelerated path.
	if indexEntries := store.loadOrRebuildIndex(ctx); indexEntries != nil {
		for _, entry := range indexEntries {
			if entry.LoadError != "" {
				continue
			}
			for _, labelStr := range entry.Labels {
				labelSet[strings.ToLower(labelStr)] = struct{}{}
				if key, _, ok := strings.Cut(labelStr, "="); ok {
					labelSet[strings.ToLower(key)] = struct{}{}
				}
			}
		}
	} else {
		// Fallback: direct filesystem scan.
		entries, err := os.ReadDir(store.baseDir)
		if err != nil {
			errList = append(errList, fmt.Sprintf("failed to read directory %s: %s", store.baseDir, err))
			return nil, errList, err
		}

		for _, entry := range entries {
			if entry.IsDir() || !fileutil.IsYAMLFile(entry.Name()) {
				continue
			}

			baseName := path.Base(entry.Name())
			dagName := strings.TrimSuffix(baseName, path.Ext(baseName))

			parsedDAG, err := store.GetMetadata(ctx, dagName)
			if err != nil {
				errList = append(errList, fmt.Sprintf("reading %s failed: %s", entry.Name(), err))
				continue
			}

			for _, t := range parsedDAG.Labels {
				labelStr := t.String()
				labelSet[strings.ToLower(labelStr)] = struct{}{}
				if t.Value != "" {
					labelSet[strings.ToLower(t.Key)] = struct{}{}
				}
			}
		}
	}

	labelList := make([]string, 0, len(labelSet))
	for t := range labelSet {
		labelList = append(labelList, t)
	}
	sort.Strings(labelList)
	return labelList, errList, nil
}

// CreateFlag creates the given file.
func (store *Storage) createFlag(file string) error {
	_ = os.MkdirAll(store.flagsBaseDir, flagPermission)
	return os.WriteFile(path.Join(store.flagsBaseDir, file), []byte{}, flagPermission)
}

// flagExists returns true if the given file exists.
func (store *Storage) flagExists(file string) bool {
	_ = os.MkdirAll(store.flagsBaseDir, flagPermission)
	_, err := os.Stat(path.Join(store.flagsBaseDir, file))
	return err == nil
}

// deleteFlag deletes the given file.
func (store *Storage) deleteFlag(file string) error {
	_ = os.MkdirAll(store.flagsBaseDir, flagPermission)
	return os.Remove(path.Join(store.flagsBaseDir, file))
}

// flagPermission is the default file permission for newly created files.
var flagPermission os.FileMode = 0750

// containsSearchText checks if the text contains the search string (case-insensitive).
func containsSearchText(text string, search string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(search))
}

func matchesDAGListSearch(dagName, fileName, search string) bool {
	return containsSearchText(dagName, search) || containsSearchText(fileName, search)
}

// containsAllLabels checks if the DAG labels match all filter labels (AND logic).
// Supports key-only filters ("env"), exact filters ("env=prod"), and negation ("!deprecated").
func containsAllLabels(dagLabels core.Labels, filterLabels []string) bool {
	if len(filterLabels) == 0 {
		return true
	}

	filters := make([]core.LabelFilter, 0, len(filterLabels))
	for _, f := range filterLabels {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			filters = append(filters, core.ParseLabelFilter(trimmed))
		}
	}

	return dagLabels.MatchesFilters(filters)
}

// fileExists checks if a file exists.
func fileExists(file string) bool {
	_, err := os.Stat(file)
	return !os.IsNotExist(err)
}

// shouldCreateExamples checks if we should create example DAGs
func shouldCreateExamples(dir string) bool {
	// Check for marker file that indicates examples were already created
	markerFile := filepath.Join(dir, ".examples-created")
	if fileExists(markerFile) {
		return false
	}

	// Check if directory is empty (no YAML files)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if !entry.IsDir() && fileutil.IsYAMLFile(entry.Name()) {
			// Found at least one YAML file, don't create examples
			return false
		}
	}

	return true
}

// createExampleDAGs creates example DAG files for first-time users
func (store *Storage) createExampleDAGs() error {
	// Check if we should skip example creation
	if store.skipExamples {
		return nil
	}

	logger.Info(context.Background(), "Creating example DAGs for first-time users",
		tag.Dir(store.baseDir))

	// Create each example DAG
	for filename, content := range exampleDAGs {
		filePath := filepath.Join(store.baseDir, filename)
		if err := os.WriteFile(filePath, []byte(content), defaultPerm); err != nil {
			logger.Error(context.Background(), "Failed to create example DAG",
				tag.File(filename),
				tag.Error(err))
			// Continue creating other examples even if one fails
		}
	}

	// Create marker file to indicate examples were created
	markerFile := filepath.Join(store.baseDir, ".examples-created")
	markerContent := []byte("# This file indicates that example DAGs have been created.\n# Delete this file to re-create examples on next startup.\n")
	if err := os.WriteFile(markerFile, markerContent, defaultPerm); err != nil {
		logger.Error(context.Background(), "Failed to create examples marker file",
			tag.Error(err))
	}

	logger.Info(context.Background(), "Example DAGs created successfully. Check the web UI to explore them!")
	return nil
}

// findDAGFile finds the DAG file with the given file name.
func findDAGFile(name string) (string, error) {
	ext := path.Ext(name)
	switch ext {
	case ".yaml", ".yml":
		if fileutil.FileExists(name) {
			return filepath.Abs(name)
		}
	default:
		// try all supported extensions
		for _, ext := range fileutil.ValidYAMLExtensions {
			if fileutil.FileExists(name + ext) {
				return filepath.Abs(name + ext)
			}
		}
	}
	return "", fmt.Errorf("file %s not found: %w", name, os.ErrNotExist)
}
