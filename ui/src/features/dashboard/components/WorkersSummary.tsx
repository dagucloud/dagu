import React from 'react';
import { Activity, ChevronDown, ChevronRight, Clock } from 'lucide-react';
import type { components } from '../../../api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import { cn } from '../../../lib/utils';
import WorkerHealth from '../../workers/components/WorkerHealth';
import { I18nText } from '@/i18n/I18nText';

type Worker = components['schemas']['Worker'];
type RunningTask = components['schemas']['RunningTask'];

interface WorkersSummaryProps {
  workers: Worker[];
  isLoading: boolean;
  errors?: string[];
  onTaskClick?: (task: RunningTask) => void;
}

function WorkersSummary({
  workers,
  isLoading,
  errors,
  onTaskClick,
}: WorkersSummaryProps) {
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

  // Calculate metrics
  const metrics = React.useMemo(() => {
    const totalWorkers = workers.length;
    const totalPollers = workers.reduce(
      (sum, w) => sum + (w.totalPollers || 0),
      0
    );
    const busyPollers = workers.reduce(
      (sum, w) => sum + (w.busyPollers || 0),
      0
    );
    const totalTasks = workers.reduce(
      (sum, w) => sum + (w.runningTasks?.length || 0),
      0
    );
    const utilization =
      totalPollers > 0 ? Math.round((busyPollers / totalPollers) * 100) : 0;

    const healthyWorkers = workers.filter((worker) => {
      if (!worker.lastHeartbeatAt) return false;
      const lastHeartbeat = new Date(worker.lastHeartbeatAt).getTime();
      const now = new Date().getTime();
      return now - lastHeartbeat < 10000;
    }).length;

    return {
      totalWorkers,
      healthyWorkers,
      totalPollers,
      busyPollers,
      totalTasks,
      utilization,
    };
  }, [workers]);

  return (
    <div className="flex flex-col h-full">
      {/* Header with metrics */}
      <div className="flex items-center justify-between px-3 py-2 border-b flex-shrink-0">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium"><I18nText text={"Workers"} /></span>
        </div>
        <div className="flex items-center gap-5 text-xs text-muted-foreground">
          <div className="flex items-baseline gap-1">
            <span className="text-sm font-light tabular-nums text-foreground">
              {metrics.totalWorkers}
            </span>
            <span><I18nText text={"workers"} /></span>
            <span className="text-muted-foreground/60">
              ({metrics.healthyWorkers} <I18nText text={"up)"} />
            </span>
          </div>
          <div className="flex items-baseline gap-1">
            <span className="text-sm font-light tabular-nums text-foreground">
              {metrics.busyPollers}/{metrics.totalPollers}
            </span>
            <span><I18nText text={"pollers"} /></span>
          </div>
          <div className="flex items-baseline gap-1">
            <span className="text-sm font-light tabular-nums text-foreground">
              {metrics.totalTasks}
            </span>
            <span><I18nText text={"tasks"} /></span>
          </div>
          <div className="flex items-baseline gap-1">
            <span className="text-sm font-light tabular-nums text-foreground">
              {metrics.utilization}%
            </span>
            <span><I18nText text={"util"} /></span>
          </div>
        </div>
      </div>

      {/* Worker list */}
      <div className="flex-1 min-h-0 overflow-auto">
        {isLoading && workers.length === 0 ? (
          <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
            <I18nText text={"Loading workers..."} />
          </div>
        ) : errors && errors.length > 0 ? (
          <div className="p-2 text-sm text-warning">
            {errors.map((err, idx) => (
              <div key={idx}>{err}</div>
            ))}
          </div>
        ) : workers.length === 0 ? (
          <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
            <I18nText text={"No workers connected"} />
          </div>
        ) : (
          <div className="divide-y">
            {workers.map((worker) => {
              const isExpanded = expandedWorkers.has(worker.id);
              const hasRunningTasks =
                worker.runningTasks && worker.runningTasks.length > 0;
              const utilization =
                worker.totalPollers > 0
                  ? Math.round((worker.busyPollers / worker.totalPollers) * 100)
                  : 0;

              return (
                <div key={worker.id}>
                  <div
                    className={cn(
                      'px-3 py-2 flex items-center gap-2 cursor-pointer hover:bg-muted/50 transition-colors text-sm',
                      isExpanded && 'bg-muted/30'
                    )}
                    onClick={() => toggleExpanded(worker.id)}
                  >
                    <div className="w-4">
                      {hasRunningTasks ? (
                        isExpanded ? (
                          <ChevronDown className="h-4 w-4 text-muted-foreground" />
                        ) : (
                          <ChevronRight className="h-4 w-4 text-muted-foreground" />
                        )
                      ) : null}
                    </div>

                    <WorkerHealth healthStatus={worker.healthStatus} />

                    <div className="flex-1 min-w-0 font-mono text-sm truncate">
                      {worker.id}
                    </div>

                    <div className="flex flex-wrap gap-1">
                      {worker.labels &&
                        Object.entries(worker.labels)
                          .sort(
                            ([keyA], [keyB]) =>
                              Number(keyA === 'os' || keyA === 'arch') -
                              Number(keyB === 'os' || keyB === 'arch')
                          )
                          .slice(0, 2)
                          .map(([key, value]) => (
                            <span
                              key={key}
                              className="px-1.5 py-0.5 rounded text-xs font-medium bg-accent"
                            >
                              {key}={value}
                            </span>
                          ))}
                      {worker.labels &&
                        Object.keys(worker.labels).length > 2 && (
                          <span className="text-xs text-muted-foreground">
                            +{Object.keys(worker.labels).length - 2}
                          </span>
                        )}
                    </div>

                    <div className="w-20 flex items-center gap-1">
                      <div className="flex-1 h-1 bg-muted rounded-full overflow-hidden">
                        <div
                          className="h-full transition-all bg-foreground/40"
                          style={{ width: `${utilization}%` }}
                        />
                      </div>
                      <span className="text-xs text-muted-foreground w-8 text-right tabular-nums">
                        {utilization}%
                      </span>
                    </div>

                    <div className="w-14 text-right text-xs text-muted-foreground">
                      {worker.runningTasks?.length || 0}/{worker.totalPollers}
                    </div>

                    <RelativeTime
                      className="text-xs text-muted-foreground w-10 text-right"
                      timestamp={worker.lastHeartbeatAt}
                      fallback="Never"
                      compact
                    />
                  </div>

                  {isExpanded && hasRunningTasks && (
                    <div className="bg-muted/20 border-t px-8 py-2 space-y-1">
                      {worker.runningTasks.map((task: RunningTask) => (
                        <TaskRow
                          key={task.dagRunId}
                          task={task}
                          onTaskClick={onTaskClick}
                        />
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
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

  const handleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (e.metaKey || e.ctrlKey) {
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
      onTaskClick(task);
    }
  };

  return (
    <div
      className="flex items-center gap-2 p-1.5 rounded bg-background/50 cursor-pointer hover:bg-background/80 transition-colors text-xs"
      onClick={handleClick}
    >
      <Clock className="h-3 w-3 text-muted-foreground flex-shrink-0" />
      <span className="font-medium truncate">{task.dagName}</span>
      <span className="text-muted-foreground font-mono truncate">
        {task.dagRunId}
      </span>
      <span className="text-muted-foreground ml-auto">{duration}</span>
    </div>
  );
}

export default WorkersSummary;
