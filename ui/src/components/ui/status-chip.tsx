import React from 'react';

import { Status } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { MatrixText } from '@/components/ui/matrix-text';
import { useI18n } from '@/i18n/I18nProvider';
import { getStatusClass, statusLabelText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';

type StatusChipProps = {
  status?: Status;
  /**
   * The run runs for ever and has no failed step (runDisplayStatus): the chip
   * reads "Healthy" in the success token. `status` stays the API status.
   */
  healthy?: boolean;
  /** An API status label such as `not_started` is shown as readable text. */
  children: React.ReactNode;
  size?: 'xs' | 'sm' | 'md' | 'lg';
};

function StatusChip({
  status,
  healthy = false,
  children,
  size = 'md',
}: StatusChipProps): React.JSX.Element {
  const { ts } = useI18n();
  const statusClass = healthy ? 'status-success' : getStatusClass(status);
  const isRunning = status === Status.Running && !healthy;
  const label = healthy
    ? ts('Healthy')
    : typeof children === 'string'
      ? ts(statusLabelText(children))
      : children;
  const sizeClasses = {
    xs: 'h-5 px-1.5 text-[11px]',
    sm: 'h-5 px-2 text-[11px]',
    md: 'h-6 px-2.5 text-xs',
    lg: 'h-7 px-3 text-sm',
  };

  return (
    <Badge
      variant="outline"
      data-status={status}
      className={cn(
        'rounded-full normal-case tracking-normal',
        statusClass,
        sizeClasses[size]
      )}
    >
      <span className="font-medium break-keep text-nowrap whitespace-nowrap">
        {isRunning && typeof label === 'string' ? (
          <MatrixText text={label} />
        ) : (
          label
        )}
      </span>
    </Badge>
  );
}

export type { StatusChipProps };
export { StatusChip };
export default StatusChip;
