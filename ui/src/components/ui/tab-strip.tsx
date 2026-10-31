// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { LucideIcon } from 'lucide-react';
import {
  useCallback,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
} from 'react';

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { cn, FOCUS_RING } from '@/lib/utils';

export interface TabStripTab {
  id: string;
  /** The text of the tab. The caller translates it. */
  label: string;
  /** Needed for the icon-only form: a tab without an icon keeps its label. */
  icon?: LucideIcon;
  /** A count shown after the label. 0 shows nothing. */
  badge?: number;
  /**
   * What the count means, for example "2 waiting". The accessible name of the
   * tab then ends with it, as the badge is hidden from assistive technology.
   */
  badgeLabel?: string;
  disabled?: boolean;
  /** The id of the panel that this tab controls (aria-controls). */
  controls?: string;
}

export interface TabStripProps {
  /** The accessible name of the tablist. The caller translates it. */
  label: string;
  tabs: ReadonlyArray<TabStripTab>;
  value: string;
  onChange(id: string): void;
  /** Icon-only tabs, whatever the width. */
  compact?: boolean;
  /** Prefix of the DOM ids of the tabs. Defaults to a generated one. */
  idPrefix?: string;
  className?: string;
}

/** The DOM id of a tab, for aria-labelledby of its panel. */
export function tabStripTabId(prefix: string, id: string): string {
  return `${prefix}-tab-${id}`;
}

const KEY_STEP: Record<string, 1 | -1> = { ArrowRight: 1, ArrowLeft: -1 };

/**
 * The one tab row of the workspace: a 32 px row of 24 px tabs. The names
 * collapse into icons, with a tooltip and an aria-label, when `compact` is
 * set or when the row is too narrow for them; if the icons do not fit
 * either, the row scrolls horizontally. There is no overflow menu. Only the
 * selected tab is a tab stop; the arrow keys, Home and End select and focus
 * a tab.
 */
export function TabStrip({
  label,
  tabs,
  value,
  onChange,
  compact = false,
  idPrefix,
  className,
}: TabStripProps): ReactElement {
  const generated = useId();
  const prefix = idPrefix ?? generated;
  const listRef = useRef<HTMLDivElement>(null);
  const [squeezed, setSqueezed] = useState(false);
  // The width that the row needs with the names; known once measured.
  const fullWidth = useRef(0);
  const iconOnly = compact || squeezed;

  const measure = useCallback(() => {
    const list = listRef.current;
    if (!list || compact) return;
    if (!squeezed) {
      if (list.scrollWidth > list.clientWidth) {
        fullWidth.current = list.scrollWidth;
        setSqueezed(true);
      }
    } else if (fullWidth.current > 0 && list.clientWidth >= fullWidth.current) {
      setSqueezed(false);
    }
  }, [compact, squeezed]);

  // The labels or the tabs may change: forget the old width and measure again.
  const signature = tabs
    .map((tab) => `${tab.id}:${tab.label}:${tab.badge ?? 0}`)
    .join('|');
  useLayoutEffect(() => {
    fullWidth.current = 0;
    setSqueezed(false);
  }, [signature]);

  useLayoutEffect(() => {
    measure();
  });

  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => measure());
    observer.observe(list);
    return () => observer.disconnect();
  }, [measure]);

  const enabled = tabs.filter((tab) => !tab.disabled);
  const selected = enabled.find((tab) => tab.id === value);
  const stopId = (selected ?? enabled[0])?.id;

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>, id: string) => {
    if (enabled.length === 0) return;
    const index = enabled.findIndex((tab) => tab.id === id);
    let target: TabStripTab | undefined;
    if (event.key === 'Home') target = enabled[0];
    else if (event.key === 'End') target = enabled[enabled.length - 1];
    else {
      const step = KEY_STEP[event.key];
      if (step !== undefined) {
        target = enabled[(index + step + enabled.length) % enabled.length];
      }
    }
    if (!target) return;
    event.preventDefault();
    onChange(target.id);
    const next = target.id;
    Array.from(
      listRef.current?.querySelectorAll<HTMLElement>('[role="tab"]') ?? []
    )
      .find((el) => el.dataset.tabId === next)
      ?.focus();
  };

  return (
    <div
      ref={listRef}
      role="tablist"
      aria-label={label}
      className={cn(
        'flex h-8 min-w-0 items-center gap-0.5 overflow-x-auto overflow-y-hidden',
        className
      )}
    >
      {tabs.map((tab) => {
        const Icon = tab.icon;
        const active = tab.id === value;
        const hideLabel = iconOnly && !!Icon;
        const button = (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={tabStripTabId(prefix, tab.id)}
            data-tab-id={tab.id}
            aria-selected={active}
            aria-controls={tab.controls}
            aria-label={
              tab.badge && tab.badgeLabel
                ? `${tab.label}, ${tab.badgeLabel}`
                : hideLabel
                  ? tab.badge
                    ? `${tab.label} (${tab.badge})`
                    : tab.label
                  : undefined
            }
            disabled={tab.disabled}
            tabIndex={tab.id === stopId ? 0 : -1}
            onClick={() => onChange(tab.id)}
            onKeyDown={(event) => onKeyDown(event, tab.id)}
            className={cn(
              'inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-md px-2 text-xs transition-colors disabled:pointer-events-none disabled:opacity-50',
              FOCUS_RING,
              active
                ? 'bg-muted font-medium text-foreground'
                : 'text-muted-foreground hover:bg-muted hover:text-foreground'
            )}
          >
            {Icon && <Icon className="h-3.5 w-3.5" aria-hidden="true" />}
            {!hideLabel && <span>{tab.label}</span>}
            {!!tab.badge && (
              <span
                aria-hidden="true"
                className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-warning px-1 text-[10px] font-semibold leading-none text-warning-foreground"
              >
                {tab.badge}
              </span>
            )}
          </button>
        );
        if (!hideLabel) return button;
        return (
          <Tooltip key={tab.id}>
            <TooltipTrigger asChild>{button}</TooltipTrigger>
            <TooltipContent>{tab.label}</TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
}
