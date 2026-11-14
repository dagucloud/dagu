// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { formatCompactCount, formatExactCount } from '@/lib/formatCompactCount';

type Props = {
  /** The count of executions. */
  value: number;
  className?: string;
};

/**
 * A count of executions in its compact form (`1.2k`), with the whole count,
 * in the separators of the language, as its title and accessible name. Every
 * place that prints a count of executions without the repeat badge uses it.
 */
export function CompactCount({ value, className }: Props): ReactElement {
  const { locale } = useI18n();
  const exact = formatExactCount(value, locale);
  return (
    <span
      role="img"
      title={exact}
      aria-label={exact}
      data-testid="compact-count"
      className={className}
    >
      {formatCompactCount(value)}
    </span>
  );
}

export default CompactCount;
