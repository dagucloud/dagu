// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Activity,
  Archive,
  Bot,
  ClipboardCheck,
  FileCode,
  GanttChart,
  History,
  ListChecks,
  MessageSquare,
  MoreHorizontal,
  Package,
  ScrollText,
  type LucideIcon,
} from 'lucide-react';
import { useMemo } from 'react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { TabStrip, type TabStripTab } from '@/components/ui/tab-strip';
import { cn, FOCUS_RING } from '@/lib/utils';
import { useI18n } from '@/i18n/I18nProvider';
import type { DockTab } from '../state/workspaceUrl';

export const DOCK_TAB_LABELS: Record<DockTab, string> = {
  log: 'Log',
  agent: 'Agent',
  actions: 'Actions',
  timeline: 'Timeline',
  history: 'History',
  outputs: 'Outputs',
  artifacts: 'Artifacts',
  tasks: 'Tasks',
  chat: 'Chat',
  continuous: 'Continuous',
  definition: 'Definition',
};

const DOCK_TAB_ICONS: Record<DockTab, LucideIcon> = {
  log: ScrollText,
  agent: Bot,
  actions: ClipboardCheck,
  timeline: GanttChart,
  history: History,
  outputs: Package,
  artifacts: Archive,
  tasks: ListChecks,
  chat: MessageSquare,
  continuous: Activity,
  definition: FileCode,
};

export const DOCK_ID_PREFIX = 'ww-dock';

/** The id of a tab button. Another strip on the page gives its own prefix. */
export function dockTabId(tab: DockTab, prefix = DOCK_ID_PREFIX): string {
  return `${prefix}-tab-${tab}`;
}

/** The id of the tab panel of a strip. */
export function dockPanelId(prefix = DOCK_ID_PREFIX): string {
  return `${prefix}-panel`;
}

export const DOCK_PANEL_ID = dockPanelId();

/**
 * The props of a strip of Run panel tabs. A host may add tabs of its own
 * (T beyond DockTab): each of them needs a label and an icon.
 */
export interface DockTabStripProps<T extends string = DockTab> {
  tabs: readonly T[];
  active: T;
  badges: Partial<Record<T, number>>;
  // The tabs give T: a handler that takes more tabs does not widen it.
  onSelect(tab: NoInfer<T>): void;
  /** Prefix of the ids, when a second strip is on the page. */
  idPrefix?: string;
  /** Accessible name of the tab list. Default: 'Logs'. */
  label?: string;
  /** Labels that replace the default ones, as a step view names its tabs. */
  labels?: Partial<Record<T, string>>;
  /** Icons of the tabs that are no Run panel tab. */
  icons?: Partial<Record<T, LucideIcon>>;
  /**
   * Tabs that sit in a More menu at the end of the row, unless active: the
   * ones that are rarely used.
   */
  overflow?: readonly T[];
}

/** The label of a tab: the host's, else the one of the Run panel. */
export function dockTabLabel<T extends string>(
  tab: T,
  labels?: Partial<Record<T, string>>
): string {
  // A host tab without a label shows its id rather than nothing.
  return labels?.[tab] ?? DOCK_TAB_LABELS[tab as string as DockTab] ?? tab;
}

/**
 * Tab strip of the Run panel: the one TabStrip row. At a narrow width the
 * tabs show their icon only, with a tooltip, and the row scrolls if the icons
 * do not fit. The `overflow` tabs are in a More menu after the row; the
 * active one shows in the row.
 */
export function DockTabStrip<T extends string = DockTab>({
  tabs,
  active,
  badges,
  onSelect,
  idPrefix = DOCK_ID_PREFIX,
  label = 'Logs',
  labels,
  icons,
  overflow,
}: DockTabStripProps<T>) {
  const { ts } = useI18n();
  const panelId = dockPanelId(idPrefix);
  const inMenu = (tab: T) => !!overflow?.includes(tab) && tab !== active;
  const rowTabs = tabs.filter((tab) => !inMenu(tab));
  const menuTabs = tabs.filter(inMenu);
  const iconOf = (tab: T) =>
    icons?.[tab] ?? DOCK_TAB_ICONS[tab as string as DockTab];
  const stripTabs = useMemo<TabStripTab[]>(
    () =>
      rowTabs.map((tab) => ({
        id: tab,
        label: ts(dockTabLabel(tab, labels)),
        icon: iconOf(tab),
        badge: badges[tab],
        badgeLabel: badges[tab]
          ? ts('{count} waiting', { count: badges[tab] })
          : undefined,
        controls: panelId,
      })),
    // rowTabs is new for each render: its content is the key.
    [rowTabs.join('|'), badges, panelId, labels, icons, ts]
  );
  return (
    <>
      <TabStrip
        label={ts(label)}
        tabs={stripTabs}
        value={active}
        onChange={(id) => onSelect(id as T)}
        idPrefix={idPrefix}
        className="flex-1"
      />
      {menuTabs.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              data-testid={`${idPrefix}-more-tabs`}
              className={cn(
                'inline-flex h-6 shrink-0 items-center gap-1 self-center rounded-md px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground pointer-coarse:h-9',
                FOCUS_RING
              )}
            >
              <MoreHorizontal className="size-3.5" aria-hidden="true" />
              {ts('More')}
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-40">
            {menuTabs.map((tab) => {
              const Icon = iconOf(tab);
              return (
                <DropdownMenuItem
                  key={tab}
                  className="gap-2 text-xs"
                  onSelect={() => onSelect(tab)}
                >
                  {Icon && <Icon className="size-3.5" aria-hidden="true" />}
                  {ts(dockTabLabel(tab, labels))}
                  {!!badges[tab] && (
                    <span className="ml-auto text-[10px] tabular-nums">
                      {badges[tab]}
                    </span>
                  )}
                </DropdownMenuItem>
              );
            })}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </>
  );
}
