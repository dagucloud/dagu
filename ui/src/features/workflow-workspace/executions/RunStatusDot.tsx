// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { runStatusText } from '@/lib/status-utils';
import { statusClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';

/** The status of a run as a dot in its status token, named for screen readers. */
export function RunStatusDot({ status }: { status: Status }) {
  const { ts } = useI18n();
  const statusText = ts(runStatusText(status));
  return (
    <span
      role="img"
      aria-label={statusText}
      title={statusText}
      data-testid="status-dot"
      data-status={status}
      className={cn(
        'inline-block h-2 w-2 shrink-0 rounded-full',
        statusClasses(status).dot,
        status === Status.Waiting && 'motion-safe:animate-pulse'
      )}
    />
  );
}
