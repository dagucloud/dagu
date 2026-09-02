// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  ComponentsParametersEventLogPaginationMode,
} from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { DateRangePicker } from '@/components/ui/date-range-picker';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { RefreshButton } from '@/components/ui/refresh-button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanViewEventLogs } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useSearchState } from '@/contexts/SearchStateContext';
import { useClient, useQuery } from '@/hooks/api';
import { FetchError } from '@/lib/fetchJson';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import {
  Activity,
  ExternalLink,
  FileJson,
  FilterX,
  Search,
} from 'lucide-react';
import * as React from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type EventLogEntry = components['schemas']['EventLogEntry'];
type EventLogsResponse = components['schemas']['EventLogsResponse'];

type DateRangeMode = 'preset' | 'specific' | 'custom';
type SpecificPeriod = 'date' | 'month' | 'year';

type EventLogFilters = {
  type: string;
  dagName: string;
  dagRunId: string;
  attemptId: string;
  fromDate?: string;
  toDate?: string;
  dateRangeMode: DateRangeMode;
  datePreset: string;
  specificPeriod: SpecificPeriod;
  specificValue: string;
};

type StoredEventLogState = EventLogFilters;
type EventLogQueryParams = {
  remoteNode: string;
  kind: 'dag_run';
  paginationMode: components['parameters']['EventLogPaginationMode'];
  type?: string;
  dagName?: string;
  dagRunId?: string;
  attemptId?: string;
  startTime?: string;
  endTime?: string;
  limit: number;
};

const PAGE_SIZE = 50;
const DEFAULT_DATE_PRESET = 'last7days';
const SEARCH_STATE_KEY = 'eventLogs';

const EVENT_TYPE_OPTIONS = [
  { value: 'all', label: 'All outcomes' },
  { value: 'dag.run.queued', label: 'Queued' },
  { value: 'dag.run.running', label: 'Running' },
  { value: 'dag.run.succeeded', label: 'Succeeded' },
  {
    value: 'dag.run.partially_succeeded',
    label: 'Partially succeeded',
  },
  { value: 'dag.run.failed', label: 'Failed' },
  { value: 'dag.run.aborted', label: 'Aborted' },
  { value: 'dag.run.waiting', label: 'Waiting' },
  { value: 'dag.run.rejected', label: 'Rejected' },
] as const;

function getOutcomeLabel(type: string, status?: string): string {
  switch (type) {
    case 'dag.run.queued':
      return 'Queued';
    case 'dag.run.running':
      return 'Running';
    case 'dag.run.succeeded':
      return 'Succeeded';
    case 'dag.run.partially_succeeded':
      return 'Partially succeeded';
    case 'dag.run.failed':
      return 'Failed';
    case 'dag.run.aborted':
      return 'Aborted';
    case 'dag.run.waiting':
      return 'Waiting';
    case 'dag.run.rejected':
      return 'Rejected';
    default:
      if (status) {
        return status.replace(/_/g, ' ');
      }
      return type;
  }
}

function getOutcomeVariant(
  type: string
): React.ComponentProps<typeof Badge>['variant'] {
  switch (type) {
    case 'dag.run.queued':
      return 'warning';
    case 'dag.run.running':
      return 'default';
    case 'dag.run.succeeded':
      return 'success';
    case 'dag.run.partially_succeeded':
      return 'warning';
    case 'dag.run.failed':
      return 'error';
    case 'dag.run.aborted':
      return 'cancelled';
    case 'dag.run.waiting':
      return 'warning';
    case 'dag.run.rejected':
      return 'warning';
    default:
      return 'default';
  }
}

function safeStringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2) ?? '';
  } catch {
    return String(value);
  }
}

function getInputTypeForPeriod(period: SpecificPeriod): string {
  switch (period) {
    case 'date':
      return 'date';
    case 'month':
      return 'month';
    case 'year':
      return 'number';
  }
}

function buildRunPath(entry: EventLogEntry): string | null {
  if (!entry.dagName || !entry.dagRunId) {
    return null;
  }
  return `/dag-runs/${encodeURIComponent(entry.dagName)}/${encodeURIComponent(entry.dagRunId)}`;
}

function hasQueryParams(params: URLSearchParams): boolean {
  return Array.from(params.keys()).length > 0;
}

function appendUniqueEntries(
  current: EventLogEntry[],
  next: EventLogEntry[]
): EventLogEntry[] {
  if (next.length === 0) {
    return current;
  }
  const seen = new Set(current.map((entry) => entry.id));
  const merged = [...current];
  for (const entry of next) {
    if (seen.has(entry.id)) {
      continue;
    }
    seen.add(entry.id);
    merged.push(entry);
  }
  return merged;
}

function mergeUniqueEntries(
  head: EventLogEntry[],
  older: EventLogEntry[]
): EventLogEntry[] {
  return appendUniqueEntries(head, older);
}

function getClientErrorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === 'object') {
    const maybeMessage = (error as { message?: unknown }).message;
    if (typeof maybeMessage === 'string' && maybeMessage) {
      return maybeMessage;
    }
  }
  return fallback;
}

function areEventLogFiltersEqual(
  a: EventLogFilters,
  b: EventLogFilters
): boolean {
  return (
    a.type === b.type &&
    a.dagName === b.dagName &&
    a.dagRunId === b.dagRunId &&
    a.attemptId === b.attemptId &&
    a.fromDate === b.fromDate &&
    a.toDate === b.toDate &&
    a.dateRangeMode === b.dateRangeMode &&
    a.datePreset === b.datePreset &&
    a.specificPeriod === b.specificPeriod &&
    a.specificValue === b.specificValue
  );
}

function useEventLogFeed(
  client: ReturnType<typeof useClient>,
  query: EventLogQueryParams,
  isReady: boolean
) {
  const [autoRefresh, setAutoRefresh] = React.useState(true);
  const [lastUpdatedAt, setLastUpdatedAt] = React.useState<Date | null>(null);
  const [olderEntries, setOlderEntries] = React.useState<EventLogEntry[]>([]);
  const [continuationCursorOverride, setContinuationCursorOverride] =
    React.useState<string | null | undefined>(undefined);
  const [isLoadingMore, setIsLoadingMore] = React.useState(false);
  const [loadMoreError, setLoadMoreError] = React.useState<string | null>(null);
  const activeFeedKeyRef = React.useRef('');
  const feedKey = React.useMemo(() => JSON.stringify(query), [query]);

  const { data, error, isLoading, mutate } = useQuery(
    '/event-logs',
    isReady
      ? {
          params: {
            query,
          },
        }
      : null,
    {
      refreshInterval: autoRefresh ? 5000 : 0,
      revalidateOnFocus: true,
      revalidateOnReconnect: true,
    }
  );

  React.useEffect(() => {
    activeFeedKeyRef.current = feedKey;
    setOlderEntries([]);
    setContinuationCursorOverride(undefined);
    setLoadMoreError(null);
    setIsLoadingMore(false);
    setAutoRefresh(true);
  }, [feedKey]);

  const headEntries = data?.entries ?? [];
  const entries = React.useMemo(
    () => mergeUniqueEntries(headEntries, olderEntries),
    [headEntries, olderEntries]
  );
  const headFirstEntryID = headEntries[0]?.id ?? '';
  const headLastEntryID =
    headEntries.length > 0
      ? (headEntries[headEntries.length - 1]?.id ?? '')
      : '';
  const headNextCursor = data?.nextCursor ?? '';
  const currentNextCursor =
    continuationCursorOverride === undefined
      ? (data?.nextCursor ?? null)
      : continuationCursorOverride;
  const hasLoadedMore = continuationCursorOverride !== undefined;
  const hasMoreEntries = currentNextCursor !== null;
  const hasHeadResponse = data !== undefined;
  const isAutoRefreshAvailable = !hasLoadedMore;

  React.useEffect(() => {
    if (hasHeadResponse) {
      setLastUpdatedAt(new Date());
    }
  }, [hasHeadResponse, headFirstEntryID, headLastEntryID, headNextCursor]);

  const handleRefresh = React.useCallback(async () => {
    setOlderEntries([]);
    setContinuationCursorOverride(undefined);
    setLoadMoreError(null);
    setIsLoadingMore(false);
    setAutoRefresh(true);
    await mutate();
  }, [mutate]);

  const handleLoadMore = React.useCallback(async () => {
    if (isLoadingMore || !currentNextCursor) {
      return;
    }

    const requestFeedKey = activeFeedKeyRef.current;
    setIsLoadingMore(true);
    setLoadMoreError(null);
    setAutoRefresh(false);

    const response = await client.GET('/event-logs', {
      params: {
        query: {
          ...query,
          cursor: currentNextCursor,
        },
      },
    });

    if (activeFeedKeyRef.current !== requestFeedKey) {
      return;
    }

    setIsLoadingMore(false);

    if (response.error) {
      setLoadMoreError(
        getClientErrorMessage(response.error, 'Failed to load older events')
      );
      return;
    }

    const pageData = (response.data ?? { entries: [] }) as EventLogsResponse;
    setOlderEntries((prev) =>
      appendUniqueEntries(prev, pageData.entries ?? [])
    );
    setContinuationCursorOverride(pageData.nextCursor ?? null);
  }, [client, currentNextCursor, isLoadingMore, query]);

  return {
    data,
    error,
    isLoading,
    autoRefresh,
    setAutoRefresh,
    lastUpdatedAt,
    entries,
    hasMoreEntries,
    isAutoRefreshAvailable,
    isLoadingMore,
    loadMoreError,
    handleRefresh,
    handleLoadMore,
  };
}

export default function EventLogsPage() {
  const { ts } = useI18n();
  const client = useClient();
  const config = useConfig();
  const canViewEventLogs = useCanViewEventLogs();
  const appBarContext = React.useContext(AppBarContext);
  const searchState = useSearchState();
  const remoteKey = appBarContext.selectedRemoteNode || 'local';
  const [searchParams, setSearchParams] = useSearchParams();
  const searchKey = searchParams.toString();
  const locationSearchParams = React.useMemo(
    () => new URLSearchParams(searchKey),
    [searchKey]
  );

  const getPresetDates = React.useCallback(
    (preset: string): { from: string; to?: string } => {
      const now = dayjs();
      const startOfDay =
        config.tzOffsetInSec !== undefined
          ? now.utcOffset(config.tzOffsetInSec / 60).startOf('day')
          : now.startOf('day');

      switch (preset) {
        case 'today':
          return { from: startOfDay.format('YYYY-MM-DDTHH:mm:ss') };
        case 'yesterday':
          return {
            from: startOfDay.subtract(1, 'day').format('YYYY-MM-DDTHH:mm:ss'),
            to: startOfDay.format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'last30days':
          return {
            from: startOfDay.subtract(30, 'day').format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'thisWeek':
          return {
            from: startOfDay.startOf('week').format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'thisMonth':
          return {
            from: startOfDay.startOf('month').format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'last7days':
        default:
          return {
            from: startOfDay.subtract(7, 'day').format('YYYY-MM-DDTHH:mm:ss'),
          };
      }
    },
    [config.tzOffsetInSec]
  );

  const getSpecificPeriodDates = React.useCallback(
    (period: SpecificPeriod, value: string): { from: string; to?: string } => {
      const parsedDate = dayjs(value);
      if (!parsedDate.isValid()) {
        const fallback =
          config.tzOffsetInSec !== undefined
            ? dayjs().utcOffset(config.tzOffsetInSec / 60)
            : dayjs();
        return { from: fallback.startOf('day').format('YYYY-MM-DDTHH:mm:ss') };
      }

      const date =
        config.tzOffsetInSec !== undefined
          ? parsedDate.utcOffset(config.tzOffsetInSec / 60)
          : parsedDate;
      const unit = period === 'date' ? 'day' : period;
      return {
        from: date.startOf(unit).format('YYYY-MM-DDTHH:mm:ss'),
        to: date.endOf(unit).format('YYYY-MM-DDTHH:mm:ss'),
      };
    },
    [config.tzOffsetInSec]
  );

  const parseDateFromUrl = React.useCallback(
    (value: string | null) => {
      if (!value) {
        return undefined;
      }

      if (/^\d+$/.test(value)) {
        const timestamp = Number(value);
        if (!Number.isNaN(timestamp)) {
          const parsed =
            config.tzOffsetInSec !== undefined
              ? dayjs.unix(timestamp).utcOffset(config.tzOffsetInSec / 60)
              : dayjs.unix(timestamp);
          return parsed.format('YYYY-MM-DDTHH:mm:ss');
        }
      }

      if (value.includes('T') && value.length >= 16) {
        return value;
      }

      return undefined;
    },
    [config.tzOffsetInSec]
  );

  const formatDateForApi = React.useCallback(
    (dateString: string | undefined): string | undefined => {
      if (!dateString) {
        return undefined;
      }
      const dateWithSeconds =
        dateString.split(':').length < 3 ? `${dateString}:00` : dateString;
      if (config.tzOffsetInSec !== undefined) {
        return dayjs(dateWithSeconds)
          .utcOffset(config.tzOffsetInSec / 60, true)
          .toISOString();
      }
      return dayjs(dateWithSeconds).toISOString();
    },
    [config.tzOffsetInSec]
  );

  const formatTimestamp = React.useCallback(
    (timestamp: string): string => {
      const parsed =
        config.tzOffsetInSec !== undefined
          ? dayjs(timestamp).utcOffset(config.tzOffsetInSec / 60)
          : dayjs(timestamp);
      return parsed.format('YYYY-MM-DD HH:mm:ss');
    },
    [config.tzOffsetInSec]
  );

  const formatTimezoneOffset = React.useCallback((): string => {
    if (config.tzOffsetInSec === undefined) {
      return '';
    }
    const offsetInMinutes = config.tzOffsetInSec / 60;
    const hours = Math.floor(Math.abs(offsetInMinutes) / 60);
    const minutes = Math.abs(offsetInMinutes) % 60;
    const sign = offsetInMinutes >= 0 ? '+' : '-';
    return `(${sign}${hours.toString().padStart(2, '0')}:${minutes
      .toString()
      .padStart(2, '0')})`;
  }, [config.tzOffsetInSec]);

  const defaultFilters = React.useMemo<EventLogFilters>(() => {
    const dates = getPresetDates(DEFAULT_DATE_PRESET);
    return {
      type: 'all',
      dagName: '',
      dagRunId: '',
      attemptId: '',
      fromDate: dates.from,
      toDate: dates.to,
      dateRangeMode: 'preset',
      datePreset: DEFAULT_DATE_PRESET,
      specificPeriod: 'date',
      specificValue: dayjs().format('YYYY-MM-DD'),
    };
  }, [getPresetDates]);

  const [draftFilters, setDraftFilters] =
    React.useState<EventLogFilters>(defaultFilters);
  const [appliedFilters, setAppliedFilters] =
    React.useState<EventLogFilters>(defaultFilters);
  const [selectedEvent, setSelectedEvent] =
    React.useState<EventLogEntry | null>(null);
  const [hydratedKey, setHydratedKey] = React.useState('');
  const lastPersistedAppliedFiltersRef = React.useRef<EventLogFilters | null>(
    null
  );

  const buildLocationParams = React.useCallback(
    (filters: EventLogFilters): URLSearchParams => {
      const params = new URLSearchParams();
      if (filters.type && filters.type !== 'all') {
        params.set('type', filters.type);
      }
      if (filters.dagName) {
        params.set('dagName', filters.dagName);
      }
      if (filters.dagRunId) {
        params.set('dagRunId', filters.dagRunId);
      }
      if (filters.attemptId) {
        params.set('attemptId', filters.attemptId);
      }
      if (filters.fromDate) {
        params.set('fromDate', filters.fromDate);
      }
      if (filters.toDate) {
        params.set('toDate', filters.toDate);
      }
      params.set('dateMode', filters.dateRangeMode);
      params.set('preset', filters.datePreset);
      params.set('specificPeriod', filters.specificPeriod);
      params.set('specificValue', filters.specificValue);
      return params;
    },
    []
  );

  const parseLocationState = React.useCallback(
    (params: URLSearchParams): StoredEventLogState => {
      const persisted = searchState.readState<StoredEventLogState>(
        SEARCH_STATE_KEY,
        remoteKey
      );
      const base: StoredEventLogState = {
        ...defaultFilters,
        ...persisted,
      };

      if (!hasQueryParams(params)) {
        return base;
      }

      const next: StoredEventLogState = { ...base };

      if (params.has('type')) {
        next.type = params.get('type') || 'all';
      }
      if (params.has('dagName')) {
        next.dagName = params.get('dagName') || '';
      }
      if (params.has('dagRunId')) {
        next.dagRunId = params.get('dagRunId') || '';
      }
      if (params.has('attemptId')) {
        next.attemptId = params.get('attemptId') || '';
      }

      const parsedFrom = parseDateFromUrl(params.get('fromDate'));
      if (params.has('fromDate')) {
        next.fromDate = parsedFrom;
      }
      const parsedTo = parseDateFromUrl(params.get('toDate'));
      if (params.has('toDate')) {
        next.toDate = parsedTo;
      }

      const dateMode = params.get('dateMode');
      if (
        dateMode === 'preset' ||
        dateMode === 'specific' ||
        dateMode === 'custom'
      ) {
        next.dateRangeMode = dateMode;
      }

      const datePreset = params.get('preset');
      if (datePreset) {
        next.datePreset = datePreset;
      }

      const specificPeriod = params.get('specificPeriod');
      if (
        specificPeriod === 'date' ||
        specificPeriod === 'month' ||
        specificPeriod === 'year'
      ) {
        next.specificPeriod = specificPeriod;
      }

      const specificValue = params.get('specificValue');
      if (specificValue) {
        next.specificValue = specificValue;
      }

      return next;
    },
    [defaultFilters, parseDateFromUrl, remoteKey, searchState]
  );

  React.useEffect(() => {
    appBarContext.setTitle('Events');
  }, [appBarContext]);

  React.useEffect(() => {
    const next = parseLocationState(locationSearchParams);
    setDraftFilters(next);
    setAppliedFilters(next);
    lastPersistedAppliedFiltersRef.current = next;
    searchState.writeState(SEARCH_STATE_KEY, remoteKey, next);
    setHydratedKey(`${remoteKey}:${searchKey}`);
  }, [
    locationSearchParams,
    parseLocationState,
    remoteKey,
    searchKey,
    searchState,
  ]);

  React.useEffect(() => {
    if (hydratedKey !== `${remoteKey}:${searchKey}`) {
      return;
    }
    const persisted = lastPersistedAppliedFiltersRef.current;
    if (persisted && areEventLogFiltersEqual(persisted, appliedFilters)) {
      return;
    }
    lastPersistedAppliedFiltersRef.current = appliedFilters;
    searchState.writeState(SEARCH_STATE_KEY, remoteKey, appliedFilters);
  }, [appliedFilters, hydratedKey, remoteKey, searchKey, searchState]);

  const query = React.useMemo<EventLogQueryParams>(() => {
    return {
      remoteNode: remoteKey,
      kind: 'dag_run',
      paginationMode: ComponentsParametersEventLogPaginationMode.cursor,
      type: appliedFilters.type !== 'all' ? appliedFilters.type : undefined,
      dagName: appliedFilters.dagName || undefined,
      dagRunId: appliedFilters.dagRunId || undefined,
      attemptId: appliedFilters.attemptId || undefined,
      startTime: formatDateForApi(appliedFilters.fromDate),
      endTime: formatDateForApi(appliedFilters.toDate),
      limit: PAGE_SIZE,
    };
  }, [appliedFilters, formatDateForApi, remoteKey]);

  const isReady = hydratedKey === `${remoteKey}:${searchKey}`;

  const updateDraftFilters = React.useCallback(
    (patch: Partial<EventLogFilters>) => {
      setDraftFilters((prev) => ({
        ...prev,
        ...patch,
      }));
    },
    []
  );

  const applyFilters = React.useCallback(
    (nextFilters: EventLogFilters) => {
      setAppliedFilters(nextFilters);
      setSearchParams(buildLocationParams(nextFilters));
    },
    [buildLocationParams, setSearchParams]
  );

  const handleApplyFilters = React.useCallback(() => {
    applyFilters(draftFilters);
  }, [applyFilters, draftFilters]);

  const handleClearFilters = React.useCallback(() => {
    setDraftFilters(defaultFilters);
    setAppliedFilters(defaultFilters);
    setSearchParams(buildLocationParams(defaultFilters));
  }, [buildLocationParams, defaultFilters, setSearchParams]);

  const handleDatePresetChange = React.useCallback(
    (preset: string) => {
      const dates = getPresetDates(preset);
      updateDraftFilters({
        datePreset: preset,
        fromDate: dates.from,
        toDate: dates.to,
      });
    },
    [getPresetDates, updateDraftFilters]
  );

  const handleSpecificPeriodChange = React.useCallback(
    (value: string, period?: SpecificPeriod) => {
      const nextPeriod = period || draftFilters.specificPeriod;
      const dates = getSpecificPeriodDates(nextPeriod, value);
      updateDraftFilters({
        specificValue: value,
        specificPeriod: nextPeriod,
        fromDate: dates.from,
        toDate: dates.to,
      });
    },
    [draftFilters.specificPeriod, getSpecificPeriodDates, updateDraftFilters]
  );

  const handleDateRangeModeChange = React.useCallback(
    (nextMode: DateRangeMode) => {
      if (nextMode === 'preset') {
        const dates = getPresetDates(draftFilters.datePreset);
        updateDraftFilters({
          dateRangeMode: nextMode,
          fromDate: dates.from,
          toDate: dates.to,
        });
        return;
      }
      if (nextMode === 'specific') {
        const dates = getSpecificPeriodDates(
          draftFilters.specificPeriod,
          draftFilters.specificValue
        );
        updateDraftFilters({
          dateRangeMode: nextMode,
          fromDate: dates.from,
          toDate: dates.to,
        });
        return;
      }
      updateDraftFilters({ dateRangeMode: nextMode });
    },
    [
      draftFilters.datePreset,
      draftFilters.specificPeriod,
      draftFilters.specificValue,
      getPresetDates,
      getSpecificPeriodDates,
      updateDraftFilters,
    ]
  );

  const handleSpecificPeriodSelect = React.useCallback(
    (value: string) => {
      const nextPeriod = value as SpecificPeriod;
      const parsedDate = dayjs(draftFilters.specificValue);
      let nextValue: string;

      if (nextPeriod === 'date') {
        nextValue = parsedDate.isValid()
          ? parsedDate.format('YYYY-MM-DD')
          : dayjs().format('YYYY-MM-DD');
      } else if (nextPeriod === 'month') {
        nextValue = parsedDate.isValid()
          ? parsedDate.format('YYYY-MM')
          : dayjs().format('YYYY-MM');
      } else {
        nextValue = parsedDate.isValid()
          ? parsedDate.format('YYYY')
          : dayjs().format('YYYY');
      }

      handleSpecificPeriodChange(nextValue, nextPeriod);
    },
    [draftFilters.specificValue, handleSpecificPeriodChange]
  );

  const handleKeyDown = React.useCallback(
    (event: React.KeyboardEvent<HTMLInputElement>) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        handleApplyFilters();
      }
    },
    [handleApplyFilters]
  );

  const {
    data,
    error,
    isLoading,
    autoRefresh,
    setAutoRefresh,
    lastUpdatedAt,
    entries,
    hasMoreEntries,
    isAutoRefreshAvailable,
    isLoadingMore,
    loadMoreError,
    handleRefresh,
    handleLoadMore,
  } = useEventLogFeed(client, query, isReady);
  const rawEventJson = React.useMemo(
    () => (selectedEvent ? safeStringify(selectedEvent) : ''),
    [selectedEvent]
  );
  const tzLabel = formatTimezoneOffset();

  let errorMessage: string | null = null;
  if (error instanceof FetchError) {
    errorMessage = error.data?.message || error.message;
  } else if (error instanceof Error) {
    errorMessage = error.message;
  } else if (error) {
    errorMessage = 'Failed to load event logs';
  }

  if (!canViewEventLogs) {
    return (
      <div className="flex items-center justify-center h-64">
        <p className="text-muted-foreground">
          <I18nText text={'You do not have permission to access this page.'} />
        </p>
      </div>
    );
  }

  return (
    <>
      <div className="flex flex-col gap-4 max-w-7xl h-full">
        <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
          <div>
            <h1 className="text-lg font-semibold">
              <I18nText text={'Events'} />
            </h1>
            <p className="text-sm text-muted-foreground">
              <I18nText
                text={
                  'Recent DAG-run outcome events for the selected remote node'
                }
              />
            </p>
            <p className="text-xs text-muted-foreground mt-1">
              {lastUpdatedAt ? (
                ts('Last updated {time}', {
                  time: formatTimestamp(lastUpdatedAt.toISOString()),
                })
              ) : (
                <I18nText text="Waiting for the first response" />
              )}
              {autoRefresh && isAutoRefreshAvailable ? (
                <I18nText text={' • Refreshing every 5 seconds'} />
              ) : (
                ''
              )}
              {!isAutoRefreshAvailable ? (
                <I18nText
                  text={
                    ' • Auto-refresh is disabled after loading older events'
                  }
                />
              ) : (
                ''
              )}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              onClick={() => {
                if (isAutoRefreshAvailable) {
                  setAutoRefresh((prev) => !prev);
                }
              }}
              disabled={!isAutoRefreshAvailable}
              aria-label={ts('Auto-refresh {state}', {
                state: ts(autoRefresh ? 'enabled' : 'disabled'),
              })}
              title={
                isAutoRefreshAvailable
                  ? ts('Toggle auto-refresh (currently {state})', {
                      state: ts(autoRefresh ? 'ON' : 'OFF'),
                    })
                  : ts('Auto-refresh is disabled after loading older events')
              }
            >
              <Activity
                className={cn(
                  'h-4 w-4',
                  autoRefresh && isAutoRefreshAvailable && 'text-success'
                )}
              />
              <I18nText text={'Auto:'} />{' '}
              {autoRefresh && isAutoRefreshAvailable ? (
                <I18nText text={'ON'} />
              ) : (
                <I18nText text={'OFF'} />
              )}
            </Button>
            <RefreshButton onRefresh={handleRefresh} />
          </div>
        </div>

        <div className="card-obsidian p-4 flex flex-col gap-4">
          <div className="flex flex-wrap items-center gap-2">
            <Select
              value={draftFilters.type}
              onValueChange={(value) => updateDraftFilters({ type: value })}
            >
              <SelectTrigger className="w-[180px] h-8">
                <I18nProps>
                  <SelectValue placeholder="All outcomes" />
                </I18nProps>
              </SelectTrigger>
              <SelectContent>
                {EVENT_TYPE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    <I18nText text={option.label} />
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <div className="relative">
              <Search className="absolute left-2 top-2 h-3.5 w-3.5 text-muted-foreground" />
              <I18nProps>
                <Input
                  value={draftFilters.dagName}
                  onChange={(event) =>
                    updateDraftFilters({ dagName: event.target.value })
                  }
                  onKeyDown={handleKeyDown}
                  placeholder="Filter by DAG name"
                  className="h-8 w-[220px] pl-7"
                />
              </I18nProps>
            </div>
            <I18nProps>
              <Input
                value={draftFilters.dagRunId}
                onChange={(event) =>
                  updateDraftFilters({ dagRunId: event.target.value })
                }
                onKeyDown={handleKeyDown}
                placeholder="DAG run ID"
                className="h-8 w-[220px]"
              />
            </I18nProps>
            <I18nProps>
              <Input
                value={draftFilters.attemptId}
                onChange={(event) =>
                  updateDraftFilters({ attemptId: event.target.value })
                }
                onKeyDown={handleKeyDown}
                placeholder="Attempt ID"
                className="h-8 w-[180px]"
              />
            </I18nProps>
            <Button type="button" size="sm" onClick={handleApplyFilters}>
              <I18nText text={'Apply Filters'} />
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={handleClearFilters}
            >
              <FilterX className="h-4 w-4" />
              <I18nText text={'Clear'} />
            </Button>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <I18nProps>
              <ToggleGroup aria-label="Date range mode">
                <I18nProps>
                  <ToggleButton
                    value="preset"
                    groupValue={draftFilters.dateRangeMode}
                    onClick={() => handleDateRangeModeChange('preset')}
                    aria-label="Quick select"
                  >
                    <I18nText text={'Quick'} />
                  </ToggleButton>
                </I18nProps>
                <I18nProps>
                  <ToggleButton
                    value="specific"
                    groupValue={draftFilters.dateRangeMode}
                    onClick={() => handleDateRangeModeChange('specific')}
                    aria-label="Specific date, month, or year"
                  >
                    <I18nText text={'Specific'} />
                  </ToggleButton>
                </I18nProps>
                <I18nProps>
                  <ToggleButton
                    value="custom"
                    groupValue={draftFilters.dateRangeMode}
                    onClick={() => handleDateRangeModeChange('custom')}
                    aria-label="Custom range"
                  >
                    <I18nText text={'Custom'} />
                  </ToggleButton>
                </I18nProps>
              </ToggleGroup>
            </I18nProps>

            {draftFilters.dateRangeMode === 'preset' ? (
              <Select
                value={draftFilters.datePreset}
                onValueChange={handleDatePresetChange}
              >
                <SelectTrigger className="w-[180px] h-8">
                  <I18nProps>
                    <SelectValue placeholder="Select period" />
                  </I18nProps>
                </SelectTrigger>
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
            ) : draftFilters.dateRangeMode === 'specific' ? (
              <>
                <Select
                  value={draftFilters.specificPeriod}
                  onValueChange={handleSpecificPeriodSelect}
                >
                  <SelectTrigger className="w-[110px] h-8">
                    <SelectValue />
                  </SelectTrigger>
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
                  type={getInputTypeForPeriod(draftFilters.specificPeriod)}
                  value={draftFilters.specificValue}
                  onChange={(event) =>
                    handleSpecificPeriodChange(event.target.value)
                  }
                  onKeyDown={handleKeyDown}
                  placeholder={
                    draftFilters.specificPeriod === 'year' ? 'YYYY' : undefined
                  }
                  min={
                    draftFilters.specificPeriod === 'year' ? '2000' : undefined
                  }
                  max={
                    draftFilters.specificPeriod === 'year' ? '2100' : undefined
                  }
                  className="w-[140px] h-8"
                />
              </>
            ) : (
              <DateRangePicker
                fromDate={draftFilters.fromDate}
                toDate={draftFilters.toDate}
                onFromDateChange={(value) =>
                  updateDraftFilters({ fromDate: value })
                }
                onToDateChange={(value) =>
                  updateDraftFilters({ toDate: value })
                }
                onEnterPress={handleApplyFilters}
                fromLabel={`From ${tzLabel}`}
                toLabel={`To ${tzLabel}`}
                className="w-full md:w-auto"
              />
            )}
          </div>
        </div>

        {errorMessage ? (
          <div className="p-3 text-sm text-destructive bg-destructive/10 rounded-md">
            {errorMessage}
          </div>
        ) : null}

        <div className="card-obsidian flex flex-col flex-1 min-h-0 overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b flex-shrink-0">
            <div>
              <h2 className="text-sm font-semibold">
                <I18nText text={'Event Feed'} />
              </h2>
              <p className="text-xs text-muted-foreground">
                <I18nText
                  text={
                    entries.length === 1
                      ? 'Loaded {count} event'
                      : 'Loaded {count} events'
                  }
                  values={{ count: entries.length }}
                />
              </p>
            </div>
            <div className="text-xs text-muted-foreground">
              {hasMoreEntries ? (
                <I18nText text={'More events available'} />
              ) : (
                <I18nText text={'End of feed'} />
              )}
            </div>
          </div>

          <div className="flex-1 min-h-0 overflow-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <I18nText text={'Occurred'} />
                  </TableHead>
                  <TableHead>
                    <I18nText text={'Outcome'} />
                  </TableHead>
                  <TableHead>
                    <I18nText text={'DAG'} />
                  </TableHead>
                  <TableHead>
                    <I18nText text={'Run ID'} />
                  </TableHead>
                  <TableHead>
                    <I18nText text={'Attempt'} />
                  </TableHead>
                  <TableHead>
                    <I18nText text={'Source'} />
                  </TableHead>
                  <TableHead className="text-right">
                    <I18nText text={'Actions'} />
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {isLoading && !data ? (
                  <TableRow>
                    <TableCell
                      colSpan={7}
                      className="py-8 text-center text-muted-foreground"
                    >
                      <I18nText text={'Loading event feed...'} />
                    </TableCell>
                  </TableRow>
                ) : entries.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={7}
                      className="py-8 text-center text-muted-foreground"
                    >
                      <I18nText
                        text={'No events matched the current filters.'}
                      />
                    </TableCell>
                  </TableRow>
                ) : (
                  entries.map((entry) => {
                    const runPath = buildRunPath(entry);
                    return (
                      <TableRow key={entry.id}>
                        <TableCell className="whitespace-nowrap text-muted-foreground">
                          {formatTimestamp(entry.occurredAt)}
                        </TableCell>
                        <TableCell>
                          <Badge variant={getOutcomeVariant(entry.type)}>
                            <I18nText
                              text={getOutcomeLabel(entry.type, entry.status)}
                            />
                          </Badge>
                        </TableCell>
                        <TableCell>
                          {runPath ? (
                            <Link
                              to={runPath}
                              className="font-medium text-primary hover:underline underline-offset-2"
                            >
                              {entry.dagName}
                            </Link>
                          ) : entry.dagName ? (
                            <span className="font-medium">{entry.dagName}</span>
                          ) : (
                            <span className="text-muted-foreground">-</span>
                          )}
                        </TableCell>
                        <TableCell className="font-mono break-all">
                          {runPath && entry.dagRunId ? (
                            <Link
                              to={runPath}
                              className="text-primary hover:underline underline-offset-2"
                            >
                              {entry.dagRunId}
                            </Link>
                          ) : entry.dagRunId ? (
                            entry.dagRunId
                          ) : (
                            <span className="text-muted-foreground">-</span>
                          )}
                        </TableCell>
                        <TableCell className="font-mono break-all">
                          {entry.attemptId || '-'}
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-col">
                            <span>{entry.sourceService}</span>
                            {entry.sourceInstance ? (
                              <span className="text-muted-foreground break-all">
                                {entry.sourceInstance}
                              </span>
                            ) : null}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center justify-end gap-2">
                            {runPath ? (
                              <Button variant="ghost" size="sm" asChild>
                                <Link to={runPath}>
                                  <ExternalLink className="h-4 w-4" />
                                  <I18nText text={'Open Run'} />
                                </Link>
                              </Button>
                            ) : null}
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() => setSelectedEvent(entry)}
                            >
                              <FileJson className="h-4 w-4" />
                              <I18nText text={'View Raw'} />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })
                )}
              </TableBody>
            </Table>
          </div>

          <div className="flex items-center justify-between px-4 py-3 border-t flex-shrink-0">
            <p className="text-xs text-muted-foreground">
              <I18nText
                text={
                  'Results follow committed log order, newest committed entries first.'
                }
              />
            </p>
            <div className="flex items-center gap-2">
              {loadMoreError ? (
                <p className="text-xs text-destructive">{loadMoreError}</p>
              ) : null}
              {hasMoreEntries ? (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    void handleLoadMore();
                  }}
                  disabled={isLoadingMore}
                >
                  {isLoadingMore ? (
                    <I18nText text={'Loading...'} />
                  ) : (
                    <I18nText text={'Load More'} />
                  )}
                </Button>
              ) : null}
            </div>
          </div>
        </div>
      </div>

      <Dialog
        open={selectedEvent !== null}
        onOpenChange={(open) => {
          if (!open) {
            setSelectedEvent(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-3xl max-h-[80vh] flex flex-col">
          <DialogHeader>
            <DialogTitle>
              <I18nText text={'Raw Event'} />
            </DialogTitle>
            <DialogDescription className="sr-only">
              <I18nText
                text={'Full JSON payload for the selected event log entry.'}
              />
            </DialogDescription>
          </DialogHeader>
          <div className="min-h-0 overflow-auto rounded-md bg-muted p-3">
            <pre className="text-xs leading-5 whitespace-pre-wrap break-all font-mono">
              {rawEventJson}
            </pre>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
