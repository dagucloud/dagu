// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Checkbox } from '@/components/ui/checkbox';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useMemo, useState } from 'react';
import { components, Status } from '../../../../api/v1/schema';
import dayjs from '../../../../lib/dayjs';
import { getDAGRunScheduleSortValue } from '../../../../lib/dagRunTiming';
import RelativeTime from '@/components/ui/relative-time';
import StatusChip from '@/components/ui/status-chip';
import AutoRetryBadge from '../common/AutoRetryBadge';
import {
  DAGRunSelectionItem,
  getDAGRunSelectionKey,
} from '../../hooks/useBulkDAGRunSelection';
import { StepDetailsTooltip } from './StepDetailsTooltip';
import { DAGRunArtifactsButton } from './DAGRunArtifactsButton';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

interface DAGRunGroupedViewProps {
  dagRuns: components['schemas']['DAGRunSummary'][];
  /** True while the first page is being fetched; suppresses the empty state. */
  isLoading?: boolean;
  selectedRunKeys?: Set<string>;
  selectedDAGRun?: { name: string; dagRunId: string } | null;
  onSelectDAGRun?: (dagRun: { name: string; dagRunId: string } | null) => void;
  onViewArtifacts?: (dagRun: DAGRunSelectionItem) => void;
  onToggleBulkSelect?: (dagRun: DAGRunSelectionItem) => void;
}

interface GroupedDAGRuns {
  [dagName: string]: components['schemas']['DAGRunSummary'][];
}

function DAGRunGroupedView({
  dagRuns,
  isLoading = false,
  selectedRunKeys,
  selectedDAGRun = null,
  onSelectDAGRun,
  onViewArtifacts,
  onToggleBulkSelect,
}: DAGRunGroupedViewProps) {
  const { ts } = useI18n();
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());

  // Group DAG runs by name
  const groupedDAGRuns = useMemo(() => {
    const groups: GroupedDAGRuns = {};
    dagRuns.forEach((dagRun) => {
      if (!groups[dagRun.name]) {
        groups[dagRun.name] = [];
      }
      const runsList = groups[dagRun.name];
      if (runsList) {
        runsList.push(dagRun);
      }
    });
    // Sort runs within each group by scheduled time, then queued time.
    Object.keys(groups).forEach((dagName) => {
      const runs = groups[dagName];
      if (runs) {
        runs.sort((a, b) => {
          return getDAGRunScheduleSortValue(b) - getDAGRunScheduleSortValue(a);
        });
      }
    });
    return groups;
  }, [dagRuns]);

  const toggleGroup = (dagName: string) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(dagName)) {
        next.delete(dagName);
      } else {
        next.add(dagName);
      }
      return next;
    });
  };

  // Calculate duration between start and finish times
  const calculateDuration = (
    startedAt: string,
    finishedAt: string | null,
    status: number
  ): string => {
    if (!startedAt) {
      return '-';
    }

    if (status === Status.Running && !finishedAt) {
      const start = dayjs(startedAt);
      const now = dayjs();
      const durationMs = now.diff(start);
      return formatDuration(durationMs);
    }

    if (finishedAt) {
      const start = dayjs(startedAt);
      const end = dayjs(finishedAt);
      const durationMs = end.diff(start);
      return formatDuration(durationMs);
    }

    return '-';
  };

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

  // Get the most recent run for each group for summary display
  const getGroupSummary = (runs: components['schemas']['DAGRunSummary'][]) => {
    if (!runs || runs.length === 0) {
      return null;
    }

    const latestRun = runs[0];
    if (!latestRun) {
      return null;
    }

    const failedCount = runs.filter((r) => r.status === Status.Failed).length;
    const abortedCount = runs.filter((r) => r.status === Status.Aborted).length;
    const queuedCount = runs.filter((r) => r.status === Status.Queued).length;
    const runningCount = runs.filter((r) => r.status === Status.Running).length;

    // Check if all runs have the same status
    const firstStatus = runs[0]?.status;
    const allSameStatus =
      firstStatus !== undefined && runs.every((r) => r.status === firstStatus);

    return {
      latestRun,
      failedCount,
      abortedCount,
      queuedCount,
      runningCount,
      totalCount: runs.length,
      allSameStatus,
      uniformStatus: allSameStatus ? firstStatus : null,
    };
  };

  // Empty state component
  const EmptyState = () => (
    <div className="flex flex-col items-center justify-center py-12 px-4 border rounded-md bg-card">
      <div className="text-6xl mb-4">🔍</div>
      <h3 className="text-lg font-normal text-foreground mb-2">
        <I18nText text={'No DAG runs found'} />
      </h3>
      <p className="text-sm text-muted-foreground text-center max-w-md mb-4">
        <I18nText
          text={
            'No DAG runs in the selected time range. Adjust the date range or filters, or start a workflow from the Workflows page.'
          }
        />
      </p>
    </div>
  );

  if (dagRuns.length === 0) {
    if (isLoading) {
      return (
        <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
          <I18nText text={'Loading DAG runs...'} />
        </div>
      );
    }
    return <EmptyState />;
  }

  const sortedDagNames = Object.keys(groupedDAGRuns).sort();
  const isBulkSelected = (dagRun: DAGRunSelectionItem) =>
    selectedRunKeys?.has(getDAGRunSelectionKey(dagRun)) ?? false;
  const isFocused = (dagRun: DAGRunSelectionItem) =>
    selectedDAGRun?.name === dagRun.name &&
    selectedDAGRun?.dagRunId === dagRun.dagRunId;

  return (
    <div className="border rounded-md bg-card">
      <div className="divide-y divide-border">
        {sortedDagNames.map((dagName) => {
          const runs = groupedDAGRuns[dagName];
          if (!runs) return null;

          const summary = getGroupSummary(runs);
          if (!summary) return null;

          const isExpanded = expandedGroups.has(dagName);

          return (
            <div key={dagName}>
              {/* Group Header */}
              <div
                className="flex items-center justify-between p-3 cursor-pointer hover:bg-muted/30 transition-colors"
                onClick={() => toggleGroup(dagName)}
              >
                <div className="flex items-center gap-2 flex-1 min-w-0">
                  <button
                    className="flex-shrink-0 p-1 hover:bg-muted rounded"
                    onClick={(e) => {
                      e.stopPropagation();
                      toggleGroup(dagName);
                    }}
                  >
                    {isExpanded ? (
                      <ChevronDown
                        size={16}
                        className="text-muted-foreground"
                      />
                    ) : (
                      <ChevronRight
                        size={16}
                        className="text-muted-foreground"
                      />
                    )}
                  </button>
                  <div className="flex-1 min-w-0">
                    <div className="font-medium text-sm truncate">
                      {dagName}
                    </div>
                    <div className="text-xs text-muted-foreground">
                      {ts(
                        summary.totalCount === 1
                          ? '{count} run'
                          : '{count} runs',
                        { count: summary.totalCount }
                      )}
                      {!summary.allSameStatus && (
                        <>
                          {summary.runningCount > 0 && (
                            <span className="ml-2">
                              {summary.runningCount}{' '}
                              <I18nText text={'running'} />
                            </span>
                          )}
                          {summary.failedCount > 0 && (
                            <span className="ml-2">
                              {summary.failedCount} <I18nText text={'failed'} />
                            </span>
                          )}
                          {summary.abortedCount > 0 && (
                            <span className="ml-2">
                              {summary.abortedCount}{' '}
                              <I18nText text={'aborted'} />
                            </span>
                          )}
                          {summary.queuedCount > 0 && (
                            <span className="ml-2">
                              {summary.queuedCount} <I18nText text={'queued'} />
                            </span>
                          )}
                        </>
                      )}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2 flex-shrink-0">
                  {summary.allSameStatus && summary.uniformStatus != null ? (
                    <StatusChip status={summary.uniformStatus} size="xs">
                      {summary.latestRun.statusLabel}
                    </StatusChip>
                  ) : (
                    <StatusChip status={undefined} size="xs">
                      <I18nText text={'Mixed'} />
                    </StatusChip>
                  )}
                </div>
              </div>

              {/* Expanded Runs List */}
              {isExpanded && (
                <div className="bg-muted/10">
                  <div className="divide-y divide-border/50">
                    {runs.map((dagRun) => (
                      <div
                        key={dagRun.dagRunId}
                        className={`px-3 py-2 pl-11 hover:bg-muted/20 cursor-pointer transition-colors text-xs ${
                          isFocused(dagRun) ? 'bg-muted/20' : ''
                        } ${isBulkSelected(dagRun) ? 'bg-muted/30' : ''}`}
                        onClick={(e) => {
                          if (e.ctrlKey || e.metaKey) {
                            window.open(
                              `/dag-runs/${dagRun.name}/${dagRun.dagRunId}`,
                              '_blank'
                            );
                          } else if (onSelectDAGRun) {
                            onSelectDAGRun({
                              name: dagRun.name,
                              dagRunId: dagRun.dagRunId,
                            });
                          }
                        }}
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div className="flex flex-1 items-start gap-3 min-w-0">
                            {onToggleBulkSelect && (
                              <div
                                className="pt-0.5"
                                onClick={(e) => e.stopPropagation()}
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
                                />
                              </div>
                            )}
                            <div className="flex-1 min-w-0 space-y-1">
                              <div className="font-mono text-muted-foreground truncate">
                                {dagRun.dagRunId}
                              </div>
                              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                                {dagRun.scheduleTime && (
                                  <div className="whitespace-nowrap">
                                    <span className="text-muted-foreground">
                                      <I18nText text={'Scheduled:'} />{' '}
                                    </span>
                                    {dagRun.scheduleTime}
                                  </div>
                                )}
                                <div className="whitespace-nowrap">
                                  <span className="text-muted-foreground">
                                    <I18nText text={'Queued:'} />{' '}
                                  </span>
                                  <RelativeTime
                                    timestamp={dagRun.queuedAt}
                                    absolute={dagRun.queuedAt}
                                  />
                                </div>
                                <div className="whitespace-nowrap">
                                  <span className="text-muted-foreground">
                                    <I18nText text={'Started:'} />{' '}
                                  </span>
                                  <RelativeTime
                                    timestamp={dagRun.startedAt}
                                    absolute={dagRun.startedAt}
                                  />
                                </div>
                                <div className="flex items-center gap-1 whitespace-nowrap">
                                  <span className="text-muted-foreground">
                                    <I18nText text={'Duration:'} />{' '}
                                  </span>
                                  {calculateDuration(
                                    dagRun.startedAt,
                                    dagRun.finishedAt,
                                    dagRun.status
                                  )}
                                  {dagRun.status === Status.Running &&
                                    dagRun.startedAt && (
                                      <span className="inline-block w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
                                    )}
                                </div>
                              </div>
                            </div>
                          </div>
                          <div className="mt-0.5 flex flex-shrink-0 items-start gap-2">
                            {onViewArtifacts && (
                              <DAGRunArtifactsButton
                                dagRun={dagRun}
                                onClick={() => onViewArtifacts(dagRun)}
                              />
                            )}
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
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

export default DAGRunGroupedView;
