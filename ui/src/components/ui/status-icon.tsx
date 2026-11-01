import {
  AlertTriangle,
  Ban,
  CheckCircle2,
  Circle,
  Clock,
  HeartPulse,
  Loader2,
  type LucideIcon,
  PauseCircle,
  XCircle,
} from 'lucide-react';
import React from 'react';

import { Status } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { runStatusText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';

type StatusIconProps = {
  status?: Status;
  /**
   * The run runs for ever and has no failed step (runDisplayStatus): the
   * icon is a calm heart in the success token, named "Healthy". `status`
   * stays the API status.
   */
  healthy?: boolean;
  /**
   * Gives the icon its own native title (the default). A parent that shows a
   * tooltip of its own turns it off, so that two tooltips do not stack.
   */
  title?: boolean;
  className?: string;
};

const ICONS: Record<Status, { icon: LucideIcon; color: string }> = {
  [Status.NotStarted]: { icon: Circle, color: 'var(--status-neutral)' },
  [Status.Running]: { icon: Loader2, color: 'var(--status-running)' },
  [Status.Failed]: { icon: XCircle, color: 'var(--status-error)' },
  [Status.Aborted]: { icon: Ban, color: 'var(--status-aborted)' },
  [Status.Success]: { icon: CheckCircle2, color: 'var(--status-success)' },
  [Status.Queued]: { icon: Clock, color: 'var(--status-neutral)' },
  [Status.PartialSuccess]: {
    icon: AlertTriangle,
    color: 'var(--status-warning)',
  },
  [Status.Waiting]: { icon: PauseCircle, color: 'var(--status-warning)' },
  [Status.Rejected]: { icon: XCircle, color: 'var(--status-error)' },
};

/**
 * The status of a DAG run as a colored icon alone. The status text is the
 * accessible name and the tooltip.
 */
function StatusIcon({
  status,
  healthy = false,
  title = true,
  className,
}: StatusIconProps): React.JSX.Element | null {
  const { ts } = useI18n();
  if (status === undefined || !ICONS[status]) return null;
  const { icon: Icon, color } = healthy
    ? { icon: HeartPulse, color: 'var(--status-success)' }
    : ICONS[status];
  const label = ts(healthy ? 'Healthy' : runStatusText(status));

  return (
    <span
      role="img"
      aria-label={label}
      title={title ? label : undefined}
      data-status={status}
      className="inline-flex shrink-0 items-center"
      style={{ color }}
    >
      <Icon
        aria-hidden="true"
        className={cn(
          'h-5 w-5',
          healthy
            ? 'motion-safe:animate-pulse'
            : status === Status.Running && 'animate-spin',
          className
        )}
      />
    </span>
  );
}

export type { StatusIconProps };
export { StatusIcon };
export default StatusIcon;
