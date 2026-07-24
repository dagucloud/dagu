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
    <span className="font-medium text-foreground/90 text-xs">
      {triggerTypeLabels[type] ?? type}
      {actor && ` — ${actor}`}
    </span>
  );
}
