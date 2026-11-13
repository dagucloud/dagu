// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { LucideIcon } from 'lucide-react';
import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { kindBadge, kindClasses, kindIcon, type ToolKind } from './kinds';

/** The icon of a kind, or `icon` when a tool has its own, in the colour of the kind. */
export function KindIcon({
  kind,
  icon,
  className,
}: {
  kind: ToolKind;
  icon?: LucideIcon;
  className?: string;
}): ReactElement {
  const Icon = icon ?? kindIcon(kind);
  return (
    <Icon
      data-kind-icon={kind}
      className={cn('shrink-0', kindClasses(kind).text, className)}
      aria-hidden="true"
    />
  );
}

/** The short text of a kind ("DAG", "Action", "MCP", "Built-in") on a tinted chip. */
export function KindBadge({
  kind,
  className,
}: {
  kind: ToolKind;
  className?: string;
}): ReactElement {
  const { ts } = useI18n();
  const classes = kindClasses(kind);
  return (
    <span
      data-testid="ve-tool-kind"
      data-kind={kind}
      className={cn(
        'shrink-0 rounded px-1 text-[10px] font-medium leading-4',
        classes.text,
        classes.bg,
        className
      )}
    >
      {kindBadge(ts, kind)}
    </span>
  );
}
