// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  FileCode,
  Footprints,
  Maximize2,
  Minimize2,
  PanelRightClose,
  SlidersHorizontal,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { TabStrip } from '@/components/ui/tab-strip';
import {
  PANEL_HEADER_CLASS,
  panelMaxWidth,
  useViewportWidth,
  ZONE_PANEL_TOP,
} from '@/features/dags/components/step-details/PanelShell';
import {
  PanelResizeHandle,
  useDrawerWidth,
} from '@/features/dags/components/step-details/StepSideDrawer';
import { TOOLBAR_ZONE_CLASS } from '@/features/visual-editor/canvas/toolbarHost';
import {
  LAUNCHER_ICONS,
  LAUNCHER_LABELS,
} from '@/features/visual-editor/launchers/LauncherNode';
import type { LauncherId } from '@/features/visual-editor/launchers/model';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { RightColumnTab } from './useRightColumn';

// The width of the collapsed column: one column of icon buttons.
const RAIL_CLASS = 'w-10';

export interface RightColumnProps {
  /** The tab that shows; null collapses the column to its rail. */
  tab: RightColumnTab | null;
  onSelect(tab: RightColumnTab): void;
  onCollapse(): void;
  /**
   * The open column covers the graph zone of the row, under the row of the
   * canvas toolbars, until it is restored.
   */
  maximized: boolean;
  onMaximizedChange(maximized: boolean): void;
  /** The elements that the Workflow panel and the YAML of the file render into. */
  workflowRef(element: HTMLDivElement | null): void;
  yamlRef(element: HTMLDivElement | null): void;
  /**
   * The step of the temporary Step tab, and its details. While it is set
   * the tab shows; choosing another tab or collapsing ends it.
   */
  step?: string | null;
  stepDetails?: ReactNode;
  /**
   * The trigger whose panel shows in a temporary tab, and the element that
   * the panel renders into.
   */
  launcher?: LauncherId | null;
  launcherRef(element: HTMLDivElement | null): void;
  hidden?: boolean;
}

/**
 * The right column of the workflow page: the Workflow panel and the YAML of
 * the file as two tabs, beside the graph zone. It is open on a tab or
 * collapsed to a rail of their buttons, and only the user changes that.
 * Both tabs stay mounted, so that a draft or the editor survives a switch.
 * The details of a clicked step, or the panel of a clicked trigger, show in
 * a temporary tab over the chosen one, which comes back when it closes.
 */
export function RightColumn({
  tab,
  onSelect,
  onCollapse,
  maximized,
  onMaximizedChange,
  workflowRef,
  yamlRef,
  step = null,
  stepDetails,
  launcher = null,
  launcherRef,
  hidden = false,
}: RightColumnProps) {
  const { ts } = useI18n();
  const width = useDrawerWidth();
  const viewport = useViewportWidth();
  const shown = step !== null ? 'step' : launcher !== null ? 'launcher' : tab;
  const open = shown !== null;
  const fills = open && maximized;
  const openWidth = {
    width: `${width}px`,
    maxWidth: panelMaxWidth(viewport),
  };
  return (
    <>
      {/* The maximized column keeps its place in the row, so that the graph
          zone under it keeps its size and its view. */}
      {fills && <div aria-hidden className="shrink-0" style={openWidth} />}
      <aside
        aria-label={ts('Workflow and YAML')}
        data-testid="ww-right-column"
        data-tab={shown ?? 'collapsed'}
        data-maximized={fills || undefined}
        hidden={hidden}
        className={cn(
          'flex h-full min-h-0 flex-col bg-card',
          fills
            ? // The toolbars of the zone, with Save, stay over its top rows.
              cn('absolute inset-0', TOOLBAR_ZONE_CLASS, ZONE_PANEL_TOP)
            : 'relative shrink-0 border-l border-border',
          !open && RAIL_CLASS
        )}
        style={open && !fills ? openWidth : undefined}
      >
        {open && !fills && <PanelResizeHandle shown={!hidden} />}
        {open ? (
          <div className={PANEL_HEADER_CLASS}>
            <TabStrip
              label={ts('Workflow and YAML')}
              tabs={[
                {
                  id: 'workflow',
                  label: ts('Workflow'),
                  icon: SlidersHorizontal,
                },
                { id: 'yaml', label: ts('YAML'), icon: FileCode },
                ...(step !== null
                  ? [{ id: 'step', label: step, icon: Footprints }]
                  : []),
                ...(launcher !== null
                  ? [
                      {
                        id: 'launcher',
                        label: ts(LAUNCHER_LABELS[launcher]),
                        icon: LAUNCHER_ICONS[launcher],
                      },
                    ]
                  : []),
              ]}
              value={shown}
              onChange={(id) => {
                if (id === 'workflow' || id === 'yaml') onSelect(id);
              }}
              className="flex-1"
            />
            <IconButton
              label={ts(fills ? 'Restore panel' : 'Maximize panel')}
              icon={fills ? Minimize2 : Maximize2}
              onClick={() => onMaximizedChange(!fills)}
            />
            <IconButton
              label={ts('Collapse')}
              icon={PanelRightClose}
              onClick={onCollapse}
            />
          </div>
        ) : (
          <div className="flex flex-col items-center gap-1 py-2">
            <IconButton
              label={ts('Workflow')}
              icon={SlidersHorizontal}
              onClick={() => onSelect('workflow')}
            />
            <IconButton
              label={ts('YAML')}
              icon={FileCode}
              onClick={() => onSelect('yaml')}
            />
          </div>
        )}
        <div
          ref={workflowRef}
          hidden={shown !== 'workflow'}
          data-testid="ww-right-column-workflow"
          className="flex min-h-0 flex-1 flex-col"
        />
        <div
          ref={yamlRef}
          hidden={shown !== 'yaml'}
          data-testid="ww-right-column-yaml"
          className="flex min-h-0 flex-1 flex-col"
        />
        <div
          ref={launcherRef}
          hidden={shown !== 'launcher'}
          data-testid="ww-right-column-launcher"
          className="flex min-h-0 flex-1 flex-col"
        />
        {step !== null && (
          <div
            data-testid="ww-right-column-step"
            className="flex min-h-0 flex-1 flex-col"
          >
            {stepDetails}
          </div>
        )}
      </aside>
    </>
  );
}
