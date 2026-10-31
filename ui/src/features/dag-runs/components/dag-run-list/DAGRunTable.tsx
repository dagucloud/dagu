// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';
import { Checkbox } from '@/components/ui/checkbox';
import { Link } from 'react-router-dom';
import { SlidersHorizontal } from 'lucide-react';
import { components, Status } from '../../../../api/v1/schema';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import dayjs from '../../../../lib/dayjs';
import RelativeTime from '@/components/ui/relative-time';
import { RunStatusChip } from '../common/RunStatusChip';
import AutoRetryBadge from '../common/AutoRetryBadge';
import { TriggerTypeIndicator } from '../../../dags/components/common/TriggerTypeIndicator';
import {
  DAGRunSelectionItem,
  getDAGRunSelectionKey,
} from '../../hooks/useBulkDAGRunSelection';
import { buildDAGPageURL } from '../../lib/dagRunUrls';
import { useOpenPage } from '../../lib/useOpenPage';
import { StepDetailsTooltip } from './StepDetailsTooltip';
import { DAGRunArtifactsButton } from './DAGRunArtifactsButton';
import { I18nText } from '@/i18n/I18nText';

interface DAGRunTableProps {
  dagRuns: components['schemas']['DAGRunSummary'][];
  /** True while the first page is being fetched; suppresses the empty state. */
  isLoading?: boolean;
  selectedRunKeys?: Set<string>;
  onToggleBulkSelect?: (dagRun: DAGRunSelectionItem) => void;
}

function isInteractiveEventTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }

  if (target.isContentEditable) {
    return true;
  }

  return Boolean(
    target.closest(
      'input, textarea, select, button, a, [role="button"], [role="combobox"], [role="textbox"], [role="option"], [role="menuitem"], [contenteditable="true"]'
    )
  );
}

function DAGRunTable({
  dagRuns,
  isLoading = false,
  selectedRunKeys,
  onToggleBulkSelect,
}: DAGRunTableProps) {
  const config = useConfig();
  const remoteNode = useRemoteNode();
  const openPage = useOpenPage();
  // A row opens the run on its workflow page; the artifacts button opens the Artifacts tab.
  const runURL = (dagRun: DAGRunSelectionItem, dock?: string) =>
    buildDAGPageURL({
      fileName: dagRun.name,
      remoteNode,
      run: dagRun.dagRunId,
      dock,
    });
  const [isSmallScreen, setIsSmallScreen] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState<number>(-1);
  const tableRef = useRef<HTMLDivElement>(null);

  // Check screen size on mount and when window resizes
  useEffect(() => {
    const checkScreenSize = () => {
      setIsSmallScreen(window.innerWidth < 768); // 768px is typically md breakpoint
    };

    // Initial check
    checkScreenSize();

    // Add event listener
    window.addEventListener('resize', checkScreenSize);

    // Cleanup
    return () => window.removeEventListener('resize', checkScreenSize);
  }, []);

  // Helper function to scroll to the selected row
  const scrollToSelectedRow = (index: number) => {
    if (index >= 0 && tableRef.current) {
      const rows = tableRef.current.querySelectorAll('tbody tr');
      if (rows[index]) {
        rows[index].scrollIntoView({
          behavior: 'smooth',
          block: 'nearest',
        });
      }
    }
  };

  // Keyboard navigation: arrows move the highlighted row, Enter opens its run.
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      // A popup item or another control took the key.
      if (event.defaultPrevented || isInteractiveEventTarget(event.target)) {
        return;
      }
      if (event.metaKey || event.ctrlKey || event.altKey) {
        return;
      }

      if (event.key === 'ArrowDown') {
        event.preventDefault();
        const newIndex =
          selectedIndex < dagRuns.length - 1
            ? selectedIndex + 1
            : selectedIndex;
        if (newIndex !== selectedIndex) {
          setSelectedIndex(newIndex);
          scrollToSelectedRow(newIndex);
        }
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        const newIndex = selectedIndex > 0 ? selectedIndex - 1 : 0;
        if (newIndex !== selectedIndex) {
          setSelectedIndex(newIndex);
          scrollToSelectedRow(newIndex);
        }
      } else if (event.key === 'Enter' && selectedIndex >= 0) {
        const selectedItem = dagRuns[selectedIndex];
        if (selectedItem) {
          openPage(runURL(selectedItem));
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [dagRuns, selectedIndex, openPage, remoteNode]);

  // Initialize selection when dagRuns change
  useEffect(() => {
    if (dagRuns.length > 0 && selectedIndex === -1) {
      setSelectedIndex(0);
    } else if (selectedIndex >= dagRuns.length) {
      setSelectedIndex(dagRuns.length - 1);
    }
  }, [dagRuns, selectedIndex]);

  // Format timezone information for display
  const getTimezoneInfo = (): string => {
    if (config.tzOffsetInSec === undefined) return 'Local Timezone';

    // Convert seconds to hours and minutes
    const offsetInMinutes = config.tzOffsetInSec / 60;
    const hours = Math.floor(Math.abs(offsetInMinutes) / 60);
    const minutes = Math.abs(offsetInMinutes) % 60;

    // Format with sign and padding
    const sign = offsetInMinutes >= 0 ? '+' : '-';
    const formattedHours = hours.toString().padStart(2, '0');
    const formattedMinutes = minutes.toString().padStart(2, '0');

    return `${sign}${formattedHours}:${formattedMinutes}`;
  };

  // Calculate duration between start and finish times
  const calculateDuration = (
    startedAt: string,
    finishedAt: string | null,
    status: number
  ): string => {
    // If DAG-run hasn't started yet, return dash
    if (!startedAt) {
      return '-';
    }

    // Only calculate duration dynamically for running DAGs
    if (status === Status.Running && !finishedAt) {
      // If DAG-run is still running, calculate duration from start until now
      const start = dayjs(startedAt);
      const now = dayjs();
      const durationMs = now.diff(start);
      return formatDuration(durationMs);
    }

    // For finished DAGs, use the static duration
    if (finishedAt) {
      const start = dayjs(startedAt);
      const end = dayjs(finishedAt);
      const durationMs = end.diff(start);
      return formatDuration(durationMs);
    }

    // For non-running DAGs without a finish time, return dash
    return '-';
  };

  // Format duration in a human-readable format
  const formatDuration = (durationMs: number): string => {
    const seconds = Math.floor(durationMs / 1000);

    if (seconds < 60) {
      return `${seconds}s`;
    }

    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;

    if (minutes < 60) {
      return `${minutes}m ${remainingSeconds}s`;
    }

    const hours = Math.floor(minutes / 60);
    const remainingMinutes = minutes % 60;

    return `${hours}h ${remainingMinutes}m ${remainingSeconds}s`;
  };

  const timezoneInfo = getTimezoneInfo();
  const formatScheduleTime = (scheduleTime: string): string => {
    const value = dayjs(scheduleTime);
    const configuredTime =
      config.tzOffsetInSec === undefined
        ? value
        : value.utcOffset(config.tzOffsetInSec / 60);
    return configuredTime.format('YYYY-MM-DD HH:mm:ss');
  };
  const showScheduleColumn = dagRuns.some((dagRun) =>
    Boolean(dagRun.scheduleTime)
  );
  const showProfileColumn = dagRuns.some((dagRun) =>
    Boolean(dagRun.profileName)
  );
  const isBulkSelected = (dagRun: DAGRunSelectionItem) =>
    selectedRunKeys?.has(getDAGRunSelectionKey(dagRun)) ?? false;

  // Empty state component
  const EmptyState = () => (
    <div className="flex flex-col items-center justify-center py-12 px-4 border rounded-md bg-card">
      <div className="text-6xl mb-4">🔍</div>
      <h3 className="text-lg font-normal text-foreground mb-2">
        <I18nText text={'No runs found'} />
      </h3>
      <p className="text-sm text-muted-foreground text-center max-w-md mb-4">
        <I18nText
          text={
            'No runs in the selected time range. Adjust the date range or filters, or start a workflow from the Workflows page.'
          }
        />
      </p>
    </div>
  );

  // If there are no DAG runs, show empty state (unless the first page is still loading)
  if (dagRuns.length === 0) {
    if (isLoading) {
      return (
        <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
          <I18nText text={'Loading runs...'} />
        </div>
      );
    }
    return <EmptyState />;
  }

  // Card view for small screens - Direct navigation without modal
  if (isSmallScreen) {
    return (
      <div className="space-y-2">
        {dagRuns.map((dagRun, index) => (
          <div
            key={dagRun.dagRunId}
            className={`p-3 rounded-lg border border-l-4 min-h-[80px] flex flex-col bg-card border-border ${selectedIndex === index ? 'border-l-border' : 'border-l-transparent'} ${isBulkSelected(dagRun) ? 'bg-muted/30' : ''} ${dagRun.status === Status.Running ? 'animate-running-row' : ''} cursor-pointer`}
            onClick={(e) => openPage(runURL(dagRun), e)}
          >
            {/* Header with name and status */}
            <div className="flex justify-between items-start gap-3 mb-2">
              <div className="flex items-start gap-2 min-w-0">
                {onToggleBulkSelect && (
                  <div
                    className="flex h-8 w-8 items-center justify-center rounded-md transition-colors hover:bg-muted focus-within:ring-2 focus-within:ring-ring"
                    onClick={(event) => {
                      event.stopPropagation();
                      if (event.target !== event.currentTarget) {
                        return;
                      }
                      onToggleBulkSelect({
                        name: dagRun.name,
                        dagRunId: dagRun.dagRunId,
                      });
                    }}
                  >
                    <Checkbox
                      aria-label={`Select DAG run ${dagRun.name} ${dagRun.dagRunId}`}
                      checked={isBulkSelected(dagRun)}
                      onCheckedChange={() =>
                        onToggleBulkSelect({
                          name: dagRun.name,
                          dagRunId: dagRun.dagRunId,
                        })
                      }
                      className="h-5 w-5 pointer-events-none"
                    />
                  </div>
                )}
                <Link
                  to={runURL(dagRun)}
                  className="font-normal text-sm hover:underline"
                  onClick={(event) => event.stopPropagation()}
                >
                  {dagRun.name}
                </Link>
              </div>
              <div className="flex items-start gap-2">
                <DAGRunArtifactsButton
                  dagRun={dagRun}
                  to={runURL(dagRun, 'artifacts')}
                />
                <StepDetailsTooltip dagRun={dagRun}>
                  <div className="flex flex-col items-end gap-1">
                    <RunStatusChip
                      name={dagRun.name}
                      dagRunId={dagRun.dagRunId}
                      status={dagRun.status}
                      size="xs"
                    >
                      {dagRun.statusLabel}
                    </RunStatusChip>
                    <AutoRetryBadge
                      status={dagRun.status}
                      count={dagRun.autoRetryCount}
                      limit={dagRun.autoRetryLimit}
                      className="text-[10px]"
                    />
                  </div>
                </StepDetailsTooltip>
              </div>
            </div>

            {/* DAG-run ID and Trigger */}
            <div className="flex items-center justify-between text-xs mb-2 min-w-0">
              <span className="font-mono text-muted-foreground truncate">
                {dagRun.dagRunId}
              </span>
              <TriggerTypeIndicator
                type={dagRun.triggerType}
                actor={dagRun.triggerActor}
              />
            </div>

            {/* Timestamps */}
            <div className="space-y-1 text-xs mt-2">
              {dagRun.scheduleTime && (
                <div className="flex justify-between items-center">
                  <div className="whitespace-normal break-words">
                    <span className="text-muted-foreground">
                      <I18nText text={'Scheduled:'} />{' '}
                    </span>
                    <span title={dagRun.scheduleTime}>
                      {formatScheduleTime(dagRun.scheduleTime)}
                    </span>
                  </div>
                </div>
              )}
              <div className="flex justify-between items-center">
                <div>
                  <span className="text-muted-foreground">
                    <I18nText text={'Queued:'} />{' '}
                  </span>
                  <RelativeTime
                    timestamp={dagRun.queuedAt}
                    absolute={dagRun.queuedAt}
                  />
                </div>
                <div>
                  <span className="text-muted-foreground">
                    <I18nText text={'Started:'} />{' '}
                  </span>
                  <RelativeTime
                    timestamp={dagRun.startedAt}
                    absolute={dagRun.startedAt}
                  />
                </div>
              </div>
              <div className="text-left flex items-center gap-1.5">
                <span className="text-muted-foreground">
                  <I18nText text={'Duration:'} />{' '}
                </span>
                <span className="flex items-center gap-1">
                  {calculateDuration(
                    dagRun.startedAt,
                    dagRun.finishedAt,
                    dagRun.status
                  )}
                </span>
              </div>
              {dagRun.workerId && (
                <div className="text-left">
                  <span className="text-muted-foreground">
                    <I18nText text={'Worker:'} />{' '}
                  </span>
                  {dagRun.workerId}
                </div>
              )}
              {dagRun.profileName && (
                <div className="text-left flex min-w-0 items-center gap-1.5">
                  <span className="text-muted-foreground">
                    <I18nText text={'Profile:'} />{' '}
                  </span>
                  <span
                    className="inline-flex min-w-0 items-center gap-1 font-mono"
                    title={dagRun.profileName}
                  >
                    <SlidersHorizontal className="h-3 w-3 shrink-0 text-muted-foreground" />
                    <span className="truncate">{dagRun.profileName}</span>
                  </span>
                </div>
              )}
            </div>

            {/* Timezone info */}
            <div className="text-xs text-muted-foreground text-right pt-1">
              {timezoneInfo}
            </div>
          </div>
        ))}
      </div>
    );
  }

  // Table view for larger screens
  return (
    <div ref={tableRef}>
      <Table className="w-full text-xs">
        <TableHeader>
          <TableRow>
            {onToggleBulkSelect && (
              <TableHead className="w-10">
                <I18nText text={'Select'} />
              </TableHead>
            )}
            <TableHead>
              <I18nText text={'Workflow name'} />
            </TableHead>
            <TableHead>
              <I18nText text={'Run ID'} />
            </TableHead>
            <TableHead>
              <I18nText text={'Status'} />
            </TableHead>
            <TableHead>
              <I18nText text={'Trigger'} />
            </TableHead>
            {showProfileColumn && (
              <TableHead>
                <I18nText text={'Profile'} />
              </TableHead>
            )}
            {showScheduleColumn && (
              <TableHead>
                <div>
                  <I18nText text={'Scheduled At'} />
                </div>
                <div className="text-xs text-muted-foreground font-normal">
                  {timezoneInfo}
                </div>
              </TableHead>
            )}
            <TableHead>
              <div>
                <I18nText text={'Queued At'} />
              </div>
              <div className="text-xs text-muted-foreground font-normal">
                {timezoneInfo}
              </div>
            </TableHead>
            <TableHead>
              <div>
                <I18nText text={'Started At'} />
              </div>
              <div className="text-xs text-muted-foreground font-normal">
                {timezoneInfo}
              </div>
            </TableHead>
            <TableHead>
              <I18nText text={'Duration'} />
            </TableHead>
            <TableHead>
              <I18nText text={'Worker'} />
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {dagRuns.map((dagRun, index) => (
            <TableRow
              key={dagRun.dagRunId}
              className={`cursor-pointer hover:bg-muted/50 border-l-4 ${
                selectedIndex === index
                  ? 'border-l-border'
                  : 'border-l-transparent'
              } ${isBulkSelected(dagRun) ? 'bg-muted/30' : ''} ${dagRun.status === Status.Running ? 'animate-running-row' : ''}`}
              style={{ fontSize: '0.8125rem' }}
              onClick={(e) => {
                setSelectedIndex(index);
                openPage(runURL(dagRun), e);
              }}
            >
              {onToggleBulkSelect && (
                <TableCell className="py-1 px-2">
                  <div
                    className="flex h-8 w-8 items-center justify-center rounded-md transition-colors hover:bg-muted focus-within:ring-2 focus-within:ring-ring"
                    onClick={(event) => {
                      event.stopPropagation();
                      if (event.target !== event.currentTarget) {
                        return;
                      }
                      onToggleBulkSelect({
                        name: dagRun.name,
                        dagRunId: dagRun.dagRunId,
                      });
                    }}
                  >
                    <Checkbox
                      aria-label={`Select DAG run ${dagRun.name} ${dagRun.dagRunId}`}
                      checked={isBulkSelected(dagRun)}
                      onCheckedChange={() =>
                        onToggleBulkSelect({
                          name: dagRun.name,
                          dagRunId: dagRun.dagRunId,
                        })
                      }
                      className="h-5 w-5 pointer-events-none"
                    />
                  </div>
                </TableCell>
              )}
              <TableCell className="py-1 px-2 font-normal">
                <div className="flex items-center gap-2">
                  <Link
                    to={runURL(dagRun)}
                    className="min-w-0 truncate hover:underline"
                    onClick={(event) => event.stopPropagation()}
                  >
                    {dagRun.name}
                  </Link>
                  <DAGRunArtifactsButton
                    dagRun={dagRun}
                    to={runURL(dagRun, 'artifacts')}
                  />
                </div>
              </TableCell>
              <TableCell className="py-1 px-2 font-mono text-muted-foreground">
                {dagRun.dagRunId}
              </TableCell>
              <TableCell className="py-1 px-2">
                <StepDetailsTooltip dagRun={dagRun}>
                  <div className="flex flex-wrap items-center gap-1">
                    <RunStatusChip
                      name={dagRun.name}
                      dagRunId={dagRun.dagRunId}
                      status={dagRun.status}
                      size="xs"
                    >
                      {dagRun.statusLabel}
                    </RunStatusChip>
                    <AutoRetryBadge
                      status={dagRun.status}
                      count={dagRun.autoRetryCount}
                      limit={dagRun.autoRetryLimit}
                      className="text-[10px]"
                    />
                  </div>
                </StepDetailsTooltip>
              </TableCell>
              <TableCell className="py-1 px-2">
                <TriggerTypeIndicator
                  type={dagRun.triggerType}
                  actor={dagRun.triggerActor}
                />
              </TableCell>
              {showProfileColumn && (
                <TableCell className="py-1 px-2">
                  {dagRun.profileName ? (
                    <span
                      className="inline-flex max-w-[160px] items-center gap-1 font-mono text-xs"
                      title={dagRun.profileName}
                    >
                      <SlidersHorizontal className="h-3 w-3 shrink-0 text-muted-foreground" />
                      <span className="truncate">{dagRun.profileName}</span>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">-</span>
                  )}
                </TableCell>
              )}
              {showScheduleColumn && (
                <TableCell className="py-1 px-2 text-left whitespace-nowrap">
                  {dagRun.scheduleTime ? (
                    <span title={dagRun.scheduleTime}>
                      {formatScheduleTime(dagRun.scheduleTime)}
                    </span>
                  ) : (
                    '-'
                  )}
                </TableCell>
              )}
              <TableCell className="py-1 px-2 text-left whitespace-nowrap">
                <RelativeTime
                  timestamp={dagRun.queuedAt}
                  absolute={dagRun.queuedAt}
                />
              </TableCell>
              <TableCell className="py-1 px-2 text-left whitespace-nowrap">
                <RelativeTime
                  timestamp={dagRun.startedAt}
                  absolute={dagRun.startedAt}
                />
              </TableCell>
              <TableCell className="py-1 px-2 text-left">
                <div className="flex items-center gap-1">
                  {calculateDuration(
                    dagRun.startedAt,
                    dagRun.finishedAt,
                    dagRun.status
                  )}
                </div>
              </TableCell>
              <TableCell className="py-1 px-2 text-muted-foreground">
                {dagRun.workerId || '-'}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

export default DAGRunTable;
