// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import type { DockTab } from '../state/workspaceUrl';
import {
  DOCK_ID_PREFIX,
  DOCK_TAB_LABELS,
  DockTabStrip,
  dockPanelId,
} from './DockTabStrip';

export interface RunTabsPaneProps {
  tabs: DockTab[];
  active: DockTab;
  badges: Partial<Record<DockTab, number>>;
  onSelect(tab: DockTab): void;
  /** Prefix of the ids, when a second pane is on the page. */
  idPrefix?: string;
  /** Accessible name of the tab list. Default: 'Logs'. */
  label?: string;
  /** Labels that replace the default ones: see DockTabStrip. */
  labels?: Partial<Record<DockTab, string>>;
  /** The body of the active tab. */
  children?: ReactNode;
}

/**
 * The right side of the Run panel: the strip of tabs, then the body of the
 * active tab. The dock of Executions and the OUTPUT column of the step view
 * in the editor both render it, so that the two never differ.
 */
export function RunTabsPane({
  tabs,
  active,
  badges,
  onSelect,
  idPrefix = DOCK_ID_PREFIX,
  label,
  labels,
  children,
}: RunTabsPaneProps) {
  const { ts } = useI18n();
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-w-0 shrink-0 border-b border-border px-2">
        <DockTabStrip
          tabs={tabs}
          active={active}
          badges={badges}
          onSelect={onSelect}
          idPrefix={idPrefix}
          label={label}
          labels={labels}
        />
      </div>
      {/* Always present, so the aria-controls of the tabs name an element. */}
      <div
        id={dockPanelId(idPrefix)}
        role="tabpanel"
        aria-label={ts(labels?.[active] ?? DOCK_TAB_LABELS[active])}
        className="min-h-0 flex-1 overflow-hidden"
      >
        {children}
      </div>
    </div>
  );
}
