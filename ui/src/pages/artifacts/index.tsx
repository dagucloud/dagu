// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from '@/lib/dayjs';
import {
  AlertCircle,
  File,
  FileCode,
  FileImage,
  FileText,
  Folder,
  FolderOpen,
  Link as LinkIcon,
  RefreshCw,
} from 'lucide-react';
import React from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import type {
  ArtifactListItem,
  ArtifactListQuery,
} from '@/features/artifacts/hooks/artifactListPagination';
import { usePaginatedArtifacts } from '@/features/artifacts/hooks/artifactListPagination';
import {
  ARTIFACT_PRESET_ALL,
  artifactsFilterSetFromView,
  buildArtifactViewSpec,
  type ArtifactsFilterSet,
  type ArtifactsFilterView,
} from '@/features/artifacts/lib/artifactViews';
import { ViewSelector } from '@/features/views/ViewSelector';
import {
  viewMatchesScope,
  viewScopeForSelection,
} from '@/features/views/viewScope';
import { useViews, type View } from '@/hooks/useViews';
import { ViewSpecType } from '@/api/v1/schema';
import { ArtifactFilePreview } from '@/features/dags/components/artifacts/ArtifactFilePreview';
import { useArtifactTreeNavigation } from '@/features/dags/components/artifacts/useArtifactTreeNavigation';
import { Button } from '@/components/ui/button';
import { DateRangePicker } from '@/components/ui/date-range-picker';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import Title from '@/components/ui/title';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useCanWriteForWorkspace } from '../../contexts/AuthContext';
import { useConfig } from '../../contexts/ConfigContext';
import { useSearchState } from '../../contexts/SearchStateContext';
import {
  workspaceSelectionKey,
  workspaceSelectionQuery,
} from '../../lib/workspace';
import { cn } from '@/lib/utils';

const ARTIFACT_LIST_LIMIT = 100;
const ALL_ARTIFACTS_VIEW_PARAM = 'all';
const SEARCH_STATE_PAGE_KEY = 'artifacts';

const ARTIFACT_FILTER_QUERY_KEYS = [
  'name',
  'fileName',
  'fromDate',
  'toDate',
  'dateMode',
  'preset',
  'view',
] as const;

const DEFAULT_ARTIFACT_FILTERS: ArtifactsFilterSet = {
  searchText: '',
  fileName: '',
  fromDate: undefined,
  toDate: undefined,
  dateRangeMode: 'preset',
  datePreset: ARTIFACT_PRESET_ALL,
};

const areArtifactFiltersEqual = (
  a: ArtifactsFilterSet,
  b: ArtifactsFilterSet
): boolean =>
  a.searchText === b.searchText &&
  a.fileName === b.fileName &&
  a.fromDate === b.fromDate &&
  a.toDate === b.toDate &&
  a.dateRangeMode === b.dateRangeMode &&
  a.datePreset === b.datePreset;

function artifactsFilterViewFromView(view: View): ArtifactsFilterView {
  return {
    id: view.id,
    name: view.name,
    pinned: view.pinned ?? false,
    filters: artifactsFilterSetFromView(view),
  };
}

function computePresetDates(
  preset: string,
  tzOffsetInSec: number | undefined
): { from: string; to?: string } {
  const now = dayjs();
  const startOfDay =
    tzOffsetInSec !== undefined
      ? now.utcOffset(tzOffsetInSec / 60).startOf('day')
      : now.startOf('day');

  switch (preset) {
    case 'today':
      return { from: startOfDay.format('YYYY-MM-DDTHH:mm') };
    case 'yesterday':
      return {
        from: startOfDay.subtract(1, 'day').format('YYYY-MM-DDTHH:mm'),
        to: startOfDay.format('YYYY-MM-DDTHH:mm'),
      };
    case 'last7days':
      return {
        from: startOfDay.subtract(7, 'day').format('YYYY-MM-DDTHH:mm'),
      };
    case 'last30days':
      return {
        from: startOfDay.subtract(30, 'day').format('YYYY-MM-DDTHH:mm'),
      };
    case 'thisWeek':
      return {
        from: startOfDay.startOf('week').format('YYYY-MM-DDTHH:mm'),
      };
    case 'thisMonth':
      return {
        from: startOfDay.startOf('month').format('YYYY-MM-DDTHH:mm'),
      };
    default:
      return { from: startOfDay.format('YYYY-MM-DDTHH:mm') };
  }
}

// Preset ranges are relative to "now", so a saved view stores the preset and
// the concrete dates are derived whenever the view is applied or compared.
function resolveArtifactViewFilters(
  filters: ArtifactsFilterSet,
  tzOffsetInSec: number | undefined
): ArtifactsFilterSet {
  if (filters.dateRangeMode !== 'preset') {
    return filters;
  }
  if (filters.datePreset === ARTIFACT_PRESET_ALL) {
    return { ...filters, fromDate: undefined, toDate: undefined };
  }
  const dates = computePresetDates(filters.datePreset, tzOffsetInSec);
  return { ...filters, fromDate: dates.from, toDate: dates.to };
}

function runKey(item: Pick<ArtifactListItem, 'name' | 'dagRunId'>): string {
  return `${item.name}\u0000${item.dagRunId}`;
}

// Synthetic paths keep tree node identities unique across runs while the
// real relative path stays available for preview and download requests.
function runTreeRoot(
  item: Pick<ArtifactListItem, 'name' | 'dagRunId'>
): string {
  return `@run:${runKey(item)}`;
}

type FileTreeNode = {
  name: string;
  path: string;
  type: 'directory' | 'file';
  size?: number;
  children?: FileTreeNode[];
};

function filesToTreeNodes(
  files: ArtifactListItem['files'],
  prefix: string
): FileTreeNode[] {
  const root: FileTreeNode[] = [];
  for (const file of files) {
    const parts = file.path.split('/');
    let level: FileTreeNode[] = root;
    let acc = prefix;
    for (let i = 0; i < parts.length - 1; i++) {
      const part = parts[i]!;
      acc = `${acc}/${part}`;
      const existing = level.find(
        (node) => node.type === 'directory' && node.path === acc
      );
      if (existing) {
        level = existing.children ?? [];
        continue;
      }
      const dir: FileTreeNode = {
        name: part,
        path: acc,
        type: 'directory',
        children: [],
      };
      level.push(dir);
      level = dir.children ?? [];
    }
    const leafName = parts[parts.length - 1] ?? file.path;
    level.push({
      name: leafName,
      path: `${acc}/${leafName}`,
      type: 'file',
      size: file.size,
    });
  }
  return root;
}

function Artifacts() {
  const { ts } = useI18n();
  const appBarContext = React.useContext(AppBarContext);
  const config = useConfig();
  const workspaceSelection = appBarContext.workspaceSelection;
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const location = useLocation();
  const navigate = useNavigate();
  const searchState = useSearchState();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const searchStateScope = JSON.stringify({
    remoteNode,
    workspace: workspaceSelectionKey(workspaceSelection),
  });
  const [searchText, setSearchText] = React.useState('');
  const [apiSearchText, setApiSearchText] = React.useState('');
  const [fileNameText, setFileNameText] = React.useState('');
  const [apiFileNameText, setApiFileNameText] = React.useState('');
  const [dateRangeMode, setDateRangeMode] = React.useState<'preset' | 'custom'>(
    DEFAULT_ARTIFACT_FILTERS.dateRangeMode
  );
  const [datePreset, setDatePreset] = React.useState(
    DEFAULT_ARTIFACT_FILTERS.datePreset
  );
  const [fromDate, setFromDate] = React.useState<string | undefined>();
  const [toDate, setToDate] = React.useState<string | undefined>();
  const [apiFromDate, setApiFromDate] = React.useState<string | undefined>();
  const [apiToDate, setApiToDate] = React.useState<string | undefined>();

  const artifactViewScope = React.useMemo(
    () => viewScopeForSelection(workspaceSelection),
    [workspaceSelection]
  );
  const canManageArtifactViews = useCanWriteForWorkspace(
    artifactViewScope.workspace
  );
  const {
    views: sharedArtifactViews,
    isLoading: artifactViewsLoading,
    createView,
    updateView,
    deleteView,
  } = useViews(ViewSpecType.artifact);
  const scopedArtifactViews = React.useMemo(
    () =>
      sharedArtifactViews.filter((view) =>
        viewMatchesScope(view, artifactViewScope)
      ),
    [sharedArtifactViews, artifactViewScope]
  );
  const artifactViews = React.useMemo(
    () => scopedArtifactViews.map(artifactsFilterViewFromView),
    [scopedArtifactViews]
  );
  const defaultArtifactViewId = scopedArtifactViews.find(
    (view) => view.isDefault
  )?.id;
  const [activeArtifactViewId, setActiveArtifactViewId] = React.useState<
    string | null
  >(null);
  const [artifactViewError, setArtifactViewError] = React.useState<
    string | null
  >(null);
  const [selected, setSelected] = React.useState<{
    name: string;
    dagRunId: string;
    path: string;
  } | null>(null);
  const [selectionScope, setSelectionScope] = React.useState(searchStateScope);
  if (selectionScope !== searchStateScope) {
    setSelectionScope(searchStateScope);
    setSelected(null);
  }
  const loadMoreSentinelRef = React.useRef<HTMLDivElement>(null);
  const autoLoadPendingRef = React.useRef(false);

  // Convert datetime to unix timestamp (seconds) for API calls
  const formatDateForApi = (
    dateString: string | undefined
  ): number | undefined => {
    if (!dateString) return undefined;

    // Add seconds if they're missing (datetime-local inputs only have HH:mm)
    const dateWithSeconds =
      dateString.split(':').length < 3 ? `${dateString}:00` : dateString;

    // Interpret the wall clock in the configured timezone, never in the
    // browser's, then convert to the Unix timestamp.
    if (config.tzOffsetInSec !== undefined) {
      return dayjs(dateWithSeconds)
        .utcOffset(config.tzOffsetInSec / 60, true)
        .unix();
    } else {
      return dayjs(dateWithSeconds).unix();
    }
  };

  React.useEffect(() => {
    appBarContext.setTitle('Artifacts');
  }, [appBarContext]);

  const currentFilters = React.useMemo<ArtifactsFilterSet>(
    () => ({
      searchText: apiSearchText,
      fileName: apiFileNameText,
      fromDate: apiFromDate,
      toDate: apiToDate,
      dateRangeMode,
      datePreset,
    }),
    [
      apiFileNameText,
      apiFromDate,
      apiSearchText,
      apiToDate,
      dateRangeMode,
      datePreset,
    ]
  );
  const currentFiltersRef = React.useRef(currentFilters);
  currentFiltersRef.current = currentFilters;
  const lastPersistedFiltersRef = React.useRef<ArtifactsFilterSet | null>(null);
  const previousArtifactScopeRef = React.useRef(searchStateScope);

  const applyResolvedFilters = React.useCallback(
    (filters: ArtifactsFilterSet) => {
      setSearchText(filters.searchText);
      setFileNameText(filters.fileName);
      setDateRangeMode(filters.dateRangeMode);
      setDatePreset(filters.datePreset);
      setFromDate(filters.fromDate);
      setToDate(filters.toDate);
      setApiSearchText(filters.searchText);
      setApiFileNameText(filters.fileName);
      setApiFromDate(filters.fromDate);
      setApiToDate(filters.toDate);
    },
    []
  );

  React.useEffect(() => {
    if (artifactViewsLoading) {
      return;
    }

    // URL parameters belong to the previous workspace when the scope has just
    // changed; drop them and start from the destination's default view (or All
    // artifacts), so another workspace's filters cannot leak in.
    const scopeChanged = previousArtifactScopeRef.current !== searchStateScope;
    if (scopeChanged) {
      previousArtifactScopeRef.current = searchStateScope;
      setArtifactViewError(null);
      const clean = new URLSearchParams();
      clean.set('view', defaultArtifactViewId ?? ALL_ARTIFACTS_VIEW_PARAM);
      navigate(
        { pathname: location.pathname, search: `?${clean.toString()}` },
        { replace: true }
      );
      return;
    }

    const params = new URLSearchParams(location.search);
    const stored = searchState.readState<ArtifactsFilterSet>(
      SEARCH_STATE_PAGE_KEY,
      searchStateScope
    );
    const urlFilters: Partial<ArtifactsFilterSet> = {};
    let hasUrlFilters = false;

    if (params.has('name')) {
      urlFilters.searchText = params.get('name') ?? '';
      hasUrlFilters = true;
    }
    if (params.has('fileName')) {
      urlFilters.fileName = params.get('fileName') ?? '';
      hasUrlFilters = true;
    }

    const dateModeParam = params.get('dateMode');
    if (dateModeParam === 'preset' || dateModeParam === 'custom') {
      urlFilters.dateRangeMode = dateModeParam;
      hasUrlFilters = true;
    }
    if (params.has('preset')) {
      urlFilters.datePreset =
        params.get('preset') || DEFAULT_ARTIFACT_FILTERS.datePreset;
      hasUrlFilters = true;
    }
    // An explicit custom mode defines its range entirely through the URL, so an
    // absent bound means no bound rather than the one a selected view carries.
    // A concrete range with no mode at all comes from a hand-written link;
    // treat it as custom so the dates are not overwritten by a preset.
    if (
      dateModeParam === 'custom' ||
      (dateModeParam === null &&
        (params.has('fromDate') || params.has('toDate')))
    ) {
      urlFilters.fromDate = params.get('fromDate') || undefined;
      urlFilters.toDate = params.get('toDate') || undefined;
      if (dateModeParam === null) {
        urlFilters.dateRangeMode = 'custom';
      }
      hasUrlFilters = true;
    }

    // A URL that carries filters is self-contained: a link shared with someone
    // else must resolve identically for them, so only a bare URL falls back to
    // this session's stored filters. Every write to the URL carries the whole
    // applied filter set, so an absent parameter means empty, not "unset".
    let base: ArtifactsFilterSet = hasUrlFilters
      ? { ...DEFAULT_ARTIFACT_FILTERS }
      : { ...DEFAULT_ARTIFACT_FILTERS, ...(stored ?? {}) };
    let nextActiveArtifactViewId: string | null = null;
    const requestedViewId = params.get('view');
    const requestedView =
      requestedViewId === ALL_ARTIFACTS_VIEW_PARAM
        ? undefined
        : artifactViews.find((view) => view.id === requestedViewId);
    const defaultView = artifactViews.find(
      (view) => view.id === defaultArtifactViewId
    );

    if (requestedViewId === ALL_ARTIFACTS_VIEW_PARAM) {
      base = { ...DEFAULT_ARTIFACT_FILTERS };
    } else if (requestedView) {
      base = resolveArtifactViewFilters(
        requestedView.filters,
        config.tzOffsetInSec
      );
      nextActiveArtifactViewId = requestedView.id;
    } else if (!hasUrlFilters && defaultView) {
      base = resolveArtifactViewFilters(
        defaultView.filters,
        config.tzOffsetInSec
      );
      nextActiveArtifactViewId = defaultView.id;
    }

    const next = hasUrlFilters ? { ...base, ...urlFilters } : base;
    // A preset range is relative to "now", so it is derived on every restore.
    // The concrete dates a saved view or this session carries were computed
    // whenever the preset was last picked, which may be days ago.
    const resolved =
      next.dateRangeMode === 'preset'
        ? resolveArtifactViewFilters(next, config.tzOffsetInSec)
        : next;

    setActiveArtifactViewId(nextActiveArtifactViewId);

    if (areArtifactFiltersEqual(currentFiltersRef.current, resolved)) {
      if (hasUrlFilters) {
        lastPersistedFiltersRef.current = resolved;
        searchState.writeState(
          SEARCH_STATE_PAGE_KEY,
          searchStateScope,
          resolved
        );
      }
      return;
    }

    applyResolvedFilters(resolved);
    lastPersistedFiltersRef.current = resolved;
    searchState.writeState(SEARCH_STATE_PAGE_KEY, searchStateScope, resolved);
  }, [
    applyResolvedFilters,
    artifactViews,
    artifactViewsLoading,
    config.tzOffsetInSec,
    defaultArtifactViewId,
    location.pathname,
    location.search,
    navigate,
    searchState,
    searchStateScope,
  ]);

  React.useEffect(() => {
    // Persistence must wait for the URL/view restoration to complete: writing
    // the initial default filters before stored state is restored would
    // clobber the session's filters.
    if (artifactViewsLoading) {
      return;
    }
    const persisted = lastPersistedFiltersRef.current;
    if (persisted && areArtifactFiltersEqual(persisted, currentFilters)) {
      return;
    }
    lastPersistedFiltersRef.current = currentFilters;
    searchState.writeState(
      SEARCH_STATE_PAGE_KEY,
      searchStateScope,
      currentFilters
    );
  }, [artifactViewsLoading, currentFilters, searchState, searchStateScope]);

  const updateSearchParams = (updates: Record<string, string | undefined>) => {
    const params = new URLSearchParams(location.search);
    if (!('view' in updates) && activeArtifactViewId) {
      params.set('view', activeArtifactViewId);
    }
    for (const [key, value] of Object.entries(updates)) {
      // An explicit empty string overrides a saved view's value; only
      // undefined removes the parameter.
      if (value !== undefined) {
        params.set(key, value);
      } else {
        params.delete(key);
      }
    }
    const search = params.toString();
    navigate({
      pathname: location.pathname,
      search: search ? `?${search}` : '',
    });
  };

  const searchOverrideKey = (value: string): string | undefined =>
    activeArtifactViewId !== null
      ? value
      : value.length > 0
        ? value
        : undefined;

  const handleSearch = () => {
    const nextSearchText = searchText.trim();
    const nextFileName = fileNameText.trim();
    setApiSearchText(nextSearchText);
    setApiFileNameText(nextFileName);
    setApiFromDate(fromDate);
    setApiToDate(toDate);
    updateSearchParams({
      name: searchOverrideKey(nextSearchText),
      fileName: searchOverrideKey(nextFileName),
      dateMode: dateRangeMode,
      preset: dateRangeMode === 'preset' ? datePreset : undefined,
      fromDate: dateRangeMode === 'custom' ? fromDate : undefined,
      toDate: dateRangeMode === 'custom' ? toDate : undefined,
    });
  };

  const handleDatePresetChange = (preset: string) => {
    setDatePreset(preset);
    const dates =
      preset === ARTIFACT_PRESET_ALL
        ? undefined
        : computePresetDates(preset, config.tzOffsetInSec);
    setFromDate(dates?.from);
    setToDate(dates?.to);
    setApiFromDate(dates?.from);
    setApiToDate(dates?.to);
    // Read the payload from the local dates: the state setters above have not
    // been applied yet within this handler.
    updateSearchParams({
      name: searchOverrideKey(apiSearchText),
      fileName: searchOverrideKey(apiFileNameText),
      dateMode: 'preset',
      preset,
      fromDate: undefined,
      toDate: undefined,
    });
  };

  const handleInputKeyPress = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      handleSearch();
    }
  };

  const applyArtifactView = React.useCallback(
    (view: ArtifactsFilterView) => {
      setArtifactViewError(null);
      const params = new URLSearchParams(location.search);
      const filters = resolveArtifactViewFilters(
        view.filters,
        config.tzOffsetInSec
      );
      // Apply the filters directly: when the resulting URL is unchanged (for
      // example resetting a view that was selected from the dropdown), the
      // restoration effect has no location change to react to.
      applyResolvedFilters(filters);
      for (const key of ARTIFACT_FILTER_QUERY_KEYS) {
        params.delete(key);
      }
      params.set('view', view.id);
      if (filters.searchText) {
        params.set('name', filters.searchText);
      }
      if (filters.fileName) {
        params.set('fileName', filters.fileName);
      }
      params.set('dateMode', filters.dateRangeMode);
      if (filters.dateRangeMode === 'preset') {
        params.set('preset', filters.datePreset);
      } else {
        // Only a custom range persists concrete dates; preset mode derives
        // them whenever the view is applied.
        if (filters.fromDate) {
          params.set('fromDate', filters.fromDate);
        }
        if (filters.toDate) {
          params.set('toDate', filters.toDate);
        }
      }
      const search = params.toString();
      navigate(
        { pathname: location.pathname, search: search ? `?${search}` : '' },
        { replace: true }
      );
    },
    [
      applyResolvedFilters,
      config.tzOffsetInSec,
      location.pathname,
      location.search,
      navigate,
    ]
  );

  const handleSelectArtifactView = (viewId: string) => {
    const view = artifactViews.find((item) => item.id === viewId);
    if (view) {
      applyArtifactView(view);
    }
  };

  const handleShowAllArtifacts = () => {
    setArtifactViewError(null);
    // Same rationale as applyArtifactView: the target URL may already be
    // active, so restore the default filters directly.
    applyResolvedFilters({ ...DEFAULT_ARTIFACT_FILTERS });
    const params = new URLSearchParams(location.search);
    for (const key of ARTIFACT_FILTER_QUERY_KEYS) {
      params.delete(key);
    }
    params.set('view', ALL_ARTIFACTS_VIEW_PARAM);
    const search = params.toString();
    navigate(
      { pathname: location.pathname, search: search ? `?${search}` : '' },
      { replace: true }
    );
  };

  const handleResetArtifactView = () => {
    const view = artifactViews.find((item) => item.id === activeArtifactViewId);
    if (view) {
      applyArtifactView(view);
    }
  };

  const handleSaveArtifactView = async (
    name: string,
    makeDefault: boolean,
    pinned: boolean
  ): Promise<void> => {
    setArtifactViewError(null);
    try {
      const view = await createView(
        buildArtifactViewSpec(
          name,
          currentFiltersRef.current,
          makeDefault,
          pinned,
          artifactViewScope
        )
      );
      applyArtifactView(artifactsFilterViewFromView(view));
    } catch (error) {
      setArtifactViewError(
        error instanceof Error ? error.message : 'Failed to save artifact view'
      );
      throw error;
    }
  };

  const handleUpdateArtifactView = async (): Promise<void> => {
    const view = scopedArtifactViews.find(
      (item) => item.id === activeArtifactViewId
    );
    if (!view) {
      return;
    }
    setArtifactViewError(null);
    try {
      const updated = await updateView(
        view.id,
        buildArtifactViewSpec(
          view.name,
          currentFiltersRef.current,
          view.isDefault ?? false,
          view.pinned ?? false,
          artifactViewScope
        )
      );
      applyArtifactView(artifactsFilterViewFromView(updated));
    } catch (error) {
      setArtifactViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update artifact view'
      );
      throw error;
    }
  };

  const handleSetDefaultArtifactView = async (
    viewId: string | undefined
  ): Promise<void> => {
    const target = scopedArtifactViews.find(
      (view) => view.id === (viewId ?? defaultArtifactViewId)
    );
    if (!target) {
      return;
    }
    setArtifactViewError(null);
    try {
      await updateView(
        target.id,
        buildArtifactViewSpec(
          target.name,
          artifactsFilterSetFromView(target),
          viewId !== undefined,
          target.pinned ?? false,
          artifactViewScope
        )
      );
    } catch (error) {
      setArtifactViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update the default artifact view'
      );
      throw error;
    }
  };

  const handleSetPinnedArtifactView = async (
    viewId: string,
    pinned: boolean
  ): Promise<void> => {
    const target = scopedArtifactViews.find((view) => view.id === viewId);
    if (!target) {
      return;
    }
    setArtifactViewError(null);
    try {
      await updateView(
        target.id,
        buildArtifactViewSpec(
          target.name,
          artifactsFilterSetFromView(target),
          target.isDefault ?? false,
          pinned,
          artifactViewScope
        )
      );
    } catch (error) {
      setArtifactViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update the starred artifact view'
      );
      throw error;
    }
  };

  const handleDeleteArtifactView = async (viewId: string): Promise<void> => {
    const deletingActiveView = viewId === activeArtifactViewId;
    setArtifactViewError(null);
    try {
      await deleteView(viewId);
      if (deletingActiveView) {
        handleShowAllArtifacts();
      }
    } catch (error) {
      setArtifactViewError(
        error instanceof Error
          ? error.message
          : 'Failed to delete artifact view'
      );
      throw error;
    }
  };

  const activeArtifactView = artifactViews.find(
    (view) => view.id === activeArtifactViewId
  );
  const isArtifactViewEdited = activeArtifactView
    ? !areArtifactFiltersEqual(
        currentFilters,
        resolveArtifactViewFilters(
          activeArtifactView.filters,
          config.tzOffsetInSec
        )
      )
    : false;
  const isAllArtifactsView =
    activeArtifactViewId === null &&
    areArtifactFiltersEqual(currentFilters, DEFAULT_ARTIFACT_FILTERS);

  const artifactQuery = React.useMemo<ArtifactListQuery>(
    () => ({
      remoteNode: appBarContext.selectedRemoteNode || 'local',
      name: apiSearchText || undefined,
      fileName: apiFileNameText || undefined,
      fromDate: formatDateForApi(apiFromDate),
      toDate: formatDateForApi(apiToDate),
      limit: ARTIFACT_LIST_LIMIT,
      ...workspaceQuery,
    }),
    [
      apiFileNameText,
      apiFromDate,
      apiSearchText,
      apiToDate,
      appBarContext.selectedRemoteNode,
      formatDateForApi,
      workspaceQuery,
    ]
  );

  const {
    items,
    error,
    isInitialLoading,
    isLoadingMore,
    loadMoreError,
    hasMore,
    refresh: refreshArtifacts,
    loadMore: handleLoadMore,
  } = usePaginatedArtifacts({
    query: artifactQuery,
  });

  React.useEffect(() => {
    if (isInitialLoading) {
      return;
    }
    const selectedIsLoaded =
      selected !== null &&
      items.some(
        (item) =>
          item.name === selected.name &&
          item.dagRunId === selected.dagRunId &&
          item.files.some((file) => file.path === selected.path)
      );
    if (!selectedIsLoaded) {
      const firstRun = items.find((item) => item.files.length > 0);
      setSelected(
        firstRun
          ? {
              name: firstRun.name,
              dagRunId: firstRun.dagRunId,
              path: firstRun.files[0]!.path,
            }
          : null
      );
    }
  }, [items, selected, isInitialLoading]);

  const canAutoLoadMore = typeof IntersectionObserver !== 'undefined';
  useAutoLoadMore(
    loadMoreSentinelRef,
    canAutoLoadMore && hasMore && !isLoadingMore && !loadMoreError,
    () => {
      if (autoLoadPendingRef.current) {
        return;
      }
      autoLoadPendingRef.current = true;
      void handleLoadMore();
    }
  );
  React.useEffect(() => {
    if (!isLoadingMore) {
      autoLoadPendingRef.current = false;
    }
  }, [isLoadingMore]);

  const fileCount = items.reduce((total, item) => total + item.files.length, 0);

  const formatTimezoneOffset = (): string => {
    if (config.tzOffsetInSec === undefined) return '';

    // Convert seconds to hours and minutes
    const offsetInMinutes = config.tzOffsetInSec / 60;
    const hours = Math.floor(Math.abs(offsetInMinutes) / 60);
    const minutes = Math.abs(offsetInMinutes) % 60;

    // Format with sign and padding
    const sign = offsetInMinutes >= 0 ? '+' : '-';
    const formattedHours = hours.toString().padStart(2, '0');
    const formattedMinutes = minutes.toString().padStart(2, '0');

    return `(${sign}${formattedHours}:${formattedMinutes})`;
  };

  const formatTimestamp = (timestamp: string | undefined): string => {
    if (!timestamp) {
      return '-';
    }
    const value = dayjs(timestamp);
    const configuredTime =
      config.tzOffsetInSec === undefined
        ? value
        : value.utcOffset(config.tzOffsetInSec / 60);
    return configuredTime.format('YYYY-MM-DD HH:mm:ss');
  };

  const tzLabel = formatTimezoneOffset();
  const selectedNodeSyntheticPath =
    selected !== null ? `${runTreeRoot(selected)}/${selected.path}` : null;

  const treeNodes = React.useMemo<FileTreeNode[]>(
    () =>
      items.map((item) => ({
        name: item.name,
        path: runTreeRoot(item),
        type: 'directory',
        children: filesToTreeNodes(item.files, runTreeRoot(item)),
      })),
    [items]
  );
  const filenameFilterRef = React.useRef<HTMLInputElement>(null);
  const navigation = useArtifactTreeNavigation({
    nodes: treeNodes,
    selectedPath: selectedNodeSyntheticPath,
    scope: searchStateScope,
    ready: !isInitialLoading,
    autoFocus: true,
    onSearch: () => filenameFilterRef.current?.focus(),
    onSelect: (path) => {
      const item = items.find((item) =>
        path.startsWith(`${runTreeRoot(item)}/`)
      );
      if (item) {
        setSelected({
          name: item.name,
          dagRunId: item.dagRunId,
          path: path.slice(runTreeRoot(item).length + 1),
        });
      }
    },
  });
  const focusedRun = items.find(
    (item) =>
      navigation.focusedPath === runTreeRoot(item) ||
      navigation.focusedPath?.startsWith(`${runTreeRoot(item)}/`)
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="mb-2 flex min-w-0 items-center gap-3">
        <Title>
          <I18nText text={'Artifacts'} />
        </Title>
        <ViewSelector
          kind="artifact"
          views={artifactViews}
          activeViewId={activeArtifactViewId}
          defaultViewId={defaultArtifactViewId}
          isAllView={isAllArtifactsView}
          isActiveViewEdited={isArtifactViewEdited}
          canManageViews={canManageArtifactViews}
          error={artifactViewError}
          onSelectView={handleSelectArtifactView}
          onShowAll={handleShowAllArtifacts}
          onResetView={handleResetArtifactView}
          onSaveView={handleSaveArtifactView}
          onUpdateView={handleUpdateArtifactView}
          onSetDefault={handleSetDefaultArtifactView}
          onSetPinned={handleSetPinnedArtifactView}
          onDeleteView={handleDeleteArtifactView}
        />
        <I18nProps>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => void refreshArtifacts()}
            title="Reload artifacts"
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
        </I18nProps>
      </div>
      {artifactViewError && (
        <p role="alert" className="mb-2 text-xs text-destructive">
          {artifactViewError}
        </p>
      )}
      <div className="mb-3 space-y-3 rounded-lg border border-border bg-card/50 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <I18nProps>
            <Input
              placeholder="Filter by DAG name..."
              value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              onKeyDown={handleInputKeyPress}
              className="w-[200px]"
            />
          </I18nProps>
          <I18nProps>
            <Input
              ref={filenameFilterRef}
              placeholder="Filter by file name..."
              value={fileNameText}
              onChange={(e) => setFileNameText(e.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Escape' && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  navigation.returnToTree();
                } else {
                  handleInputKeyPress(event);
                }
              }}
              className="w-[200px]"
            />
          </I18nProps>
          <I18nProps>
            <ToggleGroup aria-label="Date range mode" className="h-9 p-0.5">
              <I18nProps>
                <ToggleButton
                  value="preset"
                  groupValue={dateRangeMode}
                  onClick={() => {
                    setDateRangeMode('preset');
                    handleDatePresetChange(datePreset);
                  }}
                  position="first"
                  className="h-8 px-3"
                >
                  <I18nText text={'Quick'} />
                </ToggleButton>
              </I18nProps>
              <I18nProps>
                <ToggleButton
                  value="custom"
                  groupValue={dateRangeMode}
                  onClick={() => {
                    setDateRangeMode('custom');
                    setFromDate(apiFromDate);
                    setToDate(apiToDate);
                  }}
                  position="last"
                  className="h-8 px-3"
                >
                  <I18nText text={'Custom'} />
                </ToggleButton>
              </I18nProps>
            </ToggleGroup>
          </I18nProps>
          {dateRangeMode === 'preset' ? (
            <Select value={datePreset} onValueChange={handleDatePresetChange}>
              <I18nProps>
                <SelectTrigger aria-label="Date preset" className="w-[180px]">
                  <I18nProps>
                    <SelectValue placeholder="Select period" />
                  </I18nProps>
                </SelectTrigger>
              </I18nProps>
              <SelectContent>
                <SelectItem value={ARTIFACT_PRESET_ALL}>
                  <I18nText text={'All time'} />
                </SelectItem>
                <SelectItem value="today">
                  <I18nText text={'Today'} />
                </SelectItem>
                <SelectItem value="yesterday">
                  <I18nText text={'Yesterday'} />
                </SelectItem>
                <SelectItem value="last7days">
                  <I18nText text={'Last 7 days'} />
                </SelectItem>
                <SelectItem value="last30days">
                  <I18nText text={'Last 30 days'} />
                </SelectItem>
                <SelectItem value="thisWeek">
                  <I18nText text={'This week'} />
                </SelectItem>
                <SelectItem value="thisMonth">
                  <I18nText text={'This month'} />
                </SelectItem>
              </SelectContent>
            </Select>
          ) : (
            <DateRangePicker
              fromDate={fromDate}
              toDate={toDate}
              onFromDateChange={(value) => setFromDate(value || undefined)}
              onToDateChange={(value) => setToDate(value || undefined)}
              onEnterPress={() => handleSearch()}
              fromLabel={`From ${tzLabel}`}
              toLabel={`To ${tzLabel}`}
              className="w-full md:w-auto"
            />
          )}
        </div>
      </div>

      {items.length === 0 ? (
        isInitialLoading ? (
          <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
            <I18nText text={'Loading artifacts...'} />
          </div>
        ) : error ? (
          <div className="flex items-start gap-2 rounded-md bg-destructive/5 px-3 py-3 text-sm text-destructive">
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>
              {error instanceof Error
                ? error.message
                : 'Failed to load artifacts'}
            </span>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center rounded-md border border-dashed border-border bg-muted/20 px-4 py-10 text-sm text-muted-foreground">
            <p className="font-medium text-foreground">
              <I18nText text={'No artifacts found'} />
            </p>
            <p className="mt-1 max-w-md text-center">
              <I18nText
                text={
                  'No DAG-runs in the selected time range produced artifact files. Adjust the date range or filters.'
                }
              />
            </p>
          </div>
        )
      ) : (
        <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 xl:grid-cols-[320px_minmax(0,1fr)] xl:grid-rows-1">
          <div className="flex min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-surface">
            <div className="flex items-center justify-between border-b border-border px-3 py-2">
              <div>
                <p className="text-sm font-medium">
                  <I18nText text={'Artifacts'} />
                </p>
                <p className="text-xs text-muted-foreground">
                  {ts('{count} files', { count: fileCount })}
                  {items.some((item) => item.filesTruncated) ? (
                    <span className="ml-1">
                      <I18nText
                        text={'· selective list, use the run to view all files'}
                      />
                    </span>
                  ) : null}
                </p>
              </div>
            </div>
            <div className="min-h-0 flex-1 overflow-auto p-2">
              <div
                {...navigation.treeProps}
                aria-label={ts('Artifacts')}
                className="space-y-0.5"
              >
                {items.map((item, index) => {
                  const root = runTreeRoot(item);
                  const treeNode = treeNodes[index]!;
                  const nodes = treeNode.children ?? [];
                  const isOpen = navigation.expandedPaths.has(root);
                  const Icon = isOpen ? FolderOpen : Folder;
                  return (
                    <div
                      key={runKey(item)}
                      {...navigation.getItemProps(treeNode)}
                    >
                      <div className="group/run flex items-center gap-1 rounded-md transition-colors hover:bg-muted">
                        <div
                          title={item.name}
                          className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm text-foreground transition-colors"
                        >
                          <Icon className="h-4 w-4 shrink-0" />
                          <span className="min-w-0 flex-1 truncate font-medium">
                            {item.name}
                          </span>
                          <span className="shrink-0 text-[11px] text-muted-foreground">
                            {item.files.length > 0
                              ? ts('{count} files', {
                                  count: item.files.length,
                                })
                              : '—'}
                          </span>
                        </div>
                        <Link
                          to={`/dag-runs/${item.name}/${item.dagRunId}`}
                          tabIndex={-1}
                          className="mr-1 shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                          title="Open DAG run"
                          aria-label={`Open DAG run ${item.name}`}
                          onClick={(event) => event.stopPropagation()}
                        >
                          <LinkIcon className="h-4 w-4" />
                        </Link>
                      </div>
                      <span className="block truncate pl-8 text-[11px] text-muted-foreground">
                        {formatTimestamp(item.createdAt)} · {item.dagRunId}
                      </span>
                      {isOpen && nodes.length > 0 && (
                        <div role="group" className="space-y-0.5">
                          {nodes.map((node) => (
                            <TreeNode
                              key={node.path}
                              node={node}
                              depth={1}
                              rootPrefix={root}
                              navigation={navigation}
                              selectedPath={selectedNodeSyntheticPath}
                            />
                          ))}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
              <div ref={loadMoreSentinelRef} className="h-4 w-full" />
            </div>
            <div className="space-y-2 p-2">
              <p className="text-xs text-muted-foreground">
                <I18nText text="↑↓ / j k navigate · ←→ folders · Enter preview · / filter" />
              </p>
              {focusedRun && (
                <Link
                  className="inline-flex items-center gap-1 rounded text-xs text-primary focus-visible:ring-2 focus-visible:ring-ring"
                  to={`/dag-runs/${focusedRun.name}/${focusedRun.dagRunId}`}
                >
                  <LinkIcon className="h-3 w-3" />
                  <I18nText text="Open DAG run" />
                </Link>
              )}
              {loadMoreError && (
                <div className="text-sm text-error">{loadMoreError}</div>
              )}
              {isLoadingMore ? (
                <div className="text-sm text-muted-foreground">
                  <I18nText text={'Loading more artifacts...'} />
                </div>
              ) : hasMore ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-full"
                  onClick={() => void handleLoadMore()}
                >
                  {loadMoreError ? (
                    <I18nText text={'Retry loading more'} />
                  ) : (
                    <I18nText text={'Load more'} />
                  )}
                </Button>
              ) : (
                <div className="text-sm text-muted-foreground">
                  <I18nText text={'All artifact files are displayed.'} />
                </div>
              )}
            </div>
          </div>

          <ArtifactFilePreview
            contentRef={navigation.previewRef}
            onReturnToFiles={navigation.returnToTree}
            dagRunName={selected?.name ?? ''}
            dagRunId={selected?.dagRunId ?? ''}
            path={selected?.path ?? null}
            remoteNode={appBarContext.selectedRemoteNode || 'local'}
            fillHeight
          />
        </div>
      )}
    </div>
  );
}

function TreeNode({
  node,
  depth,
  rootPrefix,
  navigation,
  selectedPath,
}: {
  node: FileTreeNode;
  depth: number;
  rootPrefix: string;
  navigation: ReturnType<typeof useArtifactTreeNavigation>;
  selectedPath: string | null;
}) {
  const isDir = node.type === 'directory';
  const isOpen = isDir && navigation.expandedPaths.has(node.path);
  const isSelected = !isDir && selectedPath === node.path;
  const displayPath = node.path.slice(rootPrefix.length + 1);

  const Icon = isDir
    ? isOpen
      ? FolderOpen
      : Folder
    : node.path.match(/\.(md|markdown|mdown|mkd)$/i)
      ? FileText
      : node.path.match(/\.(html?|xhtml)$/i)
        ? FileCode
        : node.path.match(/\.(png|jpe?g|gif|webp|svg|bmp|ico)$/i)
          ? FileImage
          : File;

  return (
    <div {...navigation.getItemProps(node)} title={displayPath}>
      <div
        className={cn(
          'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
          isSelected
            ? 'bg-primary/10 text-primary'
            : 'text-foreground hover:bg-muted'
        )}
        style={{ paddingLeft: `${depth * 14 + 8}px` }}
        {...(!isDir ? { 'data-artifact-path': node.path } : {})}
      >
        <Icon className="h-4 w-4 shrink-0" />
        <span className="min-w-0 flex-1 truncate">{node.name}</span>
        {!isDir && node.size != null && (
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {Intl.NumberFormat().format(node.size)}
          </span>
        )}
      </div>
      {isDir && isOpen && node.children && node.children.length > 0 && (
        <div role="group" className="space-y-0.5">
          {node.children.map((child) => (
            <TreeNode
              key={child.path}
              node={child}
              depth={depth + 1}
              rootPrefix={rootPrefix}
              navigation={navigation}
              selectedPath={selectedPath}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function useAutoLoadMore(
  sentinelRef: React.RefObject<HTMLDivElement | null>,
  enabled: boolean,
  onLoadMore: () => void
) {
  React.useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !enabled || typeof IntersectionObserver === 'undefined') {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          onLoadMore();
        }
      },
      { threshold: 0.1 }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [enabled, onLoadMore, sentinelRef]);
}

export default Artifacts;
