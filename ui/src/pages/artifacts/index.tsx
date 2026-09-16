// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from '@/lib/dayjs';
import { AlertCircle, RefreshCw } from 'lucide-react';
import React from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import type { StatusTab } from '@/features/dags/components/DAGStatus';
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import Title from '@/components/ui/title';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useConfig } from '../../contexts/ConfigContext';
import { DAGRunDetailsModal } from '../../features/dag-runs/components/dag-run-details';
import {
  type ArtifactListItem,
  type ArtifactListQuery,
  usePaginatedArtifacts,
} from '../../features/artifacts/hooks/artifactListPagination';
import { useIsMobile } from '../../hooks/useIsMobile';
import { workspaceSelectionQuery } from '../../lib/workspace';

const DEFAULT_PRESET = 'today';
const ARTIFACT_LIST_LIMIT = 100;
const MAX_VISIBLE_FILES = 3;

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

function Artifacts() {
  const location = useLocation();
  const navigate = useNavigate();
  const appBarContext = React.useContext(AppBarContext);
  const config = useConfig();
  const isMobile = useIsMobile();
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );
  const initialPresetDates = computePresetDates(
    DEFAULT_PRESET,
    config.tzOffsetInSec
  );

  const [searchText, setSearchText] = React.useState('');
  const [apiSearchText, setApiSearchText] = React.useState('');
  const [fileNameText, setFileNameText] = React.useState('');
  const [apiFileNameText, setApiFileNameText] = React.useState('');
  const [dateRangeMode, setDateRangeMode] = React.useState<
    'preset' | 'custom'
  >('preset');
  const [datePreset, setDatePreset] = React.useState(DEFAULT_PRESET);
  const [fromDate, setFromDate] = React.useState<string | undefined>(
    initialPresetDates.from
  );
  const [toDate, setToDate] = React.useState<string | undefined>(
    initialPresetDates.to
  );
  const [apiFromDate, setApiFromDate] = React.useState<string | undefined>(
    initialPresetDates.from
  );
  const [apiToDate, setApiToDate] = React.useState<string | undefined>(
    initialPresetDates.to
  );
  const [selectedDAGRun, setSelectedDAGRun] = React.useState<{
    name: string;
    dagRunId: string;
  } | null>(null);
  const [selectedDAGRunInitialTab, setSelectedDAGRunInitialTab] =
    React.useState<StatusTab>('artifacts');
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

  const handleSearch = () => {
    setApiSearchText(searchText.trim());
    setApiFileNameText(fileNameText.trim());
    setApiFromDate(fromDate);
    setApiToDate(toDate);
  };

  const handleDatePresetChange = (preset: string) => {
    setDatePreset(preset);
    const dates = computePresetDates(preset, config.tzOffsetInSec);
    setFromDate(dates.from);
    setToDate(dates.to);
    setApiFromDate(dates.from);
    setApiToDate(dates.to);
  };

  const handleInputKeyPress = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      handleSearch();
    }
  };

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
  React.useEffect(() => {
    if (!isLoadingMore) {
      autoLoadPendingRef.current = false;
    }
  }, [isLoadingMore]);

  const updateSelectedDAGRun = React.useCallback(
    (
      dagRun: { name: string; dagRunId: string } | null,
      initialTab: StatusTab = 'artifacts',
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

  const openDAGRun = React.useCallback(
    (item: ArtifactListItem) => {
      if (isMobile) {
        navigate(`/dag-runs/${item.name}/${item.dagRunId}`);
        return;
      }
      updateSelectedDAGRun({ name: item.name, dagRunId: item.dagRunId });
    },
    [isMobile, navigate, updateSelectedDAGRun]
  );

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
      <div className="mb-2 flex min-w-0 items-center gap-3">
        <Title>
          <I18nText text={'Artifacts'} />
        </Title>
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
      <div>
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
                placeholder="Filter by file name..."
                value={fileNameText}
                onChange={(e) => setFileNameText(e.target.value)}
                onKeyDown={handleInputKeyPress}
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
              <Select
                value={datePreset}
                onValueChange={handleDatePresetChange}
              >
                <I18nProps>
                  <SelectTrigger
                    aria-label="Date preset"
                    className="w-[180px]"
                  >
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
          <Table className="w-full text-xs">
            <TableHeader>
              <TableRow>
                <TableHead className="py-1 px-2">
                  <I18nText text={'DAG Name'} />
                </TableHead>
                <TableHead className="py-1 px-2">
                  <I18nText text={'Run ID'} />
                </TableHead>
                <TableHead className="py-1 px-2">
                  <div>
                    <I18nText text={'Created At'} />
                  </div>
                  <div className="text-xs font-normal text-muted-foreground">
                    {tzLabel}
                  </div>
                </TableHead>
                <TableHead className="py-1 px-2">
                  <div>
                    <I18nText text={'Started At'} />
                  </div>
                  <div className="text-xs font-normal text-muted-foreground">
                    {tzLabel}
                  </div>
                </TableHead>
                <TableHead className="py-1 px-2">
                  <I18nText text={'Files'} />
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow
                  key={item.dagRunId}
                  className="cursor-pointer border-l-4 border-l-transparent transition-colors hover:bg-muted/50 focus-visible:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                  style={{ fontSize: '0.8125rem' }}
                  onClick={(e) => {
                    if (e.ctrlKey || e.metaKey) {
                      const basePath = config.basePath || '';
                      window.open(
                        `${basePath}/dag-runs/${item.name}/${item.dagRunId}`,
                        '_blank'
                      );
                      return;
                    }
                    openDAGRun(item);
                  }}
                >
                  <TableCell className="py-1 px-2 font-normal">
                    <div className="flex min-w-0 items-center gap-2">
                      <Link
                        to={`/dag-runs/${item.name}/${item.dagRunId}`}
                        className="min-w-0 truncate hover:underline"
                        onClick={(event) => event.stopPropagation()}
                      >
                        {item.name}
                      </Link>
                    </div>
                  </TableCell>
                  <TableCell className="py-1 px-2 font-mono text-muted-foreground">
                    {item.dagRunId}
                  </TableCell>
                  <TableCell className="py-1 px-2 text-muted-foreground">
                    {formatTimestamp(item.createdAt)}
                  </TableCell>
                  <TableCell className="py-1 px-2 text-muted-foreground">
                    {formatTimestamp(item.startedAt)}
                  </TableCell>
                  <TableCell className="py-1 px-2">
                    <ArtifactFilesCell item={item} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
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
                  <I18nText text={'Loading more artifacts...'} />
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
          ) : items.length > 0 ? (
            <div className="text-sm text-muted-foreground">
              <I18nText text={'All artifact files are displayed.'} />
            </div>
          ) : null}
        </div>
      </div>

      {selectedDAGRun && (
        <DAGRunDetailsModal
          name={selectedDAGRun.name}
          dagRunId={selectedDAGRun.dagRunId}
          isOpen={!!selectedDAGRun}
          onClose={() => updateSelectedDAGRun(null, 'artifacts', true)}
          initialTab={selectedDAGRunInitialTab}
        />
      )}
    </div>
  );
}

function ArtifactFilesCell({
  item,
}: {
  item: ArtifactListItem;
}): React.ReactNode {
  const visibleFiles = item.files.slice(0, MAX_VISIBLE_FILES);
  const hiddenCount = item.files.length - visibleFiles.length;

  return (
    <div className="flex flex-wrap items-center gap-1">
      {item.files.length === 0 ? (
        <span className="text-muted-foreground">
          <I18nText text={'No files'} />
        </span>
      ) : (
        <>
          {visibleFiles.map((file) => (
            <span
              key={file.path}
              className="inline-flex min-w-0 max-w-[10rem] items-center truncate rounded border bg-muted/30 px-1 text-[10px] text-muted-foreground"
              title={file.path}
            >
              {file.path}
            </span>
          ))}
          {hiddenCount > 0 && (
            <span className="text-muted-foreground">+{hiddenCount}</span>
          )}
          {item.filesTruncated && (
            <span className="inline-flex items-center rounded bg-muted/50 px-1 text-[10px] text-muted-foreground">
              <I18nText text={'truncated'} />
            </span>
          )}
        </>
      )}
    </div>
  );
}

export default Artifacts;