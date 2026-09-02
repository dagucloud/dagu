// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { RefreshButton } from '@/components/ui/refresh-button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Filter, GanttChart } from 'lucide-react';
import React from 'react';
import { Link } from 'react-router-dom';
import {
  PathsDagsGetParametersQueryOrder,
  PathsDagsGetParametersQuerySort,
  Status,
} from '../api/v1/schema';
import { AppBarContext } from '../contexts/AppBarContext';
import { CreateDAGModal } from '../features/dags/components/common';
import { useConfig } from '../contexts/ConfigContext';
import { useSearchState } from '../contexts/SearchStateContext';
import { DAGRunDetailsModal } from '../features/dag-runs/components/dag-run-details';
import { usePaginatedDAGRuns } from '../features/dag-runs/hooks/dagRunPagination';
import { useClient } from '../hooks/api';
import DashboardTimeChart from '../features/dashboard/components/DashboardTimechart';
import { optionalPositiveInt } from '../hooks/queryUtils';
import PathsCard from '../features/system-status/components/PathsCard';
import dayjs from '../lib/dayjs';
import {
  workspaceSelectionKey,
  workspaceSelectionQuery,
} from '../lib/workspace';
import { I18nText } from '@/i18n/I18nText';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type Metrics = Record<Status, number>;

const TRACKED_STATUSES = [
  Status.Success,
  Status.Failed,
  Status.Running,
  Status.Aborted,
  Status.Queued,
  Status.NotStarted,
  Status.PartialSuccess,
  Status.Waiting,
  Status.Rejected,
] as const;

const DASHBOARD_VISIBLE_STATUSES = TRACKED_STATUSES.filter(
  (status) => status !== Status.Queued
);

function createEmptyMetrics(): Metrics {
  const metrics: Partial<Metrics> = {};
  for (const status of TRACKED_STATUSES) {
    metrics[status] = 0;
  }
  return metrics as Metrics;
}

function getDayBounds(
  date: dayjs.Dayjs,
  tzOffsetInSec: number | undefined
): { startOfDay: dayjs.Dayjs; endOfDay: dayjs.Dayjs } {
  const adjusted =
    tzOffsetInSec !== undefined ? date.utcOffset(tzOffsetInSec / 60) : date;
  return {
    startOfDay: adjusted.startOf('day'),
    endOfDay: adjusted.endOf('day'),
  };
}

function compareDAGNames(left: string, right: string): number {
  return left.localeCompare(right);
}

async function fetchAllDashboardDAGNames(
  client: ReturnType<typeof useClient>,
  remoteNode: string,
  workspaceQuery: ReturnType<typeof workspaceSelectionQuery>,
  signal: AbortSignal
): Promise<{ names: string[]; totalCount: number }> {
  const names = new Set<string>();
  let totalCount: number | undefined;
  let page = 1;

  for (;;) {
    const response = await client.GET('/dags', {
      params: {
        query: {
          remoteNode,
          page,
          perPage: 100,
          sort: PathsDagsGetParametersQuerySort.name,
          order: PathsDagsGetParametersQueryOrder.asc,
          ...workspaceQuery,
        },
      },
      signal,
    });

    if (response.error) {
      const message =
        response.error &&
        typeof response.error === 'object' &&
        'message' in response.error
          ? String(response.error.message)
          : 'Failed to load DAG definitions';
      throw new Error(message);
    }

    const data = response.data;
    // A failed response with an unparsable body yields neither data nor
    // error; treating it as "zero DAGs" would show first-run guidance to
    // users whose requests merely failed.
    if (!data) {
      throw new Error('Empty response for DAG definitions');
    }
    totalCount ??= data?.pagination?.totalRecords;
    for (const dag of data?.dags ?? []) {
      if (dag.dag.name) {
        names.add(dag.dag.name);
      }
    }

    const totalPages = data?.pagination?.totalPages ?? page;
    if (page >= totalPages) {
      break;
    }
    page += 1;
  }

  const sortedNames = Array.from(names).sort(compareDAGNames);
  return { names: sortedNames, totalCount: totalCount ?? sortedNames.length };
}

type DAGInventory = {
  status: 'loading' | 'loaded' | 'error';
  names: string[];
  totalCount: number;
};

function GettingStartedPanel(): React.ReactElement {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 rounded-xl border border-border bg-surface p-8 text-center">
      <h2 className="text-xl font-semibold text-foreground">
        <I18nText text={'Create your first workflow'} />
      </h2>
      <p className="max-w-md text-base text-muted-foreground">
        <I18nText
          text={
            'Dagu runs workflows defined in YAML. Create one from scratch or start from the documentation examples.'
          }
        />
      </p>
      <CreateDAGModal />
      <div className="flex flex-wrap items-center justify-center gap-4 text-sm">
        <a
          href="https://docs.dagu.sh"
          target="_blank"
          rel="noreferrer"
          className="text-primary hover:underline"
        >
          <I18nText text={'Documentation'} />
        </a>
        <a
          href="https://docs.dagu.sh/writing-workflows/examples"
          target="_blank"
          rel="noreferrer"
          className="text-primary hover:underline"
        >
          <I18nText text={'Example workflows'} />
        </a>
      </div>
    </div>
  );
}

function NoRunsNotice({
  dateLabel,
  hasExampleDAGs,
}: {
  dateLabel: string;
  hasExampleDAGs: boolean;
}): React.ReactElement {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      <GanttChart className="h-12 w-12 text-muted-foreground/40" />
      <h2 className="text-xl font-semibold text-foreground">
        <I18nText text="No runs on {date}" values={{ date: dateLabel }} />
      </h2>
      <p className="text-base text-muted-foreground">
        <I18nTemplate
          text={
            hasExampleDAGs
              ? 'Run one of the example workflows from the {link} to see activity here.'
              : 'Start a workflow from the {link} to see activity here.'
          }
          values={{
            link: (
              <Link to="/dags" className="text-primary hover:underline">
                <I18nText text="Workflows page" />
              </Link>
            ),
          }}
        />
      </p>
    </div>
  );
}

function Dashboard(): React.ReactElement | null {
  const { locale } = useI18n();
  const appBarContext = React.useContext(AppBarContext);
  const client = useClient();
  const config = useConfig();
  const searchState = useSearchState();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const workspaceKey = workspaceSelectionKey(workspaceSelection);
  const searchStateScope = React.useMemo(
    () =>
      JSON.stringify({
        remoteNode,
        workspace: workspaceKey,
      }),
    [remoteNode, workspaceKey]
  );

  const [modalDAGRun, setModalDAGRun] = React.useState<{
    name: string;
    dagRunId: string;
  } | null>(null);
  const autoLoadSentinelRef = React.useRef<HTMLDivElement>(null);
  const [autoLoadRequested, setAutoLoadRequested] = React.useState(false);
  const [dagInventory, setDAGInventory] = React.useState<DAGInventory>({
    status: 'loading',
    names: [],
    totalCount: 0,
  });
  const [inventoryRetryNonce, setInventoryRetryNonce] = React.useState(0);
  const lastWindowScrollYRef = React.useRef(0);

  type DashboardFilters = {
    selectedDAGRun: string;
    dateRange: {
      startDate: number;
      endDate: number | undefined;
    };
  };

  const areFiltersEqual = (a: DashboardFilters, b: DashboardFilters) =>
    a.selectedDAGRun === b.selectedDAGRun &&
    a.dateRange.startDate === b.dateRange.startDate &&
    (a.dateRange.endDate ?? null) === (b.dateRange.endDate ?? null);

  const getDefaultDateRange = React.useCallback(() => {
    const { startOfDay } = getDayBounds(dayjs(), config.tzOffsetInSec);
    return {
      startDate: startOfDay.unix(),
      endDate: undefined,
    };
  }, [config.tzOffsetInSec]);

  const defaultFilters = React.useMemo<DashboardFilters>(
    () => ({
      selectedDAGRun: 'all',
      dateRange: getDefaultDateRange(),
    }),
    [getDefaultDateRange]
  );

  const [selectedDAGRun, setSelectedDAGRun] = React.useState<string>(
    defaultFilters.selectedDAGRun
  );
  const [dateRange, setDateRange] = React.useState<{
    startDate: number;
    endDate: number | undefined;
  }>(defaultFilters.dateRange);

  const currentFilters = React.useMemo<DashboardFilters>(
    () => ({
      selectedDAGRun,
      dateRange,
    }),
    [selectedDAGRun, dateRange]
  );

  const currentFiltersRef = React.useRef(currentFilters);
  React.useEffect(() => {
    currentFiltersRef.current = currentFilters;
  }, [currentFilters]);

  const lastPersistedFiltersRef = React.useRef<DashboardFilters | null>(null);

  React.useEffect(() => {
    const stored = searchState.readState<DashboardFilters>(
      'dashboard',
      searchStateScope
    );
    const base = defaultFilters;
    const next = stored
      ? {
          selectedDAGRun: stored.selectedDAGRun || base.selectedDAGRun,
          dateRange: {
            startDate: stored.dateRange?.startDate ?? base.dateRange.startDate,
            endDate:
              stored.dateRange?.endDate === undefined
                ? base.dateRange.endDate
                : stored.dateRange.endDate,
          },
        }
      : base;

    const current = currentFiltersRef.current;
    if (current && areFiltersEqual(current, next)) {
      if (!stored) {
        searchState.writeState('dashboard', searchStateScope, next);
      }
      lastPersistedFiltersRef.current = next;
      return;
    }

    setSelectedDAGRun(next.selectedDAGRun);
    setDateRange(next.dateRange);
    lastPersistedFiltersRef.current = next;
    searchState.writeState('dashboard', searchStateScope, next);
  }, [defaultFilters, searchState, searchStateScope]);

  React.useEffect(() => {
    const persisted = lastPersistedFiltersRef.current;
    if (persisted && areFiltersEqual(persisted, currentFilters)) {
      return;
    }
    lastPersistedFiltersRef.current = currentFilters;
    searchState.writeState('dashboard', searchStateScope, currentFilters);
  }, [currentFilters, searchState, searchStateScope]);

  const handleDateChange = (startTimestamp: number, endTimestamp: number) => {
    setDateRange({
      startDate: startTimestamp,
      endDate: endTimestamp,
    });
  };

  const selectedDAGName = selectedDAGRun !== 'all' ? selectedDAGRun : undefined;
  const dashboardPageLimit = React.useMemo(
    () => optionalPositiveInt(config.maxDashboardPageLimit),
    [config.maxDashboardPageLimit]
  );
  const dagRunsQuery = React.useMemo(
    () => ({
      remoteNode,
      fromDate: dateRange.startDate,
      toDate: dateRange.endDate,
      name: selectedDAGName,
      status: DASHBOARD_VISIBLE_STATUSES,
      ...workspaceQuery,
      ...(dashboardPageLimit !== undefined
        ? { limit: dashboardPageLimit }
        : {}),
    }),
    [
      dashboardPageLimit,
      dateRange.endDate,
      dateRange.startDate,
      remoteNode,
      selectedDAGName,
      workspaceQuery,
    ]
  );

  const {
    dagRuns: dagRunsList,
    error,
    isInitialLoading: isLoading,
    isLoadingMore,
    hasMore,
    loadMore,
    refresh,
  } = usePaginatedDAGRuns({
    query: dagRunsQuery,
    liveEnabled: true,
    fallbackIntervalMs: 5000,
    resetOnSSEInvalidate: true,
  });

  const handleRefreshAll = async () => {
    await refresh();
  };

  const uniqueDAGRunNames = React.useMemo(() => {
    const names = new Set(dagInventory.names);

    for (const dagRun of dagRunsList) {
      if (dagRun.name) {
        names.add(dagRun.name);
      }
    }
    if (selectedDAGRun !== 'all') {
      names.add(selectedDAGRun);
    }

    return Array.from(names).sort(compareDAGNames);
  }, [dagInventory.names, dagRunsList, selectedDAGRun]);

  const handleDAGRunChange = (value: string) => {
    setSelectedDAGRun(value);
  };

  const selectedTimelineDate = React.useMemo(
    () => ({
      startTimestamp: dateRange.startDate,
      endTimestamp: dateRange.endDate,
    }),
    [dateRange.endDate, dateRange.startDate]
  );

  React.useEffect(() => {
    if (appBarContext) {
      appBarContext.setTitle('Timeline');
    }
  }, [appBarContext]);

  React.useEffect(() => {
    const controller = new AbortController();
    setDAGInventory({ status: 'loading', names: [], totalCount: 0 });

    void fetchAllDashboardDAGNames(
      client,
      remoteNode,
      workspaceQuery,
      controller.signal
    )
      .then(({ names, totalCount }) => {
        if (!controller.signal.aborted) {
          setDAGInventory({ status: 'loaded', names, totalCount });
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setDAGInventory({ status: 'error', names: [], totalCount: 0 });
        }
      });

    return () => controller.abort();
  }, [client, remoteNode, workspaceQuery, inventoryRetryNonce]);

  React.useEffect(() => {
    lastWindowScrollYRef.current = window.scrollY;

    const requestAutoLoad = () => {
      const currentScrollY = window.scrollY;
      const isScrollingDown = currentScrollY > lastWindowScrollYRef.current;
      lastWindowScrollYRef.current = currentScrollY;

      if (!isScrollingDown) {
        return;
      }

      const documentHeight = Math.max(
        document.documentElement.scrollHeight,
        document.body.scrollHeight
      );
      const viewportBottom = currentScrollY + window.innerHeight;
      const distanceToBottom = documentHeight - viewportBottom;

      if (distanceToBottom > 200) {
        return;
      }

      setAutoLoadRequested(true);
    };

    window.addEventListener('scroll', requestAutoLoad, { passive: true });

    return () => {
      window.removeEventListener('scroll', requestAutoLoad);
    };
  }, []);

  React.useEffect(() => {
    const el = autoLoadSentinelRef.current;
    if (
      !autoLoadRequested ||
      !el ||
      !hasMore ||
      isLoadingMore ||
      typeof IntersectionObserver === 'undefined'
    ) {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          setAutoLoadRequested(false);
          void loadMore();
        }
      },
      { threshold: 0.1 }
    );

    observer.observe(el);
    return () => observer.disconnect();
  }, [autoLoadRequested, hasMore, isLoadingMore, loadMore]);

  if (error) {
    const errorMessage = error.message || 'Unknown error loading dashboard';
    return (
      <div className="p-4 text-error">
        <I18nText text={'Error:'} /> {errorMessage}
      </div>
    );
  }

  const metrics = createEmptyMetrics();
  const totalDAGRuns = dagRunsList.length;

  for (const dagRun of dagRunsList) {
    if (dagRun && dagRun.status in metrics) {
      metrics[dagRun.status as Status] += 1;
    }
  }

  const hasFailures = metrics[Status.Failed] > 0;
  const hasRunning = metrics[Status.Running] > 0;

  const showGettingStarted =
    dagInventory.status === 'loaded' && dagInventory.totalCount === 0;
  // With the inventory pending or failed, "no runs" cannot be distinguished
  // from "request failed", so first-run guidance stays off.
  const showNoRunsNotice =
    dagInventory.status === 'loaded' &&
    !showGettingStarted &&
    !isLoading &&
    totalDAGRuns === 0;
  const showInventoryError =
    dagInventory.status === 'error' && !isLoading && totalDAGRuns === 0;
  const hasExampleDAGs = dagInventory.names.some((name) =>
    name.startsWith('example-')
  );
  // Show placeholders instead of zeros while the first page is loading.
  const stat = (value: number) => (isLoading ? '-' : value);

  return (
    <div className="flex flex-col max-w-7xl h-full overflow-hidden">
      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-h-0 gap-3 p-1">
        {/* Toolbar - Top */}
        <div className="flex flex-wrap items-center gap-2 flex-shrink-0">
          <Select
            value={selectedDAGRun}
            onValueChange={handleDAGRunChange}
            disabled={isLoading}
          >
            <SelectTrigger className="h-9 w-[140px]">
              <Filter className="h-4 w-4 mr-1.5 text-muted-foreground" />
              <I18nProps>
                <SelectValue
                  placeholder={isLoading ? 'Loading...' : 'All DAGs'}
                />
              </I18nProps>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">
                <I18nText text={'All DAGs'} />
              </SelectItem>
              {uniqueDAGRunNames.map((name) => (
                <SelectItem key={name} value={name}>
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Input
            type="date"
            value={dayjs.unix(dateRange.startDate).format('YYYY-MM-DD')}
            onChange={(e) => {
              const newDate = e.target.value;
              if (!newDate) return;
              const date = dayjs(newDate);
              if (!date.isValid()) return;
              const { startOfDay, endOfDay } = getDayBounds(
                date,
                config.tzOffsetInSec
              );
              handleDateChange(startOfDay.unix(), endOfDay.unix());
            }}
            className="h-9 w-[150px]"
          />
          <Button
            variant="outline"
            onClick={() => {
              const { startOfDay, endOfDay } = getDayBounds(
                dayjs(),
                config.tzOffsetInSec
              );
              handleDateChange(startOfDay.unix(), endOfDay.unix());
            }}
          >
            <I18nText text={'Today'} />
          </Button>

          <div className="flex-1" />

          <PathsCard />
          <RefreshButton onRefresh={handleRefreshAll} />
        </div>

        {showGettingStarted ? (
          <GettingStartedPanel />
        ) : (
          <>
            {/* Stats Row */}
            <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 sm:gap-x-6 text-sm text-muted-foreground flex-shrink-0">
              <div className="flex items-baseline gap-1">
                <span className="text-lg sm:text-xl font-light tabular-nums text-foreground">
                  {stat(totalDAGRuns)}
                  {hasMore ? '+' : ''}
                </span>
                <span className="text-xs">
                  <I18nText text={'recent runs'} />
                </span>
              </div>
              <div className="flex items-baseline gap-1">
                <span className="text-lg sm:text-xl font-light tabular-nums text-foreground">
                  {stat(metrics[Status.Success])}
                </span>
                <span className="text-xs">
                  <I18nText text={'ok'} />
                </span>
              </div>
              <div className="flex items-baseline gap-1">
                <span
                  className={`text-lg sm:text-xl font-light tabular-nums ${hasFailures ? 'text-foreground' : 'text-muted-foreground/50'}`}
                >
                  {stat(metrics[Status.Failed])}
                </span>
                <span className="text-xs">
                  <I18nText text={'failed'} />
                </span>
              </div>
              <div className="flex items-baseline gap-1">
                <span className="text-lg sm:text-xl font-light tabular-nums text-foreground">
                  {stat(metrics[Status.Aborted])}
                </span>
                <span className="text-xs">
                  <I18nText text={'aborted'} />
                </span>
              </div>
              {hasRunning && (
                <div className="flex items-baseline gap-1">
                  <span className="text-lg sm:text-xl font-light tabular-nums text-foreground">
                    {metrics[Status.Running]}
                  </span>
                  <span className="text-xs">
                    <I18nText text={'active'} />
                  </span>
                </div>
              )}
              {metrics[Status.Waiting] > 0 && (
                <div className="flex items-baseline gap-1">
                  <span className="text-lg sm:text-xl font-light tabular-nums text-foreground">
                    {metrics[Status.Waiting]}
                  </span>
                  <span className="text-xs">
                    <I18nText text={'waiting'} />
                  </span>
                </div>
              )}
              {metrics[Status.Rejected] > 0 && (
                <div className="flex items-baseline gap-1">
                  <span className="text-lg sm:text-xl font-light tabular-nums text-foreground">
                    {metrics[Status.Rejected]}
                  </span>
                  <span className="text-xs">
                    <I18nText text={'rejected'} />
                  </span>
                </div>
              )}
            </div>

            {/* Timeline Visualization - Hero */}
            <div className="flex-1 min-h-[250px] rounded-xl border border-border bg-surface overflow-hidden">
              {showNoRunsNotice ? (
                <NoRunsNotice
                  dateLabel={new Intl.DateTimeFormat(locale, {
                    year: 'numeric',
                    month: 'short',
                    day: 'numeric',
                  }).format(dayjs.unix(dateRange.startDate).toDate())}
                  hasExampleDAGs={hasExampleDAGs}
                />
              ) : showInventoryError ? (
                <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
                  <p className="text-base font-medium text-foreground">
                    <I18nText text={'Failed to load the workflow list.'} />
                  </p>
                  <Button
                    variant="outline"
                    onClick={() => setInventoryRetryNonce((n) => n + 1)}
                  >
                    <I18nText text={'Retry'} />
                  </Button>
                </div>
              ) : (
                <DashboardTimeChart
                  data={dagRunsList}
                  selectedDate={selectedTimelineDate}
                />
              )}
            </div>
            {hasMore && (
              <div className="flex flex-col items-center justify-center gap-2 flex-shrink-0">
                <Button
                  variant="outline"
                  onClick={() => void loadMore()}
                  disabled={isLoadingMore}
                >
                  {isLoadingMore ? (
                    <I18nText text={'Loading...'} />
                  ) : (
                    <I18nText text={'Load older runs'} />
                  )}
                </Button>
                <div
                  ref={autoLoadSentinelRef}
                  className="h-1 w-full shrink-0"
                />
              </div>
            )}
          </>
        )}
      </div>

      {/* DAG Run Details Modal */}
      {modalDAGRun && (
        <DAGRunDetailsModal
          name={modalDAGRun.name}
          dagRunId={modalDAGRun.dagRunId}
          isOpen={!!modalDAGRun}
          onClose={() => {
            setModalDAGRun(null);
            window.history.pushState({}, '', window.location.pathname);
          }}
        />
      )}
    </div>
  );
}

export default Dashboard;
