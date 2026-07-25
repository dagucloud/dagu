import { TriggerType } from '@/api/v1/schema';
import type { ReactElement } from 'react';

export const triggerTypeLabels: Record<TriggerType, string> = {
  scheduler: 'Scheduled',
  manual: 'Manual',
  webhook: 'Webhook',
  subdag: 'Sub-DAG',
  retry: 'Retry',
  catchup: 'Catch-up',
  unknown: 'Unknown',
};

type Props = {
  type?: TriggerType;
  actor?: string;
};

export function TriggerTypeIndicator({
  type,
  actor,
}: Props): ReactElement | null {
  if (!type) {
    return null;
  }

  return (
    <span className="whitespace-normal break-words font-medium text-foreground/90 text-xs">
      {triggerTypeLabels[type] ?? type}
      {actor && (
        <span className="text-muted-foreground font-mono"> ({actor})</span>
      )}
    </span>
  );
}
