// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronRight, Search, TriangleAlert } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { KeyboardEvent, ReactElement, ReactNode, Ref } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ValidationIssue } from '../contracts';
import { issueSummary, type RowChip } from '../tools/dockModel';
import { kindClasses, kindIcon, type ToolKind } from '../tools/kinds';

/** The square tile of a row and of the detail: the icon of a kind in its colour. */
export function KindTile({
  kind,
  icon,
  className,
}: {
  kind: ToolKind;
  icon?: LucideIcon | undefined;
  className?: string;
}): ReactElement {
  const Icon = icon ?? kindIcon(kind);
  const classes = kindClasses(kind);
  return (
    <span
      data-kind-tile={kind}
      className={cn(
        'flex size-8 shrink-0 items-center justify-center rounded-md',
        classes.bg,
        classes.text,
        className
      )}
    >
      <Icon data-kind-icon={kind} className="size-4" aria-hidden="true" />
    </span>
  );
}

/** A small fact on a row or in the detail. */
export function Chip({
  chip,
  className,
}: {
  chip: Pick<RowChip, 'text' | 'tone'>;
  className?: string;
}): ReactElement {
  return (
    <span
      data-testid="ve-dock-chip"
      data-tone={chip.tone}
      className={cn(
        'max-w-full truncate rounded px-1.5 text-[11px] leading-4',
        chip.tone === 'warning'
          ? 'bg-warning/10 text-warning'
          : 'bg-muted text-muted-foreground',
        className
      )}
    >
      {chip.text}
    </span>
  );
}

/** The count of issues of a row, in the colour of its strongest severity. */
export function IssueBadge({
  issues,
}: {
  issues: readonly ValidationIssue[];
}): ReactElement | null {
  const { ts } = useI18n();
  const { count, severity } = issueSummary(issues);
  if (count === 0) return null;
  return (
    <span
      data-testid="ve-dock-issues"
      data-severity={severity}
      aria-label={ts(count === 1 ? '{count} issue' : '{count} issues', {
        count,
      })}
      className={cn(
        'inline-flex items-center gap-0.5 rounded px-1 text-[10px] font-medium leading-4',
        severity === 'error'
          ? 'bg-destructive/10 text-destructive'
          : 'bg-warning/10 text-warning'
      )}
    >
      <TriangleAlert className="size-2.5" aria-hidden="true" />
      {count}
    </span>
  );
}

/** The search box of a dock: it is the combobox of the list under it. */
export function DockSearch({
  value,
  label,
  placeholder,
  listId,
  activeId,
  inputRef,
  keyShortcuts,
  onChange,
  onKeyDown,
}: {
  value: string;
  label: string;
  placeholder: string;
  listId: string;
  activeId: string | undefined;
  inputRef: Ref<HTMLInputElement>;
  keyShortcuts?: string;
  onChange(value: string): void;
  onKeyDown(event: KeyboardEvent<HTMLInputElement>): void;
}): ReactElement {
  return (
    <div className="relative">
      <Search
        className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground"
        aria-hidden="true"
      />
      <input
        ref={inputRef}
        autoFocus
        type="text"
        role="combobox"
        aria-expanded
        aria-controls={listId}
        aria-activedescendant={activeId}
        aria-keyshortcuts={keyShortcuts}
        aria-label={label}
        placeholder={placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={onKeyDown}
        className="h-9 w-full rounded-md border bg-background pl-8 pr-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
      />
    </div>
  );
}

/**
 * The heading of a section of the list: its kind icon, its title and the count
 * of its rows, with a chevron that shows whether it is open. The tree item
 * that holds it gives the name to a screen reader; a click opens or closes it.
 */
export function SectionHead({
  title,
  count,
  open,
  active,
  icon: Icon,
  iconClass,
  onToggle,
}: {
  title: string;
  count: number;
  open: boolean;
  active: boolean;
  icon?: LucideIcon | undefined;
  iconClass?: string | undefined;
  onToggle(): void;
}): ReactElement {
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <div
      aria-hidden="true"
      data-testid="ve-dock-section"
      data-open={open}
      data-active={active || undefined}
      onClick={onToggle}
      className={cn(
        'sticky top-0 z-10 flex cursor-default items-center gap-1.5 bg-card px-3 pb-1 pt-2.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground',
        active && 'text-foreground'
      )}
    >
      <Chevron className="size-3.5 shrink-0" aria-hidden="true" />
      {Icon && (
        <Icon className={cn('size-3 shrink-0', iconClass)} aria-hidden="true" />
      )}
      <span className="min-w-0 flex-1 truncate">{title}</span>
      <span className="font-normal">{count}</span>
    </div>
  );
}

/** A divider between the tools of the step and the catalogue. */
export function GroupLabel({
  title,
  count,
  hint,
}: {
  title: string;
  count?: number;
  hint?: string;
}): ReactElement {
  return (
    <div
      role="presentation"
      className="mt-1 flex items-baseline gap-2 border-y border-border bg-muted/40 px-4 py-1.5"
    >
      <span className="text-xs font-semibold text-foreground">{title}</span>
      {count !== undefined && (
        <span className="text-xs text-muted-foreground">{count}</span>
      )}
      {hint && (
        <span className="ml-auto min-w-0 truncate text-[11px] text-muted-foreground">
          {hint}
        </span>
      )}
    </div>
  );
}

/** The heading of a block of the detail area. */
export function DetailHeading({
  children,
  aside,
}: {
  children: ReactNode;
  aside?: ReactNode;
}): ReactElement {
  return (
    <div className="mb-1 flex items-center justify-between gap-2">
      <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {children}
      </h3>
      {aside}
    </div>
  );
}

/** A note with a tone, for the empty states of the list and of the detail. */
export function Note({
  children,
  className,
  'data-testid': testId,
}: {
  children: ReactNode;
  className?: string;
  'data-testid'?: string;
}): ReactElement {
  return (
    <p
      data-testid={testId}
      className={cn(
        'rounded-md border border-dashed border-border px-3 py-2 text-xs text-muted-foreground',
        className
      )}
    >
      {children}
    </p>
  );
}
