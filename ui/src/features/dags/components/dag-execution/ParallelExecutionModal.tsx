// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { ExternalLink, Layers } from 'lucide-react';
import React, { useMemo, useState } from 'react';
import { components, StatusLabel } from '../../../../api/v1/schema';
import { STATUS_DISPLAY_LABELS, StatusDot } from '../common';

type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];

// "all" is a special filter that shows everything
type StatusFilterValue = 'all' | StatusLabel;

type Props = {
  isOpen: boolean;
  onClose: () => void;
  stepName: string;
  subDAGName: string;
  subRuns: SubDAGRun[];
  onSelectSubRun: (subRunIndex: number, openInNewTab?: boolean) => void;
  /** Root DAG name for API calls */
  rootDagName: string;
  /** Root DAG run ID for API calls */
  rootDagRunId: string;
  /** Current DAG run ID (parent of sub-runs) - for multi-level nested DAGs */
  parentDagRunId?: string;
};

export function ParallelExecutionModal({
  isOpen,
  onClose,
  subDAGName,
  subRuns,
  onSelectSubRun,
  rootDagName,
  rootDagRunId,
  parentDagRunId,
}: Props) {
  const isMac = navigator.platform.toUpperCase().indexOf('MAC') >= 0;
  const [selectedIndex, setSelectedIndex] = useState<number | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilterValue>('all');
  const scrollContainerRef = React.useRef<HTMLDivElement>(null);
  const remoteNode = useRemoteNode();

  // Determine if this is a nested sub-DAG
  const isNestedSubDAG = parentDagRunId && parentDagRunId !== rootDagRunId;

  // Fetch sub DAG run details with status information
  const { data: subRunsData } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
    whenEnabled(isOpen, {
      params: {
        path: {
          name: rootDagName,
          dagRunId: rootDagRunId,
        },
        query: {
          remoteNode,
          parentSubDAGRunId: isNestedSubDAG ? parentDagRunId : undefined,
        },
      },
    }),
    {
      refreshInterval: isOpen ? 3000 : 0,
    }
  );

  // Create a map of dagRunId to status details
  const statusMap = useMemo(() => {
    const map = new Map<string, SubDAGRunDetail>();
    if (subRunsData?.subRuns) {
      for (const detail of subRunsData.subRuns) {
        map.set(detail.dagRunId, detail);
      }
    }
    return map;
  }, [subRunsData]);

  // Count sub runs by statusLabel - dynamically build from actual data
  const statusCounts = useMemo(() => {
    const counts = new Map<StatusFilterValue, number>();
    counts.set('all', subRuns.length);

    for (const subRun of subRuns) {
      const detail = statusMap.get(subRun.dagRunId);
      if (!detail) continue;

      const statusLabel = detail.statusLabel;
      counts.set(statusLabel, (counts.get(statusLabel) || 0) + 1);
    }

    return counts;
  }, [subRuns, statusMap]);

  // Filter sub runs based on status filter
  // Keep track of original index for navigation
  const filteredSubRuns = useMemo(() => {
    if (statusFilter === 'all') {
      return subRuns.map((subRun, index) => ({ subRun, originalIndex: index }));
    }

    return subRuns
      .map((subRun, index) => ({ subRun, originalIndex: index }))
      .filter(({ subRun }) => {
        const detail = statusMap.get(subRun.dagRunId);
        if (!detail) return false;
        return detail.statusLabel === statusFilter;
      });
  }, [subRuns, statusFilter, statusMap]);

  // Get available filters (only show filters that have items)
  const availableFilters = useMemo(() => {
    const filters: {
      value: StatusFilterValue;
      label: string;
      count: number;
    }[] = [{ value: 'all', label: 'All', count: statusCounts.get('all') || 0 }];

    // Add filters for each status that has items, in a consistent order
    const statusOrder: StatusLabel[] = [
      StatusLabel.running,
      StatusLabel.queued,
      StatusLabel.succeeded,
      StatusLabel.partially_succeeded,
      StatusLabel.failed,
      StatusLabel.aborted,
      StatusLabel.not_started,
    ];

    for (const status of statusOrder) {
      const count = statusCounts.get(status);
      if (count && count > 0) {
        filters.push({
          value: status,
          label: STATUS_DISPLAY_LABELS[status],
          count,
        });
      }
    }

    return filters;
  }, [statusCounts]);

  // Handle keyboard navigation
  React.useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (filteredSubRuns.length === 0) {
        if (
          e.key === 'ArrowDown' ||
          e.key === 'ArrowUp' ||
          e.key === 'Enter'
        ) {
          e.preventDefault();
          setSelectedIndex(null);
        }
        return;
      }

      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setSelectedIndex((prev) =>
            prev === null ? 0 : (prev + 1) % filteredSubRuns.length
          );
          break;
        case 'ArrowUp':
          e.preventDefault();
          setSelectedIndex((prev) =>
            prev === null
              ? filteredSubRuns.length - 1
              : (prev - 1 + filteredSubRuns.length) % filteredSubRuns.length
          );
          break;
        case 'Enter':
          e.preventDefault();
          if (selectedIndex !== null && filteredSubRuns[selectedIndex]) {
            const openInNewTab = e.metaKey || e.ctrlKey;
            onSelectSubRun(
              filteredSubRuns[selectedIndex].originalIndex,
              openInNewTab
            );
            if (!openInNewTab) {
              onClose();
            }
          }
          break;
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, selectedIndex, filteredSubRuns, onSelectSubRun, onClose]);

  // Reset selected index when filter changes
  React.useEffect(() => {
    setSelectedIndex(null);
  }, [statusFilter]);

  // Auto-scroll to selected item
  React.useEffect(() => {
    if (selectedIndex !== null && scrollContainerRef.current) {
      const container = scrollContainerRef.current;
      const selectedElement = container.children[selectedIndex] as HTMLElement;

      if (selectedElement) {
        // Use scrollIntoView for more reliable scrolling
        selectedElement.scrollIntoView({
          block: 'nearest',
          behavior: 'smooth',
        });
      }
    }
  }, [selectedIndex]);

  return (
    <Dialog open={isOpen} onOpenChange={onClose}>
      <DialogContent className="sm:max-w-[800px] overflow-hidden p-0">
        <div className="p-4 border-b border-border">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-lg font-semibold">
              <Layers className="h-4 w-4 text-info" />
              {subDAGName}
            </DialogTitle>
            <DialogDescription className="text-xs mt-1 font-mono text-muted-foreground">
              {filteredSubRuns.length === subRuns.length
                ? `${subRuns.length} sub DAG-runs`
                : `${filteredSubRuns.length} of ${subRuns.length} sub DAG-runs`}
            </DialogDescription>
          </DialogHeader>

          {/* Status filter buttons - only show when there's more than one status */}
          {availableFilters.length > 2 && (
            <div className="flex items-center gap-1 mt-3 flex-wrap">
              {availableFilters.map((filter) => {
                const isActive = statusFilter === filter.value;

                return (
                  <button
                    key={filter.value}
                    onClick={() => setStatusFilter(filter.value)}
                    className={`
                      px-2 py-1 text-xs font-medium rounded transition-colors
                      ${
                        isActive
                          ? 'bg-info-muted text-info'
                          : 'bg-muted text-muted-foreground hover:bg-accent'
                      }
                    `}
                  >
                    {filter.label}
                    <span
                      className={`ml-1 ${isActive ? 'text-info' : 'text-muted-foreground'}`}
                    >
                      {filter.count}
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>

        <div className="p-3">
          <div
            ref={scrollContainerRef}
            className="space-y-1 max-h-[400px] overflow-y-auto"
          >
            {filteredSubRuns.length === 0 ? (
              <div className="text-center py-8 text-sm text-muted-foreground">
                No sub DAG-runs match the selected filter
              </div>
            ) : (
              filteredSubRuns.map(({ subRun, originalIndex }, displayIndex) => {
                const detail = statusMap.get(subRun.dagRunId);
                return (
                  <div
                    key={subRun.dagRunId}
                    className="group relative flex items-center gap-2"
                    onMouseEnter={() => setSelectedIndex(displayIndex)}
                  >
                    <button
                      className={`
                        flex-1 text-left transition-all duration-150 border rounded px-3 py-2 flex items-center gap-3 focus:outline-none
                        ${
                          selectedIndex === displayIndex
                            ? 'border-info bg-info-muted'
                            : 'border-transparent hover:border-border hover:bg-muted'
                        }
                      `}
                      onClick={(e) => {
                        const openInNewTab = e.metaKey || e.ctrlKey;
                        onSelectSubRun(originalIndex, openInNewTab);
                        if (!openInNewTab) {
                          onClose();
                        }
                      }}
                    >
                      <span className="font-mono text-xs text-muted-foreground min-w-[24px] flex-shrink-0">
                        {String(originalIndex + 1).padStart(2, '0')}
                      </span>
                      {detail && (
                        <span className="flex-shrink-0">
                          <StatusDot
                            status={detail.status}
                            statusLabel={detail.statusLabel}
                          />
                        </span>
                      )}
                      <div className="flex-1 min-w-0 overflow-x-auto">
                        {subRun.params ? (
                          <code className="text-sm font-mono text-muted-foreground whitespace-nowrap">
                            {subRun.params}
                          </code>
                        ) : (
                          <span className="text-sm text-muted-foreground italic">
                            No parameters
                          </span>
                        )}
                      </div>
                    </button>
                    <button
                      className="opacity-0 group-hover:opacity-100 transition-opacity duration-150 p-1.5 rounded hover:bg-muted focus:outline-none"
                      onClick={() => {
                        onSelectSubRun(originalIndex, true);
                      }}
                      title="Open in new tab"
                    >
                      <ExternalLink className="h-3 w-3 text-muted-foreground" />
                    </button>
                  </div>
                );
              })
            )}
          </div>
        </div>

        <div className="px-4 py-2 bg-muted border-t border-border">
          <div className="flex items-center gap-3 text-xs text-muted-foreground font-mono">
            <span>{isMac ? '⌘' : 'Ctrl'}+Click: new tab</span>
            <span className="opacity-40">•</span>
            <span>↑↓ Navigate</span>
            <span className="opacity-40">•</span>
            <span>Enter: select</span>
            <span className="opacity-40">•</span>
            <span>ESC: close</span>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
