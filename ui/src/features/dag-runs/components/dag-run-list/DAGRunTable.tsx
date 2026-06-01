// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';
import { Checkbox } from '@/components/ui/checkbox';
import { useNavigate } from 'react-router-dom';
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
import dayjs from '../../../../lib/dayjs';
import StatusChip from '@/components/ui/status-chip';
import AutoRetryBadge from '../common/AutoRetryBadge';
import { TriggerTypeIndicator } from '../../../dags/components/common/TriggerTypeIndicator';
import {
  DAGRunSelectionItem,
  getDAGRunSelectionKey,
} from '../../hooks/useBulkDAGRunSelection';
import { StepDetailsTooltip } from './StepDetailsTooltip';

interface DAGRunTableProps {
  dagRuns: components['schemas']['DAGRunSummary'][];
  selectedRunKeys?: Set<string>;
  selectedDAGRun?: { name: string; dagRunId: string } | null;
  onSelectDAGRun?: (dagRun: { name: string; dagRunId: string } | null) => void;
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
      'input, textarea, select, button, a, [role="button"], [role="combobox"], [role="textbox"], [contenteditable="true"]'
    )
  );
}

function DAGRunTable({
  dagRuns,
  selectedRunKeys,
  selectedDAGRun = null,
  onSelectDAGRun,
  onToggleBulkSelect,
}: DAGRunTableProps) {
  const config = useConfig();
  const navigate = useNavigate();
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

  // Update selectedIndex when selectedDAGRun changes from parent
  useEffect(() => {
    if (selectedDAGRun) {
      const index = dagRuns.findIndex(
        (item) => item.dagRunId === selectedDAGRun.dagRunId
      );
      if (index !== -1 && index !== selectedIndex) {
        setSelectedIndex(index);
        scrollToSelectedRow(index);
      }
    }
  }, [selectedDAGRun, dagRuns]);

  // Keyboard navigation - works both when panel is open and closed
  useEffect(() => {
    if (!onSelectDAGRun) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (isInteractiveEventTarget(event.target)) {
        return;
      }

      // Find current index based on selectedDAGRun or selectedIndex
      const currentIdx = selectedDAGRun
        ? dagRuns.findIndex((item) => item.dagRunId === selectedDAGRun.dagRunId)
        : selectedIndex;

      if (event.key === 'ArrowDown') {
        event.preventDefault();
        const newIndex =
          currentIdx < dagRuns.length - 1 ? currentIdx + 1 : currentIdx;
        if (newIndex !== currentIdx) {
          setSelectedIndex(newIndex);
          scrollToSelectedRow(newIndex);
          // If panel is open, navigate to new item
          if (selectedDAGRun && dagRuns[newIndex]) {
            onSelectDAGRun({
              name: dagRuns[newIndex].name,
              dagRunId: dagRuns[newIndex].dagRunId,
            });
          }
        }
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        let newIndex: number;
        if (currentIdx > 0) {
          newIndex = currentIdx - 1;
        } else if (currentIdx === -1) {
          newIndex = 0;
        } else {
          newIndex = currentIdx;
        }
        if (newIndex !== currentIdx || currentIdx === -1) {
          setSelectedIndex(newIndex);
          scrollToSelectedRow(newIndex);
          const dagRunAtNewIndex = dagRuns[newIndex];
          if (selectedDAGRun && dagRunAtNewIndex) {
            onSelectDAGRun({
              name: dagRunAtNewIndex.name,
              dagRunId: dagRunAtNewIndex.dagRunId,
            });
          }
        }
      } else if (event.key === 'Enter' && !selectedDAGRun && currentIdx >= 0) {
        const selectedItem = dagRuns[currentIdx];
        if (selectedItem) {
          onSelectDAGRun({
            name: selectedItem.name,
            dagRunId: selectedItem.dagRunId,
          });
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [selectedDAGRun, dagRuns, selectedIndex, onSelectDAGRun]);

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
        No DAG runs found
      </h3>
      <p className="text-sm text-muted-foreground text-center max-w-md mb-4">
        There are no DAG runs matching your current filters. Try adjusting your
        search criteria or date range.
      </p>
    </div>
  );

  // If there are no DAG runs, show empty state
  if (dagRuns.length === 0) {
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
            onClick={(e) => {
              // Navigate directly to DAG-run page with correct URL pattern
              if (e.metaKey || e.ctrlKey) {
                // Open in new tab if Cmd/Ctrl is pressed
                window.open(
                  `/dag-runs/${dagRun.name}/${dagRun.dagRunId}`,
                  '_blank'
                );
              } else {
                // Use React Router for SPA navigation
                navigate(`/dag-runs/${dagRun.name}/${dagRun.dagRunId}`);
              }
            }}
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
                <div className="font-normal text-sm">{dagRun.name}</div>
              </div>
              <StepDetailsTooltip dagRun={dagRun}>
                <div className="flex flex-col items-end gap-1">
                  <StatusChip status={dagRun.status} size="xs">
                    {dagRun.statusLabel}
                  </StatusChip>
                  <AutoRetryBadge
                    status={dagRun.status}
                    count={dagRun.autoRetryCount}
                    limit={dagRun.autoRetryLimit}
                    className="text-[10px]"
                  />
                </div>
              </StepDetailsTooltip>
            </div>

            {/* DAG-run ID and Trigger */}
            <div className="flex items-center justify-between text-xs mb-2 min-w-0">
              <span className="font-mono text-muted-foreground truncate">
                {dagRun.dagRunId}
              </span>
              <TriggerTypeIndicator type={dagRun.triggerType} />
            </div>

            {/* Timestamps */}
            <div className="space-y-1 text-xs mt-2">
              {dagRun.scheduleTime && (
                <div className="flex justify-between items-center">
                  <div className="whitespace-normal break-words">
                    <span className="text-muted-foreground">Scheduled: </span>
                    {dagRun.scheduleTime}
                  </div>
                </div>
              )}
              <div className="flex justify-between items-center">
                <div>
                  <span className="text-muted-foreground">Queued: </span>
                  {dagRun.queuedAt || '-'}
                </div>
                <div>
                  <span className="text-muted-foreground">Started: </span>
                  {dagRun.startedAt || '-'}
                </div>
              </div>
              <div className="text-left flex items-center gap-1.5">
                <span className="text-muted-foreground">Duration: </span>
                <span className="flex items-center gap-1">
                  {calculateDuration(
                    dagRun.startedAt,
                    dagRun.finishedAt,
                    dagRun.status
                  )}
                  {dagRun.status === Status.Running && dagRun.startedAt && (
                    <span className="inline-block w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
                  )}
                </span>
              </div>
              {dagRun.workerId && (
                <div className="text-left">
                  <span className="text-muted-foreground">Worker: </span>
                  {dagRun.workerId}
                </div>
              )}
              {dagRun.profileName && (
                <div className="text-left flex min-w-0 items-center gap-1.5">
                  <span className="text-muted-foreground">Profile: </span>
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
              <TableHead className="w-10">Select</TableHead>
            )}
            <TableHead>DAG Name</TableHead>
            <TableHead>Run ID</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Trigger</TableHead>
            {showProfileColumn && <TableHead>Profile</TableHead>}
            {showScheduleColumn && (
              <TableHead>
                <div>Scheduled At</div>
                <div className="text-xs text-muted-foreground font-normal">
                  {timezoneInfo}
                </div>
              </TableHead>
            )}
            <TableHead>
              <div>Queued At</div>
              <div className="text-xs text-muted-foreground font-normal">
                {timezoneInfo}
              </div>
            </TableHead>
            <TableHead>
              <div>Started At</div>
              <div className="text-xs text-muted-foreground font-normal">
                {timezoneInfo}
              </div>
            </TableHead>
            <TableHead>Duration</TableHead>
            <TableHead>Worker</TableHead>
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
                if (e.ctrlKey || e.metaKey) {
                  // Open in new tab
                  window.open(
                    `/dag-runs/${dagRun.name}/${dagRun.dagRunId}`,
                    '_blank'
                  );
                } else if (isSmallScreen) {
                  // On small screens, navigate to full page
                  navigate(`/dag-runs/${dagRun.name}/${dagRun.dagRunId}`);
                } else if (onSelectDAGRun) {
                  // Select the DAG run
                  setSelectedIndex(index);
                  onSelectDAGRun({
                    name: dagRun.name,
                    dagRunId: dagRun.dagRunId,
                  });
                }
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
                {dagRun.name}
              </TableCell>
              <TableCell className="py-1 px-2 font-mono text-muted-foreground">
                {dagRun.dagRunId}
              </TableCell>
              <TableCell className="py-1 px-2">
                <StepDetailsTooltip dagRun={dagRun}>
                  <div className="flex flex-wrap items-center gap-1">
                    <StatusChip status={dagRun.status} size="xs">
                      {dagRun.statusLabel}
                    </StatusChip>
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
                <TriggerTypeIndicator type={dagRun.triggerType} />
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
                <TableCell className="py-1 px-2 text-left whitespace-normal break-words">
                  {dagRun.scheduleTime || '-'}
                </TableCell>
              )}
              <TableCell className="py-1 px-2 text-left">
                {dagRun.queuedAt || '-'}
              </TableCell>
              <TableCell className="py-1 px-2 text-left">
                {dagRun.startedAt || '-'}
              </TableCell>
              <TableCell className="py-1 px-2 text-left">
                <div className="flex items-center gap-1">
                  {calculateDuration(
                    dagRun.startedAt,
                    dagRun.finishedAt,
                    dagRun.status
                  )}
                  {dagRun.status === Status.Running && dagRun.startedAt && (
                    <span className="inline-block w-2 h-2 rounded-full bg-success animate-pulse" />
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
