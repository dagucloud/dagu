import { Status } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { getStatusColors, statusLabelText } from '@/lib/status-utils';
import React from 'react';

type Props = {
  status: Status;
  statusLabel?: string;
};

export function StatusDot({ status, statusLabel }: Props): React.JSX.Element {
  const { ts } = useI18n();
  const { bgClass, animation } = getStatusColors(status);

  return (
    <span
      className={`inline-block w-2 h-2 rounded-full ${bgClass} ${animation}`}
      title={statusLabel ? ts(statusLabelText(statusLabel)) : ''}
    />
  );
}
