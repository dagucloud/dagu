import React from 'react';
import { ChevronDown, ChevronRight, Clock, AlertCircle } from 'lucide-react';
import type { components } from '../../../api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import { cn } from '../../../lib/utils';
import WorkerHealth from './WorkerHealth';
import { I18nText } from '@/i18n/I18nText';

type Worker = components['schemas']['Worker'];
type RunningTask = components['schemas']['RunningTask'];

interface WorkerListProps {
  workers: Worker[];
  isLoading: boolean;
  errors?: string[];
  onTaskClick?: (task: RunningTask) => void;
}

function WorkerList({
  workers,
  isLoading,
  errors,
  onTaskClick,
}: WorkerListProps) {
  const [expandedWorkers, setExpandedWorkers] = React.useState<Set<string>>(
    new Set()
  );

  const toggleExpanded = (workerId: string) => {
    setExpandedWorkers((prev) => {
      const next = new Set(prev);
      if (next.has(workerId)) {
        next.delete(workerId);
      } else {
        next.add(workerId);
      }
      return next;
    });
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
        <I18nText text={'Loading workers...'} />
      </div>
    );
  }

  if (errors && errors.length > 0) {
    return (
      <div className="p-4 space-y-2">
        <div className="flex items-center gap-2 text-sm text-warning">
          <AlertCircle className="h-4 w-4" />
          <span>
            <I18nText text={'Warnings:'} />
          </span>
        </div>
        {errors.map((error, idx) => (
          <p key={idx} className="text-xs text-muted-foreground pl-6">
            {error}
          </p>
        ))}
      </div>
    );
  }

  if (workers.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
        <I18nText text={'No workers connected'} />
      </div>
    );
  }

  return (
    <div className="divide-y">
      {workers.map((worker) => {
        const isExpanded = expandedWorkers.has(worker.id);
        const hasRunningTasks =
          worker.runningTasks && worker.runningTasks.length > 0;

        return (
          <div key={worker.id} className="hover:bg-muted/50 transition-colors">
            {/* Worker row */}
            <div
              className={cn(
                'px-3 py-2 flex items-center gap-3 cursor-pointer',
                isExpanded && 'bg-muted/30'
              )}
              onClick={() => toggleExpanded(worker.id)}
            >
              {/* Expand icon */}
              <div className="w-4 h-4 flex items-center justify-center">
                {hasRunningTasks ? (
                  isExpanded ? (
                    <ChevronDown className="h-3 w-3 text-muted-foreground" />
                  ) : (
                    <ChevronRight className="h-3 w-3 text-muted-foreground" />
                  )
                ) : (
                  <div className="w-3" />
                )}
              </div>

              {/* Health indicator */}
              <WorkerHealth healthStatus={worker.healthStatus} />

              {/* Worker ID */}
              <div className="flex-1 min-w-0">
                <div className="font-mono text-sm truncate">{worker.id}</div>
              </div>

              {/* Labels */}
              <div className="flex-1 min-w-0">
                <div className="flex flex-wrap gap-1">
                  {worker.labels &&
                    Object.entries(worker.labels).map(([key, value]) => (
                      <span
                        key={key}
                        className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-accent text-foreground/90"
                      >
                        {key}={value}
                      </span>
                    ))}
                </div>
              </div>

              {/* Utilization */}
              <div className="w-32">
                <UtilizationBar
                  busy={worker.busyPollers}
                  total={worker.totalPollers}
                />
              </div>

              {/* Tasks count */}
              <div className="w-20 text-right">
                <span className="text-sm font-medium">
                  {worker.runningTasks?.length || 0}/{worker.totalPollers}
                </span>
                <div className="text-xs text-muted-foreground">
                  <I18nText text={'tasks'} />
                </div>
              </div>

              {/* Last heartbeat */}
              <div className="w-16 text-right">
                <RelativeTime
                  className="text-xs text-muted-foreground"
                  timestamp={worker.lastHeartbeatAt}
                  fallback="Never"
                  compact
                />
              </div>
            </div>

            {/* Expanded tasks */}
            {isExpanded && hasRunningTasks && (
              <div className="bg-muted/20 border-t">
                <div className="px-12 py-2 space-y-1">
                  <div className="text-xs font-medium text-muted-foreground mb-2">
                    <I18nText
                      text="Running Tasks ({count})"
                      values={{ count: worker.runningTasks.length }}
                    />
                  </div>
                  {worker.runningTasks.map((task: RunningTask) => (
                    <TaskRow
                      key={task.dagRunId}
                      task={task}
                      onTaskClick={onTaskClick}
                    />
                  ))}
                </div>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function UtilizationBar({ busy, total }: { busy: number; total: number }) {
  const percentage = total > 0 ? (busy / total) * 100 : 0;

  return (
    <div className="space-y-1">
      <div className="flex justify-between text-xs">
        <span className="text-muted-foreground">
          <I18nText text={'Usage'} />
        </span>
        <span className="font-medium">{Math.round(percentage)}%</span>
      </div>
      <div className="h-1.5 bg-muted rounded-full overflow-hidden">
        <div
          className={cn(
            'h-full transition-all duration-300',
            percentage >= 90
              ? 'bg-error'
              : percentage >= 70
                ? 'bg-warning'
                : percentage >= 50
                  ? 'bg-warning'
                  : percentage > 0
                    ? 'bg-success'
                    : 'bg-muted'
          )}
          style={{ width: `${percentage}%` }}
        />
      </div>
    </div>
  );
}

function TaskRow({
  task,
  onTaskClick,
}: {
  task: RunningTask;
  onTaskClick?: (task: RunningTask) => void;
}) {
  const duration = React.useMemo(() => {
    if (!task.startedAt) return '';
    const start = new Date(task.startedAt).getTime();
    const now = new Date().getTime();
    const seconds = Math.floor((now - start) / 1000);

    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
  }, [task.startedAt]);

  const isNestedTask =
    task.rootDagRunName && task.rootDagRunName !== task.dagName;

  const handleClick = (e: React.MouseEvent) => {
    if (e.metaKey || e.ctrlKey) {
      // Open in new tab
      let url: string;
      if (task.parentDagRunName && task.parentDagRunId) {
        const searchParams = new URLSearchParams();
        searchParams.set('subDAGRunId', task.dagRunId);
        searchParams.set('dagRunId', task.parentDagRunId);
        searchParams.set('dagRunName', task.parentDagRunName);
        url = `/dag-runs/${task.parentDagRunName}/${task.parentDagRunId}?${searchParams.toString()}`;
      } else {
        url = `/dag-runs/${task.dagName}/${task.dagRunId}`;
      }
      window.open(url, '_blank');
    } else if (onTaskClick) {
      // Open modal
      onTaskClick(task);
    }
  };

  return (
    <div
      className="flex items-center gap-3 p-1.5 rounded bg-background/50 cursor-pointer hover:bg-background/80 transition-colors"
      onClick={handleClick}
    >
      <Clock className="h-3 w-3 text-muted-foreground flex-shrink-0" />
      <div className="flex-1 min-w-0">
        <div className="text-xs font-medium truncate">{task.dagName}</div>
        <div className="text-xs text-muted-foreground font-mono truncate">
          {task.dagRunId}
        </div>
        {isNestedTask && (
          <div className="text-xs text-muted-foreground mt-0.5">
            <span className="opacity-60">
              <I18nText text={'root:'} />
            </span>{' '}
            {task.rootDagRunName}
            <span className="opacity-40 ml-1">({task.rootDagRunId})</span>
          </div>
        )}
        {task.parentDagRunName && task.parentDagRunName !== task.dagName && (
          <div className="text-xs text-muted-foreground">
            <span className="opacity-60">
              <I18nText text={'parent:'} />
            </span>{' '}
            {task.parentDagRunName}
          </div>
        )}
      </div>
      <div className="text-xs text-muted-foreground whitespace-nowrap">
        {duration}
      </div>
    </div>
  );
}

export default WorkerList;
