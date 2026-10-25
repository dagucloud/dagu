import { TriggerType } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import type { ReactElement } from 'react';

export const triggerTypeLabels: Record<TriggerType, string> = {
  scheduler: 'Scheduled',
  manual: 'Manual',
  webhook: 'Webhook',
  subdag: 'Sub-DAG',
  retry: 'Retry',
  catchup: 'Catch-up',
  restart: 'Restart',
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
  const { ts } = useI18n();
  if (!type) {
    return null;
  }

  return (
    <span className="whitespace-normal break-words font-medium text-foreground/90 text-xs">
      {ts(triggerTypeLabels[type] ?? type)}
      {actor && (
        <span className="text-muted-foreground font-mono"> ({actor})</span>
      )}
    </span>
  );
}
