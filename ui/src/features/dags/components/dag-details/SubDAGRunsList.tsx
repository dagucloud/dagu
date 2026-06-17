import { components, StatusLabel } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import dayjs from '@/lib/dayjs';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { StatusDot } from '../common/StatusDot';
import { STATUS_DISPLAY_LABELS } from '../common/statusLabels';

type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];
type IndexedSubRun = SubDAGRun & { originalIndex: number };
type IndexedSubRunDetail = SubDAGRunDetail & { originalIndex: number };
type SubRunListItem = IndexedSubRun | IndexedSubRunDetail;

// "all" is a special filter that shows everything
type StatusFilterValue = 'all' | StatusLabel;

type Props = {
  /** Current DAG name (the parent of sub-runs) - used for display */
  dagName: string;
  /** Current DAG run ID (the parent of sub-runs) - used for filtering */
  dagRunId: string;
  /** Root DAG name - used for API calls */
  rootDagName: string;
  /** Root DAG run ID - used for API calls */
  rootDagRunId: string;
  subDagName: string;
  allSubRuns: SubDAGRun[];
  isExpanded: boolean;
  onToggleExpand: () => void;
  onNavigate: (index: number, e?: React.MouseEvent) => void;
};

export function SubDAGRunsList({
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  dagName: _dagName,
  dagRunId,
  rootDagName,
  rootDagRunId,
  subDagName,
  allSubRuns,
  isExpanded,
  onToggleExpand,
  onNavigate,
}: Props) {
  const remoteNode = useRemoteNode();
  const dagRunIdsKey = allSubRuns.map((sr) => sr.dagRunId).join('|');
  const [statusFilter, setStatusFilter] = useState<StatusFilterValue>('all');

  // Fetch sub DAG run details with timing information
  // Only fetch if we have multiple sub runs AND expanded
  // Always use root DAG's name and ID for the API call since all sub-runs are stored under the root
  // For multi-level nested DAGs, pass the current DAG's run ID as parentSubDAGRunId
  const shouldFetch = allSubRuns.length > 1 && isExpanded;
  const isNestedSubDAG = dagRunId !== rootDagRunId;
  const { data: subRunsData, mutate: refetchSubRuns } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
    whenEnabled(shouldFetch, {
      params: {
        path: {
          name: rootDagName,
          dagRunId: rootDagRunId,
        },
        query: {
          remoteNode,
          // For multi-level nested DAGs, pass the parent sub DAG run ID
          parentSubDAGRunId: isNestedSubDAG ? dagRunId : undefined,
        },
      },
    }),
    {
      refreshInterval: shouldFetch ? 3000 : 0,
    }
  );

  // When the list of sub run ids changes (new executions), immediately refresh timing info
  useEffect(() => {
    if (!shouldFetch) {
      return;
    }
    refetchSubRuns();
  }, [shouldFetch, dagRunIdsKey, refetchSubRuns]);

  // Create a map of dagRunIds that belong to THIS node
  const nodeSubRunIds = new Set(allSubRuns.map((sr) => sr.dagRunId));

  // Map each sub run to include its original index
  const subRunsWithIndex: IndexedSubRun[] = allSubRuns.map((subRun, index) => ({
    ...subRun,
    originalIndex: index,
  }));

  // If we have API data with timing, filter to only THIS node's sub runs, merge and sort
  const subRunsFromApi: IndexedSubRunDetail[] = subRunsData?.subRuns
    ? subRunsData.subRuns
        // FILTER: Only include sub runs that belong to THIS node
        .filter((apiSubRun: SubDAGRunDetail) =>
          nodeSubRunIds.has(apiSubRun.dagRunId)
        )
        .map((apiSubRun): IndexedSubRunDetail => {
          const matchingSubRun = subRunsWithIndex.find(
            (sr) => sr.dagRunId === apiSubRun.dagRunId
          );
          return {
            ...apiSubRun,
            originalIndex: matchingSubRun?.originalIndex ?? 0,
          };
        })
        .sort((a, b) => {
          const timeA = new Date(a.startedAt).getTime();
          const timeB = new Date(b.startedAt).getTime();
          return timeB - timeA; // Descending order (newest first)
        })
    : [];

  const subRunsWithTiming: SubRunListItem[] =
    subRunsFromApi.length > 0 ? subRunsFromApi : subRunsWithIndex;

  // Create a map of dagRunId to statusLabel for filtering
  const statusLabelMap = useMemo(() => {
    const map = new Map<string, StatusLabel>();
    if (subRunsData?.subRuns) {
      for (const detail of subRunsData.subRuns) {
        if (nodeSubRunIds.has(detail.dagRunId)) {
          map.set(detail.dagRunId, detail.statusLabel);
        }
      }
    }
    return map;
  }, [subRunsData, nodeSubRunIds]);

  // Count sub runs by statusLabel - dynamically build from actual data
  const statusCounts = useMemo(() => {
    const counts = new Map<StatusFilterValue, number>();
    counts.set('all', subRunsWithTiming.length);

    for (const subRun of subRunsWithTiming) {
      const statusLabel =
        'statusLabel' in subRun
          ? subRun.statusLabel
          : statusLabelMap.get(subRun.dagRunId);
      if (!statusLabel) continue;

      counts.set(statusLabel, (counts.get(statusLabel) || 0) + 1);
    }

    return counts;
  }, [subRunsWithTiming, statusLabelMap]);

  // Filter sub runs based on status filter
  const filteredSubRuns = useMemo(() => {
    if (statusFilter === 'all') {
      return subRunsWithTiming;
    }

    return subRunsWithTiming.filter((subRun) => {
      const statusLabel =
        'statusLabel' in subRun
          ? subRun.statusLabel
          : statusLabelMap.get(subRun.dagRunId);
      return statusLabel === statusFilter;
    });
  }, [subRunsWithTiming, statusFilter, statusLabelMap]);

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

  if (allSubRuns.length === 1) {
    // Single sub DAG run - use per-run dagName if available, else fall back to prop
    const displayName = allSubRuns[0]?.dagName || subDagName;
    return (
      <>
        <div
          className="text-xs text-primary font-medium cursor-pointer hover:underline"
          onClick={(e) => {
            e.stopPropagation();
            onNavigate(0, e);
          }}
          title="Click to view sub DAG run (Cmd/Ctrl+Click to open in new tab)"
        >
          View Sub DAG Run: {displayName}
        </div>
        {allSubRuns[0]?.params && (
          <div className="text-xs text-muted-foreground mt-1">
            Parameters:{' '}
            <span className="font-mono">{allSubRuns[0].params}</span>
          </div>
        )}
      </>
    );
  }

  // Multiple sub DAG runs
  return (
    <div className="text-xs text-muted-foreground mt-1">
      <div className="flex items-center gap-2">
        <button
          onClick={(e) => {
            e.stopPropagation();
            onToggleExpand();
          }}
          className="flex items-center gap-1 text-primary font-medium hover:underline"
        >
          {isExpanded ? (
            <ChevronDown className="h-3 w-3" />
          ) : (
            <ChevronRight className="h-3 w-3" />
          )}
          Multiple executions:{' '}
          {filteredSubRuns.length === allSubRuns.length
            ? `${allSubRuns.length} sub DAG runs`
            : `${filteredSubRuns.length} of ${allSubRuns.length} sub DAG runs`}
        </button>
      </div>

      {/* Status filter buttons - only show when expanded and there's more than one status */}
      {isExpanded && availableFilters.length > 2 && (
        <div className="flex items-center gap-1 mt-2 ml-4 flex-wrap">
          {availableFilters.map((filter) => {
            const isActive = statusFilter === filter.value;

            return (
              <button
                key={filter.value}
                onClick={(e) => {
                  e.stopPropagation();
                  setStatusFilter(filter.value);
                }}
                className={`
                  px-1.5 py-0.5 text-xs rounded transition-colors
                  ${
                    isActive
                      ? 'bg-primary/15 text-primary'
                      : 'bg-muted text-muted-foreground hover:bg-accent'
                  }
                `}
              >
                {filter.label}
                <span
                  className={`ml-1 ${isActive ? 'text-primary' : 'text-muted-foreground'}`}
                >
                  {filter.count}
                </span>
              </button>
            );
          })}
        </div>
      )}

      {isExpanded && (
        <div className="mt-2 ml-4 space-y-1 border-l border-border pl-3">
          {filteredSubRuns.length === 0 ? (
            <div className="py-2 text-muted-foreground italic">
              No sub DAG runs match the selected filter
            </div>
          ) : (
            filteredSubRuns.map((subRun) => {
              const startedAt = 'startedAt' in subRun ? subRun.startedAt : null;
              const status = 'status' in subRun ? subRun.status : null;
              const statusLabel =
                'statusLabel' in subRun ? subRun.statusLabel : undefined;
              // Use original index for display number
              const displayNumber = subRun.originalIndex + 1;
              // Use per-run dagName if available, else fall back to prop
              const displayName = subRun.dagName || subDagName;
              return (
                <div key={subRun.dagRunId} className="py-1">
                  <div className="flex items-center gap-2">
                    <div
                      className="text-xs text-primary cursor-pointer hover:underline"
                      onClick={(e) => {
                        e.stopPropagation();
                        onNavigate(subRun.originalIndex, e);
                      }}
                      title="Click to view sub DAG run (Cmd/Ctrl+Click to open in new tab)"
                    >
                      #{String(displayNumber).padStart(2, '0')}: {displayName}
                    </div>
                    {startedAt && (
                      <div className="text-xs text-muted-foreground">
                        {dayjs(startedAt).format('MMM D, HH:mm:ss')}
                      </div>
                    )}
                    {status && (
                      <StatusDot status={status} statusLabel={statusLabel} />
                    )}
                  </div>
                  {subRun.params && (
                    <div className="text-xs text-muted-foreground ml-0 mt-1 font-mono">
                      {subRun.params}
                    </div>
                  )}
                </div>
              );
            })
          )}
        </div>
      )}
    </div>
  );
}
