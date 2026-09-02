// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from 'dayjs';
import { Layers, List, Search } from 'lucide-react';
import React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Status } from '../../api/v1/schema';
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
import { useConfig } from '../../contexts/ConfigContext';
import { useSearchState } from '../../contexts/SearchStateContext';
import { useUserPreferences } from '../../contexts/UserPreference';
import DAGRunBatchActions from '../../features/dag-runs/components/common/DAGRunBatchActions';
import { DAGRunDetailsModal } from '../../features/dag-runs/components/dag-run-details';
import DAGRunGroupedView from '../../features/dag-runs/components/dag-run-list/DAGRunGroupedView';
import DAGRunTable from '../../features/dag-runs/components/dag-run-list/DAGRunTable';
import { usePaginatedDAGRuns } from '../../features/dag-runs/hooks/dagRunPagination';
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

type DAGRunsFilters = {
  searchText: string;
  dagRunId: string;
  status: string;
  labels: string[];
  fromDate?: string;
  toDate?: string;
  dateRangeMode: 'preset' | 'specific' | 'custom';
  datePreset: string;
  specificPeriod: 'date' | 'month' | 'year';
  specificValue: string;
};

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

  React.useEffect(() => {
    const params = new URLSearchParams(location.search);
    const stored = searchState.readState<DAGRunsFilters>(
      'dagRuns',
      searchStateScope
    );
    const base: DAGRunsFilters = {
      ...defaultFilters,
      ...(stored ?? {}),
    };

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

    if (params.has('fromDate')) {
      urlFilters.fromDate = parseDateFromUrl(params.get('fromDate'));
      hasUrlFilters = true;
    }

    if (params.has('toDate')) {
      urlFilters.toDate = parseDateFromUrl(params.get('toDate'));
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

    const next = hasUrlFilters ? { ...base, ...urlFilters } : base;
    const current = currentFiltersRef.current;

    if (current && areFiltersEqual(current, next)) {
      if (hasUrlFilters) {
        lastPersistedFiltersRef.current = next;
        searchState.writeState('dagRuns', searchStateScope, next);
      }
      return;
    }

    setSearchText(next.searchText);
    setDagRunId(next.dagRunId);
    setStatus(next.status);
    setSelectedLabels(next.labels);
    setFromDate(next.fromDate);
    setToDate(next.toDate);
    setDateRangeMode(next.dateRangeMode);
    setDatePreset(next.datePreset);
    setSpecificPeriod(next.specificPeriod);
    setSpecificValue(next.specificValue);

    setAPISearchText(next.searchText);
    setApiDagRunId(next.dagRunId);
    setApiStatus(next.status);
    setApiLabels(next.labels);
    setApiFromDate(next.fromDate);
    setApiToDate(next.toDate);

    lastPersistedFiltersRef.current = next;
    searchState.writeState('dagRuns', searchStateScope, next);
  }, [
    defaultFilters,
    location.search,
    parseDateFromUrl,
    searchState,
    searchStateScope,
  ]);

  React.useEffect(() => {
    const persisted = lastPersistedFiltersRef.current;
    if (persisted && areFiltersEqual(persisted, currentFilters)) {
      return;
    }
    lastPersistedFiltersRef.current = currentFilters;
    searchState.writeState('dagRuns', searchStateScope, currentFilters);
  }, [currentFilters, searchState, searchStateScope]);

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
    if ('labels' in updates) {
      params.delete('tags');
    }
    for (const [key, value] of Object.entries(updates)) {
      if (value) {
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
      name: searchText,
      dagRunId,
      status: statusToUse,
      labels: selectedLabels.length > 0 ? selectedLabels.join(',') : undefined,
      fromDate,
      toDate,
      dateMode: dateRangeMode,
      preset: dateRangeMode === 'preset' ? datePreset : undefined,
      specificValue: dateRangeMode === 'specific' ? specificValue : undefined,
      specificPeriod: dateRangeMode === 'specific' ? specificPeriod : undefined,
    });
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
      labels: newLabels.length > 0 ? newLabels.join(',') : undefined,
    });
  };

  const handleViewModeChange = (value: string) => {
    const newViewMode = value as 'list' | 'grouped';
    updatePreference('dagRunsViewMode', newViewMode);
  };

  const getPresetDates = (preset: string): { from: string; to?: string } => {
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
        return { from: startOfDay.startOf('week').format('YYYY-MM-DDTHH:mm') };
      case 'thisMonth':
        return { from: startOfDay.startOf('month').format('YYYY-MM-DDTHH:mm') };
      default:
        return { from: startOfDay.format('YYYY-MM-DDTHH:mm') };
    }
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

  const getSpecificPeriodDates = (
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

  return (
    <div className="max-w-7xl">
      <div className="flex items-center justify-between mb-2">
        <Title>
          <I18nText text={'Executions'} />
        </Title>
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
