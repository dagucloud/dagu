// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  CircleStop,
  Crosshair,
  FileCode,
  History,
  Maximize,
  PanelBottom,
  PanelRight,
  Play,
  Plus,
  Redo2,
  RefreshCw,
  RotateCcw,
  Save,
  ScrollText,
  Undo2,
  X,
} from 'lucide-react';
import { useMemo, type ReactNode } from 'react';
import {
  usePaletteCommands,
  type PaletteCommand,
} from '@/components/command-palette/CommandPaletteProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { ApiDAGDetails, WorkspaceEditorHandle } from '../contracts';
import { useWorkspaceRefresh, useWorkspaceRun } from '../data';
import { useDockCommands, useDockFrame } from '../dock/DockContext';
import { DOCK_TAB_LABELS } from '../dock/DockTabStrip';
import { dockTabBodies } from '../dock/tabs';
import { WORKFLOW_TABS } from '../panel/WorkflowTabsPanel';
import { useSettingsLeaveGuard } from '../settings/useSettingsLeaveGuard';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/** Value of the data-ww-action attribute of a run control in the bar. */
export type BarAction = 'start' | 'stop' | 'retry';

function barControl(action: BarAction): HTMLElement | null {
  return document.querySelector<HTMLElement>(`[data-ww-action="${action}"]`);
}

/** True when the bar shows the control and it is enabled. */
export function barControlAvailable(action: BarAction): boolean {
  const control = barControl(action);
  return (
    control !== null &&
    !control.matches(':disabled') &&
    control.getAttribute('aria-disabled') !== 'true'
  );
}

/**
 * Registers the 'This workflow' group of the command palette. Mounted once,
 * inside the workspace providers and outside the mode panes.
 */
export function WorkflowCommands({
  dag,
}: {
  fileName: string;
  dag: ApiDAGDetails;
}) {
  const { ts } = useI18n();
  const [, setUrl] = useWorkspaceUrl();
  const { guard } = useSettingsLeaveGuard();
  const { handle, state } = useWorkspaceEditor();
  const run = useWorkspaceRun();
  const refresh = useWorkspaceRefresh();
  const dock = useDockCommands();
  const frame = useDockFrame();

  const commands = useMemo<PaletteCommand[]>(() => {
    const list: PaletteCommand[] = [];

    if (handle) {
      const inEditor = (fn: (h: WorkspaceEditorHandle) => void) => (): void =>
        fn(handle);
      const locked = !state?.editable || state.frozen;
      list.push(
        {
          id: 'save',
          label: ts('Save'),
          icon: <Save />,
          disabled: locked || !state?.dirty || state.saving,
          run: inEditor((h) => h.save()),
        },
        {
          id: 'discard',
          label: ts('Discard'),
          icon: <X />,
          disabled: locked || !state?.dirty,
          run: inEditor((h) => h.discard()),
        },
        {
          id: 'toggle-yaml',
          label: ts('Toggle YAML'),
          keywords: ['yaml', 'spec'],
          icon: <FileCode />,
          run: inEditor((h) => h.toggleYaml()),
        },
        {
          id: 'workflow-panel',
          label: ts('Workflow panel'),
          icon: <PanelRight />,
          run: inEditor((h) => h.openWorkflowPanel()),
        },
        {
          id: 'fit-view',
          label: ts('Fit view'),
          icon: <Maximize />,
          run: inEditor((h) => h.fitView()),
        },
        {
          id: 'add-step',
          label: ts('Add step…'),
          icon: <Plus />,
          disabled: locked,
          run: inEditor((h) => h.addStep()),
        },
        {
          id: 'undo',
          label: ts('Undo'),
          icon: <Undo2 />,
          disabled: locked || !state?.canUndo,
          run: inEditor((h) => h.undo()),
        },
        {
          id: 'redo',
          label: ts('Redo'),
          icon: <Redo2 />,
          disabled: locked || !state?.canRedo,
          run: inEditor((h) => h.redo()),
        }
      );
      for (const step of dag.steps ?? []) {
        const name = step.name;
        if (!name) continue;
        list.push({
          id: `step:${name}`,
          label: ts('Go to step {name}', { name }),
          keywords: [name],
          icon: <Crosshair />,
          run: inEditor((h) => {
            setUrl({ step: name });
            h.revealStep(name);
          }),
        });
      }
    }

    list.push({
      id: 'toggle-logs',
      label: ts('Toggle logs'),
      icon: <PanelBottom />,
      run: dock.toggle,
    });
    if (run.hasRun) {
      list.push({
        id: 'run-log',
        label: ts('Open run log'),
        icon: <ScrollText />,
        run: dock.openRunLog,
      });
    }
    for (const tab of frame.tabs) {
      if (tab === 'log' || !dockTabBodies[tab]) continue;
      list.push({
        id: `dock:${tab}`,
        label: ts('Open {tab}', { tab: ts(DOCK_TAB_LABELS[tab]) }),
        icon: <PanelBottom />,
        run: () => dock.setTab(tab, { user: true }),
      });
    }
    for (const tab of WORKFLOW_TABS) {
      list.push({
        id: `workflow-tab:${tab.id}`,
        label: ts('Workflow > {tab}', { tab: ts(tab.label) }),
        keywords: [ts('Workflow')],
        icon: <PanelRight />,
        run: () =>
          guard(() =>
            setUrl({
              mode: 'workspace',
              panel: 'workflow',
              wtab: tab.id === 'overview' ? null : tab.id,
            })
          ),
      });
    }

    if (run.hasRun) {
      list.push({
        id: 'follow-latest',
        label: ts('Follow latest run'),
        shortcut: 'Shift L',
        icon: <History />,
        disabled: run.selection === 'latest',
        run: () => run.select('latest'),
      });
    }
    list.push({
      id: 'refresh',
      label: ts('Refresh'),
      shortcut: 'R',
      icon: <RefreshCw />,
      run: () => void refresh().catch(() => undefined),
    });

    const barCommands: Array<{
      action: BarAction;
      label: string;
      icon: ReactNode;
    }> = [
      { action: 'start', label: ts('Run…'), icon: <Play /> },
      { action: 'stop', label: ts('Stop'), icon: <CircleStop /> },
      { action: 'retry', label: ts('Retry'), icon: <RotateCcw /> },
    ];
    for (const { action, label, icon } of barCommands) {
      list.push({
        id: `bar:${action}`,
        label,
        icon,
        available: () => barControlAvailable(action),
        run: () => barControl(action)?.click(),
      });
    }
    return list;
  }, [
    dag.steps,
    dock,
    frame.tabs,
    guard,
    handle,
    refresh,
    run,
    setUrl,
    state,
    ts,
  ]);

  usePaletteCommands({ id: 'workflow', label: ts('This workflow') }, commands);
  return null;
}
