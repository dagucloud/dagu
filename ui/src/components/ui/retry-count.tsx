// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RotateCcw } from 'lucide-react';
import type { ReactElement } from 'react';
import { Badge } from '@/components/ui/badge';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * How many times a step was retried: a small warning badge with the count.
 * Every surface that shows a retry count renders this one badge.
 */
export function RetryCount({ count }: { count: number }): ReactElement {
  const { ts } = useI18n();
  const label = ts('Retried {count}×', { count });
  return (
    <Badge
      variant="warning"
      size="sm"
      className="tabular-nums"
      role="img"
      title={label}
      aria-label={label}
      data-testid="retry-count"
    >
      <RotateCcw className="size-3" aria-hidden="true" />
      {count}
    </Badge>
  );
}

export default RetryCount;
