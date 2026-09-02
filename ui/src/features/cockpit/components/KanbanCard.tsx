import React, { useMemo } from 'react';
import { motion } from 'framer-motion';
import { Archive } from 'lucide-react';
import { components, Status } from '@/api/v1/schema';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import AutoRetryBadge from '@/features/dag-runs/components/common/AutoRetryBadge';
import dayjs from '@/lib/dayjs';
import StatusChip from '@/components/ui/status-chip';
import Ticker from '@/components/ui/ticker';
import { I18nText } from '@/i18n/I18nText';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

interface Props {
  run: DAGRunSummary;
  onClick: () => void;
  onArtifactsClick?: () => void;
}

function formatElapsed(run: DAGRunSummary): string {
  const start = run.startedAt ? new Date(run.startedAt).getTime() : 0;
  if (!start) return '';
  const end =
    run.status === Status.Running
      ? Date.now()
      : run.finishedAt
        ? new Date(run.finishedAt).getTime()
        : Date.now();
  const seconds = Math.floor((end - start) / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const secs = seconds % 60;
  if (minutes < 60) return `${minutes}m ${secs}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

function truncateParams(params: string | undefined, maxLen = 60): string {
  if (!params) return '';
  const clean =
    params.length > maxLen ? params.slice(0, maxLen) + '...' : params;
  return clean;
}

function formatScheduleTime(scheduleTime: string | undefined): string {
  if (!scheduleTime) return '';
  const parsed = dayjs(scheduleTime);
  if (!parsed.isValid()) return '';
  return parsed.format('MMM D, HH:mm:ss');
}

export function KanbanCard({
  run,
  onClick,
  onArtifactsClick,
}: Props): React.ReactElement {
  const params = useMemo(() => truncateParams(run.params), [run.params]);
  const scheduleTime = useMemo(
    () => formatScheduleTime(run.scheduleTime),
    [run.scheduleTime]
  );
  const showArtifacts = run.artifactsAvailable && !!onArtifactsClick;

  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget) {
      return;
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onClick();
    }
  };

  return (
    <motion.div
      layoutId={run.dagRunId}
      layout
      initial={{ opacity: 0, scale: 0.95 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.95 }}
      transition={{ duration: 0.2, ease: 'easeOut' }}
    >
      <div
        className={`relative rounded-md border border-border bg-card transition-colors hover:bg-accent/50 focus-within:ring-2 focus-within:ring-ring ${showArtifacts ? 'min-h-20' : ''}`}
      >
        <div
          role="button"
          tabIndex={0}
          onClick={onClick}
          onKeyDown={handleKeyDown}
          className="w-full cursor-pointer rounded-md p-2 text-left focus:outline-none"
        >
          <div className="mb-1 flex items-start justify-between gap-2">
            <span className="min-w-0 flex-1 truncate text-xs font-medium leading-tight">
              {run.name}
            </span>
            <div className="flex shrink-0 flex-col items-end gap-1">
              <StatusChip status={run.status} size="xs">
                {run.statusLabel}
              </StatusChip>
              <AutoRetryBadge
                status={run.status}
                count={run.autoRetryCount}
                limit={run.autoRetryLimit}
                className="text-[11px]"
              />
            </div>
          </div>
          {run.startedAt &&
            (run.status === Status.Running ? (
              <Ticker intervalMs={1000}>
                {() => (
                  <div
                    className={`text-[11px] text-muted-foreground ${showArtifacts ? 'pr-10' : ''}`}
                  >
                    {formatElapsed(run)}
                  </div>
                )}
              </Ticker>
            ) : (
              <div
                className={`text-[11px] text-muted-foreground ${showArtifacts ? 'pr-10' : ''}`}
              >
                {formatElapsed(run)}
              </div>
            ))}
          {scheduleTime && (
            <div
              className={`text-[11px] text-muted-foreground ${showArtifacts ? 'pr-10' : ''}`}
            >
              <I18nText text={"Scheduled"} /> {scheduleTime}
            </div>
          )}
          {params && (
            <div
              className={`mt-0.5 truncate font-mono text-[11px] text-muted-foreground ${showArtifacts ? 'pr-10' : ''}`}
            >
              {params}
            </div>
          )}
        </div>
        {showArtifacts && (
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-label={`View artifacts for ${run.name}`}
                className="absolute bottom-2 right-2 inline-flex size-7 items-center justify-center rounded-md border border-border bg-background/80 text-muted-foreground shadow-sm transition-colors hover:border-primary/40 hover:bg-primary/10 hover:text-primary focus:outline-none focus:ring-2 focus:ring-primary/40"
                onClick={() => onArtifactsClick?.()}
              >
                <Archive className="h-4 w-4" />
              </button>
            </TooltipTrigger>
            <TooltipContent><I18nText text={"View artifacts"} /></TooltipContent>
          </Tooltip>
        )}
      </div>
    </motion.div>
  );
}
