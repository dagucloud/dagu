// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from 'dayjs';
import { Layers, List, Search } from 'lucide-react';
import React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Status, ViewSpecType } from '../../api/v1/schema';
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
import { LabelCombobox } from '@/components/ui/label-combobox';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useCanWriteForWorkspace } from '../../contexts/AuthContext';
import { useConfig } from '../../contexts/ConfigContext';
import { useSearchState } from '../../contexts/SearchStateContext';
import { useUserPreferences } from '../../contexts/UserPreference';
import DAGRunBatchActions from '../../features/dag-runs/components/common/DAGRunBatchActions';
import { DAGRunDetailsModal } from '../../features/dag-runs/components/dag-run-details';
import DAGRunGroupedView from '../../features/dag-runs/components/dag-run-list/DAGRunGroupedView';
import DAGRunTable from '../../features/dag-runs/components/dag-run-list/DAGRunTable';
import { usePaginatedDAGRuns } from '../../features/dag-runs/hooks/dagRunPagination';
import {
  buildRunViewSpec,
  dagRunsFilterSetFromView,
  type DAGRunsFilterSet,
  type DAGRunsFilterView,
} from '../../features/dag-runs/lib/runViews';
import { ViewSelector } from '../../features/views/ViewSelector';
import {
  viewMatchesScope,
  viewScopeForSelection,
} from '../../features/views/viewScope';
import { useViews, type View } from '../../hooks/useViews';
import { useQuery } from '../../hooks/api';
import { useBulkDAGRunSelection } from '../../features/dag-runs/hooks/useBulkDAGRunSelection';
import {
  withoutWorkspaceLabels,
  workspaceSelectionKey,
  workspaceSelectionQuery,
} from '../../lib/workspace';
import StatusChip from '@/components/ui/status-chip';
import Title from '@/components/ui/title';
import type { StatusTab } from '@/features/dags/components/DAGStatus';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type DAGRunsFilters = DAGRunsFilterSet;

const ALL_RUNS_VIEW_PARAM = 'all';

const RUN_FILTER_QUERY_KEYS = [
  'name',
  'dagRunId',
  'status',
  'labels',
  'tags',
  'fromDate',
  'toDate',
  'dateMode',
  'preset',
  'specificValue',
  'specificPeriod',
  'view',
] as const;

const areLabelsEqual = (a: string[], b: string[]): boolean => {
  if (a.length !== b.length) return false;
  const sortedA = [...a].sort();
  const sortedB = [...b].sort();
  return sortedA.every((label, i) => label === sortedB[i]);
};

const STATUS_CONFIG: Record<Status, string> = {
  [Status.NotStarted]: 'not_started',
  [Status.Running]: 'running',
  [Status.Failed]: 'failed',
  [Status.Aborted]: 'aborted',
  [Status.Success]: 'succeeded',
  [Status.Queued]: 'queued',
  [Status.PartialSuccess]: 'partially_succeeded',
  [Status.Waiting]: 'waiting',
  [Status.Rejected]: 'rejected',
};

function StatusSelectDisplay({ status }: { status: string }): React.ReactNode {
  if (status === 'all') {
    return <I18nText text="All Statuses" />;
  }

  const statusNum = parseInt(status) as Status;
  const label = STATUS_CONFIG[statusNum];
  if (label) {
    return <I18nText text={label} />;
  }

  return null;
}

const areFiltersEqual = (a: DAGRunsFilters, b: DAGRunsFilters): boolean =>
  a.searchText === b.searchText &&
  a.dagRunId === b.dagRunId &&
  a.status === b.status &&
  areLabelsEqual(a.labels, b.labels) &&
  a.fromDate === b.fromDate &&
  a.toDate === b.toDate &&
  a.dateRangeMode === b.dateRangeMode &&
  a.datePreset === b.datePreset &&
  a.specificPeriod === b.specificPeriod &&
  a.specificValue === b.specificValue;

const cloneFilters = (filters: DAGRunsFilters): DAGRunsFilters => ({
  ...filters,
  labels: [...filters.labels],
});

function dagRunsFilterViewFromView(view: View): DAGRunsFilterView {
  return {
    id: view.id,
    name: view.name,
    pinned: view.pinned ?? false,
    filters: dagRunsFilterSetFromView(view),
  };
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

function supportsIntersectionObserver(): boolean {
  return typeof IntersectionObserver !== 'undefined';
}

function DAGRuns() {
  const location = useLocation();
  const navigate = useNavigate();
  const appBarContext = React.useContext(AppBarContext);
  const config = useConfig();
  const { preferences, updatePreference } = useUserPreferences();
  const searchState = useSearchState();
  const remoteKey = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const workspaceKey = workspaceSelectionKey(workspaceSelection);
  const searchStateScope = JSON.stringify({
    remoteNode: remoteKey,
    workspace: workspaceKey,
  });
  const runViewScope = React.useMemo(
    () => viewScopeForSelection(workspaceSelection),
    [workspaceSelection]
  );
  const canManageRunViews = useCanWriteForWorkspace(runViewScope.workspace);
  const {
    views: sharedRunViews,
    isLoading: runViewsLoading,
    createView,
    updateView,
    deleteView,
  } = useViews(ViewSpecType.run);
  const scopedRunViews = React.useMemo(
    () => sharedRunViews.filter((view) => viewMatchesScope(view, runViewScope)),
    [sharedRunViews, runViewScope]
  );
  const runViews = React.useMemo(
    () => scopedRunViews.map(dagRunsFilterViewFromView),
    [scopedRunViews]
  );
  const defaultRunViewId = scopedRunViews.find((view) => view.isDefault)?.id;
  const [activeRunViewId, setActiveRunViewId] = React.useState<string | null>(
    null
  );
  const [runViewError, setRunViewError] = React.useState<string | null>(null);

  // Extract short datetime format from URL if present
  const parseDateFromUrl = React.useCallback(
    (dateParam: string | null): string | undefined => {
      if (!dateParam) return undefined;

      if (/^\d+$/.test(dateParam)) {
        const timestamp = Number(dateParam);
        if (!Number.isNaN(timestamp)) {
          const parsed =
            config.tzOffsetInSec !== undefined
              ? dayjs.unix(timestamp).utcOffset(config.tzOffsetInSec / 60)
              : dayjs.unix(timestamp);
          return parsed.format('YYYY-MM-DDTHH:mm');
        }
      }

      const match = dateParam.match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2})/);
      if (match) {
        return match[1];
      }

      // If the value already looks like a datetime-local string, normalize length
      if (dateParam.includes('T') && dateParam.length >= 16) {
        return dateParam.slice(0, 16);
      }

      return undefined;
    },
    [config.tzOffsetInSec]
  );
  // Convert datetime to unix timestamp (seconds) for API calls
  const formatDateForApi = (
    dateString: string | undefined
  ): number | undefined => {
    if (!dateString) return undefined;

    // Add seconds if they're missing (datetime-local inputs only have HH:mm)
    const dateWithSeconds =
      dateString.split(':').length < 3 ? `${dateString}:00` : dateString;

    // Apply timezone offset and convert to unix timestamp (seconds)
    if (config.tzOffsetInSec !== undefined) {
      return dayjs(dateWithSeconds)
        .utcOffset(config.tzOffsetInSec / 60)
        .unix();
    } else {
      return dayjs(dateWithSeconds).unix();
    }
  };

  // Default "From" date to the start of current day in the configured timezone
  const getDefaultFromDate = React.useCallback((): string => {
    const now = dayjs();
    // Apply timezone offset and set to beginning of day (00:00)
    const startOfDay =
      config.tzOffsetInSec !== undefined
        ? now.utcOffset(config.tzOffsetInSec / 60).startOf('day')
        : now.startOf('day');
    // Format for datetime-local input (YYYY-MM-DDTHH:mm)
    return startOfDay.format('YYYY-MM-DDTHH:mm');
  }, [config.tzOffsetInSec]);

  const defaultFilters = React.useMemo<DAGRunsFilters>(
    () => ({
      searchText: '',
      dagRunId: '',
      status: 'all',
      labels: [],
      fromDate: getDefaultFromDate(),
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: 'today',
      specificPeriod: 'date',
      specificValue: dayjs().format('YYYY-MM-DD'),
    }),
    [getDefaultFromDate]
  );

  // State for search input, dagRun ID, status, labels, and date ranges
  const [searchText, setSearchText] = React.useState(defaultFilters.searchText);
  const [dagRunId, setDagRunId] = React.useState(defaultFilters.dagRunId);
  const [status, setStatus] = React.useState<string>(defaultFilters.status);
  const [selectedLabels, setSelectedLabels] = React.useState<string[]>(
    defaultFilters.labels
  );
  const [fromDate, setFromDate] = React.useState<string | undefined>(
    defaultFilters.fromDate
  );
  const [toDate, setToDate] = React.useState<string | undefined>(
    defaultFilters.toDate
  );

  // State for API parameters - these will be formatted with timezone
  const [apiSearchText, setAPISearchText] = React.useState(
    defaultFilters.searchText
  );
  const [apiDagRunId, setApiDagRunId] = React.useState(defaultFilters.dagRunId);
  const [apiStatus, setApiStatus] = React.useState(defaultFilters.status);
  const [apiLabels, setApiLabels] = React.useState<string[]>(
    defaultFilters.labels
  );
  const [apiFromDate, setApiFromDate] = React.useState<string | undefined>(
    defaultFilters.fromDate
  );
  const [apiToDate, setApiToDate] = React.useState<string | undefined>(
    defaultFilters.toDate
  );

  // State for selected DAG run in split layout
  const [selectedDAGRun, setSelectedDAGRun] = React.useState<{
    name: string;
    dagRunId: string;
  } | null>(() => {
    const params = new URLSearchParams(location.search);
    const name = params.get('selectedRunName');
    const dagRunId = params.get('selectedRunId');
    return name && dagRunId ? { name, dagRunId } : null;
  });
  const [selectedDAGRunInitialTab, setSelectedDAGRunInitialTab] =
    React.useState<StatusTab>(() =>
      new URLSearchParams(location.search).get('selectedRunTab') === 'artifacts'
        ? 'artifacts'
        : 'status'
    );
  const updateSelectedDAGRun = React.useCallback(
    (
      dagRun: { name: string; dagRunId: string } | null,
      initialTab: StatusTab = 'status',
      replace = false
    ) => {
      setSelectedDAGRun(dagRun);
      setSelectedDAGRunInitialTab(initialTab);
      const params = new URLSearchParams(location.search);
      if (dagRun) {
        params.set('selectedRunName', dagRun.name);
        params.set('selectedRunId', dagRun.dagRunId);
        if (initialTab === 'status') {
          params.delete('selectedRunTab');
        } else {
          params.set('selectedRunTab', initialTab);
        }
      } else {
        params.delete('selectedRunName');
        params.delete('selectedRunId');
        params.delete('selectedRunTab');
      }
      const search = params.toString();
      navigate(
        {
          pathname: location.pathname,
          search: search ? `?${search}` : '',
        },
        { replace }
      );
    },
    [location.pathname, location.search, navigate]
  );

  React.useEffect(() => {
    const params = new URLSearchParams(location.search);
    const name = params.get('selectedRunName');
    const dagRunId = params.get('selectedRunId');
    setSelectedDAGRun(name && dagRunId ? { name, dagRunId } : null);
    setSelectedDAGRunInitialTab(
      params.get('selectedRunTab') === 'artifacts' ? 'artifacts' : 'status'
    );
  }, [location.search]);

  const selectDAGRun = React.useCallback(
    (dagRun: { name: string; dagRunId: string } | null) => {
      updateSelectedDAGRun(dagRun);
    },
    [updateSelectedDAGRun]
  );
  const viewDAGRunArtifacts = React.useCallback(
    (dagRun: { name: string; dagRunId: string }) => {
      updateSelectedDAGRun(dagRun, 'artifacts');
    },
    [updateSelectedDAGRun]
  );
  const loadMoreSentinelRef = React.useRef<HTMLDivElement>(null);
  const autoLoadPendingRef = React.useRef(false);

  // View mode comes from user preferences (local storage)
  const viewMode = preferences.dagRunsViewMode;

  // Date range mode: 'preset', 'specific', or 'custom'
  const [dateRangeMode, setDateRangeMode] = React.useState<
    'preset' | 'specific' | 'custom'
  >(defaultFilters.dateRangeMode);
  const [datePreset, setDatePreset] = React.useState<string>(
    defaultFilters.datePreset
  );
  const [specificPeriod, setSpecificPeriod] = React.useState<
    'date' | 'month' | 'year'
  >(defaultFilters.specificPeriod);
  const [specificValue, setSpecificValue] = React.useState<string>(
    defaultFilters.specificValue
  );

  const currentFilters = React.useMemo<DAGRunsFilters>(
    () => ({
      searchText,
      dagRunId,
      status,
      labels: selectedLabels,
      fromDate,
      toDate,
      dateRangeMode,
      datePreset,
      specificPeriod,
      specificValue,
    }),
    [
      searchText,
      dagRunId,
      status,
      selectedLabels,
      fromDate,
      toDate,
      dateRangeMode,
      datePreset,
      specificPeriod,
      specificValue,
    ]
  );

  const currentFiltersRef = React.useRef(currentFilters);
  React.useEffect(() => {
    currentFiltersRef.current = currentFilters;
  }, [currentFilters]);

  const lastPersistedFiltersRef = React.useRef<DAGRunsFilters | null>(null);

  const getPresetDates = React.useCallback(
    (preset: string): { from: string; to?: string } => {
      const now = dayjs();
      const startOfDay =
        config.tzOffsetInSec !== undefined
          ? now.utcOffset(config.tzOffsetInSec / 60).startOf('day')
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
    },
    [config.tzOffsetInSec]
  );

  const getSpecificPeriodDates = React.useCallback(
    (
      period: 'date' | 'month' | 'year',
      value: string
    ): { from: string; to?: string } => {
      switch (period) {
        case 'date': {
          const date = dayjs(value);
          return {
            from: date.startOf('day').format('YYYY-MM-DDTHH:mm'),
            to: date.endOf('day').format('YYYY-MM-DDTHH:mm'),
          };
        }
        case 'month': {
          const date = dayjs(value);
          return {
            from: date.startOf('month').format('YYYY-MM-DDTHH:mm'),
            to: date.endOf('month').format('YYYY-MM-DDTHH:mm'),
          };
        }
        case 'year': {
          const date = dayjs(value);
          return {
            from: date.startOf('year').format('YYYY-MM-DDTHH:mm'),
            to: date.endOf('year').format('YYYY-MM-DDTHH:mm'),
          };
        }
      }
    },
    []
  );

  // Saved run views store relative date filters (preset or specific value);
  // resolve them to concrete dates whenever the view is applied or compared.
  const resolveRunViewFilters = React.useCallback(
    (filters: DAGRunsFilterSet): DAGRunsFilterSet => {
      if (filters.dateRangeMode === 'preset') {
        const dates = getPresetDates(filters.datePreset);
        return { ...filters, fromDate: dates.from, toDate: dates.to };
      }
      if (filters.dateRangeMode === 'specific') {
        const dates = getSpecificPeriodDates(
          filters.specificPeriod,
          filters.specificValue
        );
        return { ...filters, fromDate: dates.from, toDate: dates.to };
      }
      return {
        ...filters,
        fromDate: filters.fromDate ?? defaultFilters.fromDate,
      };
    },
    [defaultFilters, getPresetDates, getSpecificPeriodDates]
  );

  const previousRunScopeRef = React.useRef(searchStateScope);

  React.useEffect(() => {
    if (runViewsLoading) {
      return;
    }

    // URL parameters belong to the previous workspace when the scope has
    // just changed; drop them and start from the destination's default view
    // (or All runs), so another workspace's filters cannot leak in. The ref
    // is only advanced once the cleanup actually runs, so a scope change
    // during view loading is still honored once loading completes.
    const scopeChanged = previousRunScopeRef.current !== searchStateScope;
    if (scopeChanged) {
      previousRunScopeRef.current = searchStateScope;
      setRunViewError(null);
      const clean = new URLSearchParams();
      clean.set('view', defaultRunViewId ?? ALL_RUNS_VIEW_PARAM);
      navigate(
        { pathname: location.pathname, search: `?${clean.toString()}` },
        { replace: true }
      );
      return;
    }

    const params = new URLSearchParams(location.search);
    const stored = searchState.readState<DAGRunsFilters>(
      'dagRuns',
      searchStateScope
    );

    const urlFilters: Partial<DAGRunsFilters> = {};
    let hasUrlFilters = false;

    if (params.has('name')) {
      urlFilters.searchText = params.get('name') ?? '';
      hasUrlFilters = true;
    }

    if (params.has('dagRunId')) {
      urlFilters.dagRunId = params.get('dagRunId') ?? '';
      hasUrlFilters = true;
    }

    if (params.has('status')) {
      urlFilters.status = params.get('status') || 'all';
      hasUrlFilters = true;
    }

    if (params.has('labels') || params.has('tags')) {
      const labelsParam = params.get('labels') ?? params.get('tags') ?? '';
      urlFilters.labels = labelsParam
        ? labelsParam
            .split(',')
            .map((t) => t.trim().toLowerCase())
            .filter((t) => t !== '')
            .filter((t) => withoutWorkspaceLabels([t]).length > 0)
        : [];
      hasUrlFilters = true;
    }

    const dateModeParam = params.get('dateMode');
    if (
      dateModeParam === 'preset' ||
      dateModeParam === 'specific' ||
      dateModeParam === 'custom'
    ) {
      urlFilters.dateRangeMode = dateModeParam;
      hasUrlFilters = true;
    }

    // Concrete dates are only meaningful for a custom range; preset and
    // specific modes keep their relative parameters and derive dates fresh.
    // Legacy URLs may carry concrete dates without a dateMode at all — no
    // relative preset can reproduce them, so they are honored as a custom
    // range.
    const usesConcreteDates =
      dateModeParam === 'custom' || dateModeParam === null;
    if (usesConcreteDates && params.has('fromDate')) {
      urlFilters.fromDate = parseDateFromUrl(params.get('fromDate'));
      hasUrlFilters = true;
    }

    if (usesConcreteDates && params.has('toDate')) {
      urlFilters.toDate = parseDateFromUrl(params.get('toDate'));
      hasUrlFilters = true;
    }

    // A URL that carries concrete dates without a dateMode represents a
    // custom range: keep that mode so a later search does not re-derive the
    // historical dates from a relative preset.
    if (
      dateModeParam === null &&
      (params.has('fromDate') || params.has('toDate'))
    ) {
      urlFilters.dateRangeMode = 'custom';
      hasUrlFilters = true;
    }

    if (params.has('preset')) {
      urlFilters.datePreset = params.get('preset') || 'today';
      hasUrlFilters = true;
    }

    const specificPeriodParam = params.get('specificPeriod');
    if (
      specificPeriodParam === 'date' ||
      specificPeriodParam === 'month' ||
      specificPeriodParam === 'year'
    ) {
      urlFilters.specificPeriod = specificPeriodParam;
      hasUrlFilters = true;
    }

    if (params.has('specificValue')) {
      urlFilters.specificValue =
        params.get('specificValue') || defaultFilters.specificValue;
      hasUrlFilters = true;
    }

    let base: DAGRunsFilters = {
      ...defaultFilters,
      ...(stored ?? {}),
    };
    let nextActiveRunViewId: string | null = null;
    const requestedViewId = params.get('view');
    const requestedView =
      requestedViewId === ALL_RUNS_VIEW_PARAM
        ? undefined
        : runViews.find((view) => view.id === requestedViewId);
    const defaultView =
      runViews.find((view) => view.id === defaultRunViewId) ?? undefined;

    if (requestedViewId === ALL_RUNS_VIEW_PARAM) {
      base = cloneFilters(defaultFilters);
    } else if (requestedView) {
      base = resolveRunViewFilters(requestedView.filters);
      nextActiveRunViewId = requestedView.id;
    } else if (!hasUrlFilters && defaultView) {
      base = resolveRunViewFilters(defaultView.filters);
      nextActiveRunViewId = defaultView.id;
    }

    const next = hasUrlFilters ? { ...base, ...urlFilters } : base;
    // Preset and specific modes define their range relative to "now".
    // Resolve the merged filters so standalone preset/specific URLs derive
    // fresh dates instead of falling back to the default range. Legacy URLs
    // without a dateMode keep their concrete dates.
    const resolved =
      dateModeParam === 'preset' || dateModeParam === 'specific'
        ? resolveRunViewFilters(next)
        : next;
    const current = currentFiltersRef.current;

    setActiveRunViewId(nextActiveRunViewId);

    if (current && areFiltersEqual(current, resolved)) {
      if (hasUrlFilters) {
        lastPersistedFiltersRef.current = resolved;
        searchState.writeState('dagRuns', searchStateScope, resolved);
      }
      return;
    }

    setSearchText(resolved.searchText);
    setDagRunId(resolved.dagRunId);
    setStatus(resolved.status);
    setSelectedLabels(resolved.labels);
    setFromDate(resolved.fromDate);
    setToDate(resolved.toDate);
    setDateRangeMode(resolved.dateRangeMode);
    setDatePreset(resolved.datePreset);
    setSpecificPeriod(resolved.specificPeriod);
    setSpecificValue(resolved.specificValue);

    setAPISearchText(resolved.searchText);
    setApiDagRunId(resolved.dagRunId);
    setApiStatus(resolved.status);
    setApiLabels(resolved.labels);
    setApiFromDate(resolved.fromDate);
    setApiToDate(resolved.toDate);

    lastPersistedFiltersRef.current = resolved;
    searchState.writeState('dagRuns', searchStateScope, resolved);
  }, [
    defaultFilters,
    defaultRunViewId,
    location.search,
    navigate,
    parseDateFromUrl,
    resolveRunViewFilters,
    runViews,
    runViewsLoading,
    searchState,
    searchStateScope,
  ]);

  React.useEffect(() => {
    // Persistence must wait for the URL/view restoration to complete:
    // writing the initial default filters before stored state is restored
    // would clobber the session's filters.
    if (runViewsLoading) {
      return;
    }
    const persisted = lastPersistedFiltersRef.current;
    if (persisted && areFiltersEqual(persisted, currentFilters)) {
      return;
    }
    lastPersistedFiltersRef.current = currentFilters;
    searchState.writeState('dagRuns', searchStateScope, currentFilters);
  }, [currentFilters, runViewsLoading, searchState, searchStateScope]);

  React.useEffect(() => {
    appBarContext.setTitle('Executions');
  }, [appBarContext]);

  // Fetch available labels for the filter dropdown
  const { data: labelsData } = useQuery(
    '/dags/labels',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
          ...workspaceQuery,
        },
      },
    },
    {
      revalidateOnFocus: false,
      revalidateIfStale: false,
    }
  );
  const availableLabels = React.useMemo(
    () => withoutWorkspaceLabels(labelsData?.labels ?? []),
    [labelsData?.labels]
  );

  const dagRunQuery = React.useMemo(
    () => ({
      remoteNode: appBarContext.selectedRemoteNode || 'local',
      name: apiSearchText || undefined,
      dagRunId: apiDagRunId || undefined,
      status: apiStatus !== 'all' ? [parseInt(apiStatus)] : undefined,
      labels: apiLabels.length > 0 ? apiLabels.join(',') : undefined,
      fromDate: formatDateForApi(apiFromDate),
      toDate: formatDateForApi(apiToDate),
      limit: 100,
      ...workspaceQuery,
    }),
    [
      apiDagRunId,
      apiFromDate,
      apiSearchText,
      apiStatus,
      apiLabels,
      apiToDate,
      appBarContext.selectedRemoteNode,
      workspaceQuery,
    ]
  );
  const {
    dagRuns,
    isInitialLoading,
    isLoadingMore,
    loadMoreError,
    hasMore,
    refresh: refreshDagRuns,
    loadMore: handleLoadMore,
  } = usePaginatedDAGRuns({
    query: dagRunQuery,
  });
  React.useEffect(() => {
    if (!isLoadingMore) {
      autoLoadPendingRef.current = false;
    }
  }, [isLoadingMore]);
  const canAutoLoadMore = supportsIntersectionObserver();
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
  const {
    clearSelection,
    replaceSelection,
    selectAllLoaded,
    selectedKeys,
    selectedRuns,
    toggleSelection,
  } = useBulkDAGRunSelection(dagRuns);

  const updateSearchParams = (updates: Record<string, string | undefined>) => {
    const params = new URLSearchParams(location.search);
    if (!('view' in updates) && activeRunViewId) {
      params.set('view', activeRunViewId);
    }
    if ('labels' in updates) {
      params.delete('tags');
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
    activeRunViewId !== null ? value : value.length > 0 ? value : undefined;

  const handleSearch = (overrideStatus?: string) => {
    // Use override status if provided, otherwise use current status
    const statusToUse = overrideStatus !== undefined ? overrideStatus : status;

    // Update API state with values
    setAPISearchText(searchText);
    setApiDagRunId(dagRunId);
    setApiStatus(statusToUse);
    setApiLabels(selectedLabels);
    setApiFromDate(fromDate);
    setApiToDate(toDate);

    updateSearchParams({
      name: searchOverrideKey(searchText),
      dagRunId: searchOverrideKey(dagRunId),
      status: statusToUse,
      labels: searchOverrideKey(selectedLabels.join(',')),
      fromDate,
      toDate,
      dateMode: dateRangeMode,
      preset: dateRangeMode === 'preset' ? datePreset : undefined,
      specificValue: dateRangeMode === 'specific' ? specificValue : undefined,
      specificPeriod: dateRangeMode === 'specific' ? specificPeriod : undefined,
    });
  };

  const applyResolvedFilters = React.useCallback((filters: DAGRunsFilters) => {
    setSearchText(filters.searchText);
    setDagRunId(filters.dagRunId);
    setStatus(filters.status);
    setSelectedLabels(filters.labels);
    setFromDate(filters.fromDate);
    setToDate(filters.toDate);
    setDateRangeMode(filters.dateRangeMode);
    setDatePreset(filters.datePreset);
    setSpecificPeriod(filters.specificPeriod);
    setSpecificValue(filters.specificValue);
    setAPISearchText(filters.searchText);
    setApiDagRunId(filters.dagRunId);
    setApiStatus(filters.status);
    setApiLabels(filters.labels);
    setApiFromDate(filters.fromDate);
    setApiToDate(filters.toDate);
  }, []);

  const applyRunView = React.useCallback(
    (view: DAGRunsFilterView) => {
      setRunViewError(null);
      const params = new URLSearchParams(location.search);
      const filters = resolveRunViewFilters(view.filters);
      // Apply the filters directly: when the resulting URL is unchanged
      // (for example resetting a view that was selected from the dropdown),
      // the restoration effect has no location change to react to.
      applyResolvedFilters(filters);
      for (const key of RUN_FILTER_QUERY_KEYS) {
        params.delete(key);
      }
      params.set('view', view.id);
      if (filters.searchText) {
        params.set('name', filters.searchText);
      }
      if (filters.dagRunId) {
        params.set('dagRunId', filters.dagRunId);
      }
      if (filters.status && filters.status !== 'all') {
        params.set('status', filters.status);
      }
      if (filters.labels.length > 0) {
        params.set('labels', filters.labels.join(','));
      }
      params.set('dateMode', filters.dateRangeMode);
      if (filters.dateRangeMode === 'preset') {
        params.set('preset', filters.datePreset);
      } else if (filters.dateRangeMode === 'specific') {
        params.set('specificValue', filters.specificValue);
        params.set('specificPeriod', filters.specificPeriod);
      } else {
        // Only a custom range persists concrete dates; preset and specific
        // modes derive them whenever the view is applied.
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
      location.pathname,
      location.search,
      navigate,
      resolveRunViewFilters,
    ]
  );

  const handleSelectRunView = (viewId: string) => {
    const view = runViews.find((item) => item.id === viewId);
    if (view) {
      applyRunView(view);
    }
  };

  const handleShowAllRuns = () => {
    setRunViewError(null);
    // Same rationale as applyRunView: the target URL may already be active,
    // so restore the default filters directly.
    applyResolvedFilters(cloneFilters(defaultFilters));
    const params = new URLSearchParams(location.search);
    for (const key of RUN_FILTER_QUERY_KEYS) {
      params.delete(key);
    }
    params.set('view', ALL_RUNS_VIEW_PARAM);
    const search = params.toString();
    navigate(
      { pathname: location.pathname, search: search ? `?${search}` : '' },
      { replace: true }
    );
  };

  const handleResetRunView = () => {
    const view = runViews.find((item) => item.id === activeRunViewId);
    if (view) {
      applyRunView(view);
    }
  };

  const handleSaveRunView = async (
    name: string,
    makeDefault: boolean,
    pinned: boolean
  ): Promise<void> => {
    const filters = cloneFilters(currentFiltersRef.current);
    setRunViewError(null);
    try {
      const view = await createView(
        buildRunViewSpec(name, filters, makeDefault, pinned, runViewScope)
      );
      applyRunView(dagRunsFilterViewFromView(view));
    } catch (error) {
      setRunViewError(
        error instanceof Error ? error.message : 'Failed to save run view'
      );
      throw error;
    }
  };

  const handleUpdateRunView = async (): Promise<void> => {
    const view = scopedRunViews.find((item) => item.id === activeRunViewId);
    if (!view) {
      return;
    }
    const filters = cloneFilters(currentFiltersRef.current);
    setRunViewError(null);
    try {
      const updated = await updateView(
        view.id,
        buildRunViewSpec(
          view.name,
          filters,
          view.isDefault ?? false,
          view.pinned ?? false,
          runViewScope
        )
      );
      applyRunView(dagRunsFilterViewFromView(updated));
    } catch (error) {
      setRunViewError(
        error instanceof Error ? error.message : 'Failed to update run view'
      );
      throw error;
    }
  };

  const handleSetDefaultRunView = async (
    viewId: string | undefined
  ): Promise<void> => {
    const target = scopedRunViews.find(
      (view) => view.id === (viewId ?? defaultRunViewId)
    );
    if (!target) {
      return;
    }
    setRunViewError(null);
    try {
      await updateView(
        target.id,
        buildRunViewSpec(
          target.name,
          dagRunsFilterSetFromView(target),
          viewId !== undefined,
          target.pinned ?? false,
          runViewScope
        )
      );
    } catch (error) {
      setRunViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update the default run view'
      );
      throw error;
    }
  };

  const handleSetPinnedRunView = async (
    viewId: string,
    pinned: boolean
  ): Promise<void> => {
    const target = scopedRunViews.find((view) => view.id === viewId);
    if (!target) {
      return;
    }
    setRunViewError(null);
    try {
      await updateView(
        target.id,
        buildRunViewSpec(
          target.name,
          dagRunsFilterSetFromView(target),
          target.isDefault ?? false,
          pinned,
          runViewScope
        )
      );
    } catch (error) {
      setRunViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update the starred run view'
      );
      throw error;
    }
  };

  const handleDeleteRunView = async (viewId: string): Promise<void> => {
    const deletingActiveView = viewId === activeRunViewId;
    setRunViewError(null);
    try {
      await deleteView(viewId);
      if (deletingActiveView) {
        handleShowAllRuns();
      }
    } catch (error) {
      setRunViewError(
        error instanceof Error ? error.message : 'Failed to delete run view'
      );
      throw error;
    }
  };

  const handleNameInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSearchText(e.target.value);
  };

  const handleDagRunIdInputChange = (
    e: React.ChangeEvent<HTMLInputElement>
  ) => {
    setDagRunId(e.target.value);
  };

  const handleStatusChange = (value: string) => {
    setStatus(value);
    // Automatically trigger search when status changes
    handleSearch(value);
  };

  const updateLabels = (newLabels: string[]) => {
    setSelectedLabels(newLabels);
    setApiLabels(newLabels);
    updateSearchParams({
      labels: searchOverrideKey(newLabels.join(',')),
    });
  };

  const handleViewModeChange = (value: string) => {
    const newViewMode = value as 'list' | 'grouped';
    updatePreference('dagRunsViewMode', newViewMode);
  };

  const handleDatePresetChange = (preset: string) => {
    setDatePreset(preset);
    const dates = getPresetDates(preset);
    setFromDate(dates.from);
    setToDate(dates.to);
    setApiFromDate(dates.from);
    setApiToDate(dates.to);
    updateSearchParams({
      preset,
      dateMode: 'preset',
      fromDate: dates.from,
      toDate: dates.to,
    });
  };

  const getInputTypeForPeriod = (period: 'date' | 'month' | 'year'): string => {
    switch (period) {
      case 'date':
        return 'date';
      case 'month':
        return 'month';
      case 'year':
        return 'number';
    }
  };

  const handleSpecificPeriodChange = (
    value: string,
    period?: 'date' | 'month' | 'year'
  ) => {
    setSpecificValue(value);
    const periodToUse = period || specificPeriod;
    const dates = getSpecificPeriodDates(periodToUse, value);
    setFromDate(dates.from);
    setToDate(dates.to);
    setApiFromDate(dates.from);
    setApiToDate(dates.to);
    updateSearchParams({
      specificValue: value,
      specificPeriod: periodToUse,
      dateMode: 'specific',
      fromDate: dates.from,
      toDate: dates.to,
    });
  };

  const handleDateRangeModeChange = (
    newMode: 'preset' | 'specific' | 'custom'
  ) => {
    setDateRangeMode(newMode);

    if (newMode === 'preset') {
      // Apply current preset
      const dates = getPresetDates(datePreset);
      setFromDate(dates.from);
      setToDate(dates.to);
      setApiFromDate(dates.from);
      setApiToDate(dates.to);
      updateSearchParams({
        dateMode: newMode,
        preset: datePreset,
        fromDate: dates.from,
        toDate: dates.to,
        specificValue: undefined,
        specificPeriod: undefined,
      });
    } else if (newMode === 'specific') {
      // Apply current specific period value
      const dates = getSpecificPeriodDates(specificPeriod, specificValue);
      setFromDate(dates.from);
      setToDate(dates.to);
      setApiFromDate(dates.from);
      setApiToDate(dates.to);
      updateSearchParams({
        dateMode: newMode,
        specificPeriod,
        specificValue,
        fromDate: dates.from,
        toDate: dates.to,
        preset: undefined,
      });
    } else {
      updateSearchParams({
        dateMode: newMode,
        preset: undefined,
        specificValue: undefined,
        specificPeriod: undefined,
      });
    }
  };

  const handleInputKeyPress = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      handleSearch();
    }
  };

  // Format timezone offset for display
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

  const tzLabel = formatTimezoneOffset();

  const activeRunView = runViews.find((view) => view.id === activeRunViewId);
  const isRunViewEdited = activeRunView
    ? !areFiltersEqual(
        resolveRunViewFilters(activeRunView.filters),
        currentFilters
      )
    : false;
  const isAllRunsView =
    activeRunViewId === null && areFiltersEqual(currentFilters, defaultFilters);

  return (
    <div className="max-w-7xl">
      <div className="flex items-center justify-between mb-2">
        <div className="flex min-w-0 items-center gap-3">
          <Title>
            <I18nText text={'Executions'} />
          </Title>
          <ViewSelector
            kind="run"
            views={runViews}
            activeViewId={activeRunViewId}
            defaultViewId={defaultRunViewId}
            isAllView={isAllRunsView}
            isActiveViewEdited={isRunViewEdited}
            canManageViews={canManageRunViews}
            error={runViewError}
            onSelectView={handleSelectRunView}
            onShowAll={handleShowAllRuns}
            onResetView={handleResetRunView}
            onSaveView={handleSaveRunView}
            onUpdateView={handleUpdateRunView}
            onSetDefault={handleSetDefaultRunView}
            onSetPinned={handleSetPinnedRunView}
            onDeleteView={handleDeleteRunView}
          />
        </div>
        <I18nProps>
          <ToggleGroup aria-label="View mode" className="h-9 p-0.5">
            <I18nProps>
              <ToggleButton
                value="list"
                groupValue={viewMode}
                onClick={() => handleViewModeChange('list')}
                position="first"
                aria-label="List view"
                className="h-8 px-3"
              >
                <List size={16} className="mr-1.5" />
                <I18nText text={'List'} />
              </ToggleButton>
            </I18nProps>
            <I18nProps>
              <ToggleButton
                value="grouped"
                groupValue={viewMode}
                onClick={() => handleViewModeChange('grouped')}
                position="last"
                aria-label="Grouped view"
                className="h-8 px-3"
              >
                <Layers size={16} className="mr-1.5" />
                <I18nText text={'Grouped'} />
              </ToggleButton>
            </I18nProps>
          </ToggleGroup>
        </I18nProps>
      </div>
      <div>
        <div className="mb-3 space-y-3 rounded-lg border border-border bg-card/50 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <I18nProps>
              <Input
                placeholder="Filter by DAG name..."
                value={searchText}
                onChange={handleNameInputChange}
                onKeyDown={handleInputKeyPress}
                className="w-[200px]"
              />
            </I18nProps>
            <I18nProps>
              <Input
                placeholder="Filter by Run ID..."
                value={dagRunId}
                onChange={handleDagRunIdInputChange}
                onKeyDown={handleInputKeyPress}
                className="w-[180px]"
              />
            </I18nProps>
            <Select value={status} onValueChange={handleStatusChange}>
              <I18nProps>
                <SelectTrigger aria-label="Status" className="w-[150px]">
                  <I18nProps>
                    <SelectValue placeholder="Status">
                      <StatusSelectDisplay status={status} />
                    </SelectValue>
                  </I18nProps>
                </SelectTrigger>
              </I18nProps>
              <SelectContent>
                <SelectItem value="all">
                  <div className="inline-flex items-center rounded-full border bg-muted border-border text-foreground py-0.5 px-2 text-xs font-medium">
                    <I18nText text={'All Statuses'} />
                  </div>
                </SelectItem>
                {Object.entries(STATUS_CONFIG).map(([statusValue, label]) => (
                  <SelectItem key={statusValue} value={statusValue}>
                    <StatusChip
                      status={Number(statusValue) as Status}
                      size="sm"
                    >
                      <I18nText text={label} />
                    </StatusChip>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {/* Labels filter */}
            <I18nProps>
              <LabelCombobox
                selectedLabels={selectedLabels}
                onLabelsChange={updateLabels}
                availableLabels={availableLabels}
                placeholder="Filter by labels..."
                className="h-9 min-w-[170px] max-w-[220px]"
              />
            </I18nProps>
            <Button onClick={() => handleSearch()} className="px-4 font-medium">
              <Search className="mr-1.5 h-4 w-4" />
              <I18nText text={'Search'} />
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <I18nProps>
              <ToggleGroup aria-label="Date range mode" className="h-9 p-0.5">
                <I18nProps>
                  <ToggleButton
                    value="preset"
                    groupValue={dateRangeMode}
                    onClick={() => handleDateRangeModeChange('preset')}
                    position="first"
                    aria-label="Quick select"
                    className="h-8 px-3"
                  >
                    <I18nText text={'Quick'} />
                  </ToggleButton>
                </I18nProps>
                <I18nProps>
                  <ToggleButton
                    value="specific"
                    groupValue={dateRangeMode}
                    onClick={() => handleDateRangeModeChange('specific')}
                    position="middle"
                    aria-label="Specific date/month/year"
                    className="h-8 px-3"
                  >
                    <I18nText text={'Specific'} />
                  </ToggleButton>
                </I18nProps>
                <I18nProps>
                  <ToggleButton
                    value="custom"
                    groupValue={dateRangeMode}
                    onClick={() => handleDateRangeModeChange('custom')}
                    position="last"
                    aria-label="Custom range"
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
            ) : dateRangeMode === 'specific' ? (
              <>
                <Select
                  value={specificPeriod}
                  onValueChange={(v) => {
                    const newPeriod = v as 'date' | 'month' | 'year';
                    setSpecificPeriod(newPeriod);
                    let newValue: string;
                    const parsedDate = dayjs(specificValue);

                    if (newPeriod === 'date') {
                      newValue = parsedDate.isValid()
                        ? parsedDate.format('YYYY-MM-DD')
                        : dayjs().format('YYYY-MM-DD');
                    } else if (newPeriod === 'month') {
                      newValue = parsedDate.isValid()
                        ? parsedDate.format('YYYY-MM')
                        : dayjs().format('YYYY-MM');
                    } else {
                      newValue = parsedDate.isValid()
                        ? parsedDate.format('YYYY')
                        : dayjs().format('YYYY');
                    }

                    setSpecificValue(newValue);
                    handleSpecificPeriodChange(newValue, newPeriod);
                  }}
                >
                  <I18nProps>
                    <SelectTrigger
                      aria-label="Specific period"
                      className="w-[120px]"
                    >
                      <SelectValue />
                    </SelectTrigger>
                  </I18nProps>
                  <SelectContent>
                    <SelectItem value="date">
                      <I18nText text={'Date'} />
                    </SelectItem>
                    <SelectItem value="month">
                      <I18nText text={'Month'} />
                    </SelectItem>
                    <SelectItem value="year">
                      <I18nText text={'Year'} />
                    </SelectItem>
                  </SelectContent>
                </Select>
                <Input
                  type={getInputTypeForPeriod(specificPeriod)}
                  value={specificValue}
                  onChange={(e) => handleSpecificPeriodChange(e.target.value)}
                  placeholder={specificPeriod === 'year' ? 'YYYY' : undefined}
                  min={specificPeriod === 'year' ? '2000' : undefined}
                  max={specificPeriod === 'year' ? '2100' : undefined}
                  className="h-9 w-[160px]"
                />
              </>
            ) : (
              <DateRangePicker
                fromDate={fromDate}
                toDate={toDate}
                onFromDateChange={setFromDate}
                onToDateChange={setToDate}
                onEnterPress={() => handleSearch()}
                fromLabel={`From ${tzLabel}`}
                toLabel={`To ${tzLabel}`}
                className="w-full md:w-auto"
              />
            )}
          </div>
        </div>
        <DAGRunBatchActions
          selectedRuns={selectedRuns}
          loadedCount={dagRuns.length}
          onSelectAllLoaded={selectAllLoaded}
          onClearSelection={clearSelection}
          onReplaceSelection={replaceSelection}
          onActionComplete={refreshDagRuns}
        />
        {viewMode === 'list' ? (
          <DAGRunTable
            dagRuns={dagRuns}
            isLoading={isInitialLoading}
            selectedDAGRun={selectedDAGRun}
            onSelectDAGRun={selectDAGRun}
            onViewArtifacts={viewDAGRunArtifacts}
            selectedRunKeys={selectedKeys}
            onToggleBulkSelect={toggleSelection}
          />
        ) : (
          <DAGRunGroupedView
            dagRuns={dagRuns}
            isLoading={isInitialLoading}
            selectedDAGRun={selectedDAGRun}
            onSelectDAGRun={selectDAGRun}
            onViewArtifacts={viewDAGRunArtifacts}
            selectedRunKeys={selectedKeys}
            onToggleBulkSelect={toggleSelection}
          />
        )}
        <div className="mt-3 flex flex-col items-center gap-2">
          {loadMoreError && (
            <div className="text-sm text-error">{loadMoreError}</div>
          )}
          {hasMore ? (
            <>
              <div ref={loadMoreSentinelRef} className="h-4 w-full" />
              {isLoadingMore ? (
                <div className="text-sm text-muted-foreground">
                  <I18nText text={'Loading more DAG runs...'} />
                </div>
              ) : (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void handleLoadMore()}
                >
                  {loadMoreError ? (
                    <I18nText text={'Retry loading more'} />
                  ) : (
                    <I18nText text={'Load more'} />
                  )}
                </Button>
              )}
            </>
          ) : dagRuns.length > 0 ? (
            <div className="text-sm text-muted-foreground">
              <I18nText text={'All loaded DAG runs are displayed.'} />
            </div>
          ) : null}
        </div>
      </div>

      {/* Side Modal for DAG Run Details */}
      {selectedDAGRun && (
        <DAGRunDetailsModal
          name={selectedDAGRun.name}
          dagRunId={selectedDAGRun.dagRunId}
          isOpen={!!selectedDAGRun}
          onClose={() => updateSelectedDAGRun(null, 'status', true)}
          initialTab={selectedDAGRunInitialTab}
        />
      )}
    </div>
  );
}

export default DAGRuns;
