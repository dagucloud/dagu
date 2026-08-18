// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
)

var _ persis.DAGDefinitionStore = (*Store)(nil)

// Option configures filesystem DAG definition storage.
type Option func(*Options)

// Options contains filesystem DAG definition storage settings.
type Options struct {
	FlagsBaseDir           string                   // Base directory for flag store
	FileCache              *fileutil.Cache[*ir.DAG] // Optional cache for DAG objects
	SearchPaths            []string                 // Additional search paths for DAG files
	BaseConfigPath         string                   // Optional base config file applied when loading DAGs
	WorkspaceBaseConfigDir string                   // Optional directory containing workspace base configs
	SkipExamples           bool                     // Skip creating example DAGs
	Recursive              bool                     // Discover DAG definitions in subdirectories
	Symlinks               bool                     // Include recursive file symlinks and external targets
	SkipDirectoryCreation  bool                     // Skip creating base directory for execution-scoped stores
}

// WithRecursiveDiscovery controls whether DAG files are discovered recursively.
func WithRecursiveDiscovery(recursive bool) Option {
	return func(o *Options) {
		o.Recursive = recursive
	}
}

// WithSymlinks includes file symlinks in recursive discovery and permits external targets.
func WithSymlinks(enabled bool) Option {
	return func(o *Options) {
		o.Symlinks = enabled
	}
}

// WithFileCache sets the file cache for DAG objects.
func WithFileCache(cache *fileutil.Cache[*ir.DAG]) Option {
	return func(o *Options) {
		o.FileCache = cache
	}
}

// WithFlagsBaseDir sets the suspension flag directory.
func WithFlagsBaseDir(dir string) Option {
	return func(o *Options) {
		o.FlagsBaseDir = dir
	}
}

// WithSearchPaths sets additional search paths for DAG files.
func WithSearchPaths(paths []string) Option {
	return func(o *Options) {
		o.SearchPaths = paths
	}
}

// WithBaseConfig sets the base config used when loading DAGs.
func WithBaseConfig(path string) Option {
	return func(o *Options) {
		o.BaseConfigPath = path
	}
}

// WithWorkspaceBaseConfigDir sets the directory containing workspace base configs.
func WithWorkspaceBaseConfigDir(dir string) Option {
	return func(o *Options) {
		o.WorkspaceBaseConfigDir = dir
	}
}

// WithSkipExamples controls example DAG creation.
func WithSkipExamples(skip bool) Option {
	return func(o *Options) {
		o.SkipExamples = skip
	}
}

// WithSkipDirectoryCreation controls base directory creation.
func WithSkipDirectoryCreation(skip bool) Option {
	return func(o *Options) {
		o.SkipDirectoryCreation = skip
	}
}

// NewStore creates a filesystem-backed DAG definition store.
func NewStore(baseDir string, opts ...Option) *Store {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}
	if options.FlagsBaseDir == "" {
		options.FlagsBaseDir = filepath.Join(baseDir, "flags")
	}
	// Build search paths in deterministic order: baseDir first, then additional paths.
	seen := make(map[string]struct{})
	var searchPaths []string
	for _, p := range append([]string{baseDir}, options.SearchPaths...) {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		searchPaths = append(searchPaths, p)
	}

	return &Store{
		baseDir:                baseDir,
		flagsBaseDir:           options.FlagsBaseDir,
		fileCache:              options.FileCache,
		searchPaths:            searchPaths,
		baseConfigPath:         options.BaseConfigPath,
		workspaceBaseConfigDir: options.WorkspaceBaseConfigDir,
		baseConfigState:        describeBaseConfigStateSet(options.BaseConfigPath, options.WorkspaceBaseConfigDir),
		skipExamples:           options.SkipExamples,
		recursive:              options.Recursive,
		symlinks:               options.Symlinks,
		skipDirectoryCreation:  options.SkipDirectoryCreation,
	}
}

// Store persists DAG definitions in local files.
type Store struct {
	baseDir                string                   // Base directory for DAG storage
	flagsBaseDir           string                   // Base directory for flag store
	fileCache              *fileutil.Cache[*ir.DAG] // Optional cache for DAG objects
	searchPaths            []string                 // Additional search paths for DAG files
	baseConfigPath         string                   // Optional base config file applied when loading DAGs
	workspaceBaseConfigDir string                   // Optional directory containing workspace base configs
	baseConfigState        string                   // Last observed base config state for cache/index invalidation
	skipExamples           bool                     // Skip creating example DAGs
	recursive              bool                     // Discover DAG definitions in subdirectories
	symlinks               bool                     // Include recursive file symlinks and external targets
	skipDirectoryCreation  bool                     // Skip creating base directory for execution-scoped stores
	baseConfigMu           sync.Mutex               // Protects base config state refresh and invalidation
	indexMu                sync.Mutex               // Protects index load/rebuild/invalidate
}

func (store *Store) Get(ctx context.Context, id string) (persis.DAGDefinition, error) {
	resolved, err := store.locateDAG(ctx, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persis.DAGDefinition{}, persis.ErrDAGNotFound
		}
		return persis.DAGDefinition{}, err
	}
	source, err := fileutil.ReadFile(resolved.ResolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persis.DAGDefinition{}, persis.ErrDAGNotFound
		}
		return persis.DAGDefinition{}, err
	}
	entryName := fileutil.TrimYAMLFileExtension(filepath.Base(resolved.EntryPath))
	return persis.DAGDefinition{ID: entryName, Source: source, SourcePath: resolved.ResolvedPath}, nil
}

func (store *Store) Catalog(ctx context.Context) (persis.DAGCatalog, error) {
	return store.catalog(ctx, false)
}

// CatalogIncludingSearchPaths is like Catalog but also includes DAG definitions
// found under the store's additional search paths (for example alt_dags_dir).
// Entries are deduplicated and conflict-checked together with the primary
// directory's entries.
func (store *Store) CatalogIncludingSearchPaths(ctx context.Context) (persis.DAGCatalog, error) {
	return store.catalog(ctx, true)
}

func (store *Store) catalog(ctx context.Context, includeSearchPaths bool) (persis.DAGCatalog, error) {
	catalog, err := store.loadCatalog(ctx, includeSearchPaths)
	if err != nil {
		return persis.DAGCatalog{}, fmt.Errorf("failed to read DAGs directory %s: %w", store.baseDir, err)
	}
	result := persis.DAGCatalog{
		Items:  make([]persis.DAGListItem, 0, len(catalog.entries)),
		Issues: append([]string(nil), catalog.errors...),
	}
	for _, entry := range catalog.entries {
		result.Items = append(result.Items, persis.DAGListItem{
			ID:        entryStem(entry),
			DAG:       dagindex.DAGFromEntry(entry, store.baseDir),
			Suspended: entry.Suspended,
		})
	}
	return result, nil
}

func (store *Store) SetSuspended(_ context.Context, id string, suspended bool) error {
	var err error
	if suspended {
		err = store.createFlag(fileName(id))
	} else {
		err = store.deleteFlag(fileName(id))
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	}
	if err == nil {
		store.invalidateIndex()
	}
	return err
}

func (store *Store) IsSuspended(_ context.Context, id string) (bool, error) {
	return store.flagExistsResult(fileName(id))
}

func (store *Store) readSuspendFlags(ctx context.Context) (dagindex.SuspendFlags, error) {
	flags := make(dagindex.SuspendFlags)
	flagEntries, err := os.ReadDir(store.flagsBaseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exists, statErr := store.suspendFlagsDirExists()
			if statErr != nil {
				return nil, statErr
			}
			if !exists {
				logger.Debug(ctx, "Suspend flags directory does not exist", tag.Dir(store.flagsBaseDir))
				return flags, nil
			}
		}
		return nil, fmt.Errorf("read suspend flags directory %s: %w", store.flagsBaseDir, err)
	}
	for _, fe := range flagEntries {
		if !fe.IsDir() {
			flags[fe.Name()] = struct{}{}
		}
	}
	return flags, nil
}

func (store *Store) defaultLoadOptions(opts ...spec.LoadOption) []spec.LoadOption {
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

func (store *Store) refreshBaseConfigState() {
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
func (store *Store) Initialize() error {
	return store.ensureDirExist()
}

// GetMetadata retrieves the metadata of a DAG by its name.
func (store *Store) GetMetadata(ctx context.Context, name string) (*ir.DAG, error) {
	resolved, err := store.locateDAG(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to locate DAG %s in search paths (%v): %w", name, store.searchPaths, err)
	}
	store.refreshBaseConfigState()
	loadOpts := store.defaultLoadOptions(
		spec.WithDefaultName(fileutil.TrimYAMLFileExtension(filepath.Base(resolved.EntryPath))),
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
	if store.fileCache == nil {
		return spec.Load(ctx, resolved.ResolvedPath, loadOpts...)
	}
	return store.fileCache.LoadLatestByKey(metadataCacheKey(resolved), resolved.ResolvedPath, func() (*ir.DAG, error) {
		return spec.Load(ctx, resolved.ResolvedPath, loadOpts...)
	})
}

func metadataCacheKey(resolved ResolvedFile) string {
	return resolved.EntryPath + "\x00" + resolved.ResolvedPath
}

// FileMode used for newly created DAG files
const defaultPerm os.FileMode = 0600

func (store *Store) Update(ctx context.Context, name string, yamlSpec []byte) error {
	resolved, err := store.locateDAG(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to locate DAG %s: %w", name, err)
	}
	if resolved.ExternalSymlink {
		return fmt.Errorf("%w: external DAG file symlink", persis.ErrDAGReadOnly)
	}
	if err := fileutil.WriteFileAtomic(resolved.ResolvedPath, yamlSpec, defaultPerm); err != nil {
		return err
	}
	if store.fileCache != nil {
		store.fileCache.Invalidate(metadataCacheKey(resolved))
	}
	store.invalidateIndex()
	return nil
}

// Create creates a new DAG with the given name and specification.
func (store *Store) Create(_ context.Context, name string, spec []byte) error {
	if err := store.ensureDirExist(); err != nil {
		return fmt.Errorf("failed to create DAGs directory %s: %w", store.baseDir, err)
	}
	filePath := store.generateFilePath(name)
	if fileExists(filePath) || store.recursive && store.stemExists(name, "") {
		return persis.ErrDAGAlreadyExists
	}
	if err := fileutil.WriteFileAtomic(filePath, spec, defaultPerm); err != nil {
		return fmt.Errorf("failed to write DAG %s: %w", name, err)
	}
	store.invalidateIndex()
	return nil
}

// Delete deletes a DAG by its name.
func (store *Store) Delete(ctx context.Context, name string) error {
	resolved, err := store.locateDAG(ctx, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to locate DAG %s: %w", name, err)
	}
	if resolved.ExternalSymlink {
		return fmt.Errorf("%w: external DAG file symlink", persis.ErrDAGReadOnly)
	}
	if err := fileutil.Remove(resolved.EntryPath); err != nil {
		return err
	}
	if store.fileCache != nil {
		store.fileCache.Invalidate(metadataCacheKey(resolved))
	}
	store.invalidateIndex()
	return nil
}

// ensureDirExist ensures that the base directory exists.
func (store *Store) ensureDirExist() error {
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
		if shouldCreateExamples(store.baseDir, store.recursive, store.symlinks) {
			_ = store.createExampleDAGs() // Errors are logged internally
		}
	}
	return nil
}

// loadIndex returns validated index entries, rebuilding the index when needed.
func (store *Store) loadIndex(ctx context.Context, files []DiscoveredFile) ([]*indexv1.DAGIndexEntry, error) {
	store.refreshBaseConfigState()

	store.indexMu.Lock()
	defer store.indexMu.Unlock()

	yamlFiles := make([]dagindex.YAMLFileMeta, 0, len(files))
	for _, file := range files {
		yamlFiles = append(yamlFiles, dagindex.YAMLFileMeta{
			Name:     file.RelPath,
			LoadPath: file.ResolvedPath,
			Size:     file.Size,
			ModTime:  file.ModTime,
		})
	}

	// Build suspend flags set.
	flags, err := store.readSuspendFlags(ctx)
	if err != nil {
		return nil, err
	}

	indexPath := filepath.Join(store.baseDir, dagindex.IndexFileName)

	// Try loading existing index. A cached load error is re-checked against the
	// current parser before it is served, so upgrading Dagu clears errors that
	// only an older build produced.
	if cached := dagindex.Load(indexPath, yamlFiles, flags); cached != nil {
		if dagindex.RefreshFailures(ctx, store.baseDir, yamlFiles, cached, flags, store.defaultLoadOptions()...) {
			if err := dagindex.Write(indexPath, dagindex.NewIndex(cached)); err != nil {
				logger.Warn(ctx, "Failed to write DAG definition index", tag.Error(err))
			}
		}
		return cached, nil
	}

	// Rebuild.
	logger.Info(ctx, "Rebuilding DAG definition index", tag.Dir(store.baseDir))
	idx := dagindex.Build(ctx, store.baseDir, yamlFiles, flags, store.defaultLoadOptions()...)
	if err := dagindex.Write(indexPath, idx); err != nil {
		logger.Warn(ctx, "Failed to write DAG definition index", tag.Error(err))
	}
	return idx.Entries, nil
}

// invalidateIndex removes the index file so the next read triggers a rebuild.
func (store *Store) invalidateIndex() {
	store.indexMu.Lock()
	defer store.indexMu.Unlock()
	_ = fileutil.Remove(filepath.Join(store.baseDir, dagindex.IndexFileName))
}

func fileName(id string) string {
	return dagindex.SuspendFlagName(id)
}

// Rename renames a DAG from oldID to newID.
func (store *Store) Rename(ctx context.Context, oldID, newID string) error {
	resolved, err := store.locateDAG(ctx, oldID)
	if err != nil {
		return fmt.Errorf("failed to locate DAG %s: %w", oldID, err)
	}
	if resolved.ExternalSymlink {
		return fmt.Errorf("%w: external DAG file symlink", persis.ErrDAGReadOnly)
	}
	oldFilePath := resolved.EntryPath
	newFilePath := store.generateFilePath(newID)
	exceptPath := ""
	if store.recursive {
		if relPath, ok := relativeToBase(store.baseDir, oldFilePath); ok {
			newFilePath = fileutil.EnsureYAMLExtension(filepath.Join(filepath.Dir(oldFilePath), filepath.Base(newID)))
			exceptPath = relPath
		}
	}
	if fileExists(newFilePath) || store.recursive && store.stemExists(newID, exceptPath) {
		return persis.ErrDAGAlreadyExists
	}
	if err := fileutil.Rename(oldFilePath, newFilePath); err != nil {
		return err
	}
	store.invalidateIndex()
	return nil
}

// generateFilePath generates the file path for a DAG by its name.
// It uses filepath.Base to strip directory components and verifies
// the result stays inside baseDir to prevent path traversal.
func (store *Store) generateFilePath(name string) string {
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

// locateDAG resolves a DAG beneath the configured DAG directories.
func (store *Store) locateDAG(ctx context.Context, nameOrPath string) (ResolvedFile, error) {
	relativePath := filepath.FromSlash(nameOrPath)
	explicitPath := filepath.IsAbs(relativePath) || strings.ContainsAny(nameOrPath, `/\`)

	if store.recursive && !explicitPath {
		catalog, err := store.loadCatalog(ctx, false)
		if err != nil {
			return ResolvedFile{}, fmt.Errorf("failed to discover DAGs: %w", err)
		}
		fileName := fileutil.TrimYAMLFileExtension(filepath.Base(relativePath))
		if entry, ok := catalog.byStem[fileName]; ok {
			resolved, err := ResolveFile(
				store.baseDir,
				filepath.FromSlash(entry.FilePath),
			)
			if err == nil && (!resolved.ExternalSymlink || store.symlinks) {
				return resolved, nil
			}
		}
		if len(store.searchPaths) > 1 {
			return locateDAGInDirectories(nameOrPath, store.searchPaths[1:], store.symlinks)
		}
		return ResolvedFile{}, fmt.Errorf("DAG %s not found: %w", nameOrPath, os.ErrNotExist)
	}

	return locateDAGInDirectories(nameOrPath, store.searchPaths, store.symlinks)
}

func locateDAGInDirectories(
	nameOrPath string,
	directories []string,
	symlinks bool,
) (ResolvedFile, error) {
	var externalSymlinkDisabled bool
	for _, dir := range directories {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}

		relativePath := filepath.FromSlash(nameOrPath)
		if filepath.IsAbs(relativePath) {
			relativePath, err = filepath.Rel(absDir, relativePath)
			if err != nil {
				continue
			}
		}

		for _, candidatePath := range dagFileCandidates(relativePath) {
			resolved, err := ResolveFile(absDir, candidatePath)
			if err != nil {
				continue
			}
			if resolved.ExternalSymlink && !symlinks {
				externalSymlinkDisabled = true
				continue
			}
			return resolved, nil
		}
	}

	if externalSymlinkDisabled {
		return ResolvedFile{}, fmt.Errorf(
			"DAG %s uses an external file symlink; enable dag_discovery.symlinks: %w",
			nameOrPath,
			errors.Join(ErrExternalSymlinkDisabled, os.ErrNotExist),
		)
	}

	// DAG not found
	return ResolvedFile{}, fmt.Errorf("DAG %s not found: %w", nameOrPath, os.ErrNotExist)
}

func (store *Store) stemExists(name, exceptPath string) bool {
	scan, err := Discover(store.baseDir, DiscoveryOptions{
		Recursive: true,
		Symlinks:  store.symlinks,
	})
	if err != nil {
		return false
	}
	base := filepath.Base(filepath.FromSlash(name))
	target := fileutil.TrimYAMLFileExtension(base)
	for _, file := range scan.Files {
		if exceptPath != "" && file.RelPath == exceptPath {
			continue
		}
		fileBase := filepath.Base(filepath.FromSlash(file.RelPath))
		if fileutil.TrimYAMLFileExtension(fileBase) == target {
			return true
		}
	}
	return false
}

func relativeToBase(baseDir, path string) (string, bool) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", false
	}
	absBase, err = filepath.EvalSymlinks(absBase)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	absPath, err = filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func dagFileCandidates(name string) []string {
	switch filepath.Ext(name) {
	case ".yaml", ".yml":
		return []string{name}
	default:
		return []string{name + ".yaml", name + ".yml"}
	}
}

// CreateFlag creates the given file.
func (store *Store) createFlag(file string) error {
	if err := os.MkdirAll(store.flagsBaseDir, flagPermission); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path.Join(store.flagsBaseDir, file), []byte{}, flagPermission)
}

func (store *Store) flagExistsResult(file string) (bool, error) {
	_, err := os.Stat(path.Join(store.flagsBaseDir, file))
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	_, err = store.suspendFlagsDirExists()
	return false, err
}

func (store *Store) suspendFlagsDirExists() (bool, error) {
	info, err := os.Stat(store.flagsBaseDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("suspend flags path %s is not a directory", store.flagsBaseDir)
	}
	return true, nil
}

// deleteFlag deletes the given file.
func (store *Store) deleteFlag(file string) error {
	return fileutil.Remove(path.Join(store.flagsBaseDir, file))
}

// flagPermission is the default file permission for newly created files.
var flagPermission os.FileMode = 0750

// fileExists checks if a file exists.
func fileExists(file string) bool {
	_, err := os.Stat(file)
	return !os.IsNotExist(err)
}

// shouldCreateExamples checks if we should create example DAGs
func shouldCreateExamples(dir string, recursive, symlinks bool) bool {
	// Check for marker file that indicates examples were already created
	markerFile := filepath.Join(dir, ".examples-created")
	if fileExists(markerFile) {
		return false
	}

	scan, err := Discover(dir, DiscoveryOptions{
		Recursive: recursive,
		Symlinks:  symlinks,
	})
	if err != nil {
		return false
	}
	return len(scan.Files) == 0
}

// createExampleDAGs creates example DAG files for first-time users
func (store *Store) createExampleDAGs() error {
	// Check if we should skip example creation
	if store.skipExamples {
		return nil
	}

	logger.Info(context.Background(), "Creating example DAGs for first-time users",
		tag.Dir(store.baseDir))

	// Create each example DAG
	for filename, content := range exampleDAGs {
		filePath := filepath.Join(store.baseDir, filename)
		if err := fileutil.WriteFileAtomic(filePath, []byte(content), defaultPerm); err != nil {
			logger.Error(context.Background(), "Failed to create example DAG",
				tag.File(filename),
				tag.Error(err))
			// Continue creating other examples even if one fails
		}
	}

	// Create marker file to indicate examples were created
	markerFile := filepath.Join(store.baseDir, ".examples-created")
	markerContent := []byte("# This file indicates that example DAGs have been created.\n# Delete this file to re-create examples on next startup.\n")
	if err := fileutil.WriteFileAtomic(markerFile, markerContent, defaultPerm); err != nil {
		logger.Error(context.Background(), "Failed to create examples marker file",
			tag.Error(err))
	}

	logger.Info(context.Background(), "Example DAGs created successfully. Check the web UI to explore them!")
	return nil
}
