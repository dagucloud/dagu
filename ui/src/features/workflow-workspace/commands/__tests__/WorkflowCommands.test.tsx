// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CircleStop, RotateCcw } from 'lucide-react';
import type { ReactElement } from 'react';
import { act, cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { PaletteCommand } from '@/components/command-palette/CommandPaletteProvider';
import type {
  ApiDAGDetails,
  WorkspaceEditorHandle,
  WorkspaceEditorState,
} from '../../contracts';
import type { DockTab } from '../../state/workspaceUrl';
import { WorkflowCommands } from '../WorkflowCommands';

type Registered = {
  group: { id: string; label: string };
  commands: PaletteCommand[] | null;
};

const h = vi.hoisted(() => ({
  registered: null as Registered | null,
  editor: { handle: null, state: null } as {
    handle: WorkspaceEditorHandle | null;
    state: WorkspaceEditorState | null;
  },
  run: {} as Record<string, unknown>,
  refresh: vi.fn(async () => undefined),
  dock: {} as Record<string, unknown>,
  frame: { tabs: [] as DockTab[] },
}));

vi.mock('@/components/command-palette/CommandPaletteProvider', () => ({
  usePaletteCommands: (
    group: Registered['group'],
    commands: PaletteCommand[] | null
  ) => {
    h.registered = { group, commands };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => h.editor,
}));
vi.mock('../../data', () => ({
  useWorkspaceRun: () => h.run,
  useWorkspaceRefresh: () => h.refresh,
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => h.dock,
  useDockCommands: () => h.dock,
  useDockFrame: () => h.frame,
}));
vi.mock('../../dock/tabs', () => ({
  dockTabBodies: { timeline: () => null, log: () => null },
}));

const dag = {
  name: 'x',
  steps: [{ name: 'extract' }, { name: 'load' }],
} as unknown as ApiDAGDetails;

function editorState(
  extra: Partial<WorkspaceEditorState> = {}
): WorkspaceEditorState {
  return {
    frozen: false,
    canUndo: true,
    canRedo: true,
    selectedStepName: null,
    drawer: 'none',
    direction: 'TB',
    loaded: true,
    editable: true,
    dirty: true,
    saving: false,
    saveState: 'clean',
    hasDraft: false,
    conflict: false,
    ...extra,
  } as WorkspaceEditorState;
}

function editorHandle(): WorkspaceEditorHandle {
  return {
    fitView: vi.fn(),
    revealStep: vi.fn(() => true),
    addStep: vi.fn(),
    openWorkflowPanel: vi.fn(),
    toggleYaml: vi.fn(),
    openForm: vi.fn(),
    copySelection: vi.fn(),
    paste: vi.fn(),
    duplicateSelection: vi.fn(),
    undo: vi.fn(),
    redo: vi.fn(),
    save: vi.fn(),
    discard: vi.fn(),
    replaceBuffer: vi.fn(),
    isDirty: vi.fn(),
    getBuffer: vi.fn(),
    copyYaml: vi.fn(),
  } as unknown as WorkspaceEditorHandle;
}

function Location() {
  const location = useLocation();
  return (
    <output data-testid="location">
      {location.pathname + location.search}
    </output>
  );
}

function renderAt(url = '/dags/x') {
  cleanup();
  const page = (
    <>
      <Location />
      <WorkflowCommands fileName="x" dag={dag} />
    </>
  );
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/dags/:fileName" element={page} />
        <Route path="/dags/:fileName/:tab" element={page} />
        <Route path="*" element={<Location />} />
      </Routes>
    </MemoryRouter>
  );
}

function commands(): PaletteCommand[] {
  return h.registered?.commands ?? [];
}

function cmd(id: string): PaletteCommand | undefined {
  return commands().find((c) => c.id === id);
}

function must(id: string): PaletteCommand {
  const found = cmd(id);
  if (!found) throw new Error(`no command ${id}`);
  return found;
}

function location() {
  return screen.getByTestId('location').textContent;
}

let controls: HTMLElement[] = [];

function barButton(action: string, disabled = false) {
  const button = document.createElement('button');
  button.setAttribute('data-ww-action', action);
  button.disabled = disabled;
  button.onclick = vi.fn();
  document.body.appendChild(button);
  controls.push(button);
  return button;
}

beforeEach(() => {
  h.registered = null;
  h.editor = { handle: editorHandle(), state: editorState() };
  h.run = {
    hasRun: true,
    selection: { runId: 'r1' },
    ref: { name: 'x', runId: 'r1' },
    select: vi.fn(),
  };
  h.refresh.mockClear();
  h.dock = {
    toggle: vi.fn(),
    openRunLog: vi.fn(),
    setTab: vi.fn(),
  };
  h.frame = { tabs: ['log', 'timeline', 'outputs'] };
});

afterEach(() => {
  for (const control of controls) control.remove();
  controls = [];
});

const EDITOR_IDS = [
  'save',
  'discard',
  'toggle-yaml',
  'workflow-panel',
  'fit-view',
  'add-step',
  'undo',
  'redo',
  'step:extract',
  'step:load',
];

// parity:crit-command-palette-entry
describe('WorkflowCommands', () => {
  it("registers the 'This workflow' group", () => {
    renderAt();
    expect(h.registered?.group).toEqual({
      id: 'workflow',
      label: 'This workflow',
    });
  });

  it('hides the editor commands without an editor handle', () => {
    h.editor = { handle: null, state: null };
    renderAt();
    for (const id of EDITOR_IDS) expect(cmd(id)).toBeUndefined();
    expect(cmd('refresh')).toBeDefined();
  });

  it('gates Save on an editable, dirty, idle editor', () => {
    renderAt();
    expect(must('save').disabled).toBe(false);
    act(() => must('save').run());
    expect(h.editor.handle?.save).toHaveBeenCalledTimes(1);

    for (const state of [
      { editable: false },
      { frozen: true },
      { dirty: false },
      { saving: true },
    ]) {
      h.editor = { ...h.editor, state: editorState(state) };
      renderAt();
      expect(must('save').disabled).toBe(true);
    }
  });

  it('gates Discard on an editable, dirty editor', () => {
    renderAt();
    act(() => must('discard').run());
    expect(h.editor.handle?.discard).toHaveBeenCalledTimes(1);
    for (const state of [
      { editable: false },
      { frozen: true },
      { dirty: false },
    ]) {
      h.editor = { ...h.editor, state: editorState(state) };
      renderAt();
      expect(must('discard').disabled).toBe(true);
    }
  });

  it('gates Add step on an editable editor', () => {
    renderAt();
    act(() => must('add-step').run());
    expect(h.editor.handle?.addStep).toHaveBeenCalledTimes(1);
    for (const state of [{ editable: false }, { frozen: true }]) {
      h.editor = { ...h.editor, state: editorState(state) };
      renderAt();
      expect(must('add-step').disabled).toBe(true);
    }
  });

  it('gates Undo and Redo on the history of the editor', () => {
    renderAt();
    act(() => must('undo').run());
    act(() => must('redo').run());
    expect(h.editor.handle?.undo).toHaveBeenCalledTimes(1);
    expect(h.editor.handle?.redo).toHaveBeenCalledTimes(1);

    h.editor = {
      ...h.editor,
      state: editorState({ canUndo: false, canRedo: false }),
    };
    renderAt();
    expect(must('undo').disabled).toBe(true);
    expect(must('redo').disabled).toBe(true);

    h.editor = { ...h.editor, state: editorState({ frozen: true }) };
    renderAt();
    expect(must('undo').disabled).toBe(true);
    expect(must('redo').disabled).toBe(true);
  });

  // Viewing commands stay enabled on a read-only workflow.
  it('runs Toggle YAML, Workflow panel and Fit view on any editor', () => {
    h.editor = {
      ...h.editor,
      state: editorState({ editable: false, frozen: true }),
    };
    renderAt();
    for (const id of ['toggle-yaml', 'workflow-panel', 'fit-view']) {
      expect(must(id).disabled).toBeFalsy();
      act(() => must(id).run());
    }
    expect(h.editor.handle?.toggleYaml).toHaveBeenCalledTimes(1);
    expect(h.editor.handle?.openWorkflowPanel).toHaveBeenCalledTimes(1);
    expect(h.editor.handle?.fitView).toHaveBeenCalledTimes(1);
  });

  it('offers one Go to step per step: it reveals the step and writes ?step', () => {
    renderAt('/dags/x?run=r1');
    expect(must('step:extract').label).toBe('Go to step extract');
    expect(must('step:extract').keywords).toEqual(['extract']);

    act(() => must('step:load').run());

    expect(h.editor.handle?.revealStep).toHaveBeenCalledWith('load');
    expect(location()).toBe('/dags/x?run=r1&step=load');
  });

  // The editor is the only view: its commands run where the page is.
  it('runs an editor command without a change of page', () => {
    renderAt('/dags/x?run=r1');

    act(() => must('step:extract').run());

    expect(h.editor.handle?.revealStep).toHaveBeenCalledWith('extract');
    expect(location()).toBe('/dags/x?run=r1&step=extract');
  });

  it('toggles the dock and opens the run log', () => {
    renderAt('/dags/x');
    act(() => must('toggle-logs').run());
    act(() => must('run-log').run());
    expect(h.dock.toggle).toHaveBeenCalledTimes(1);
    expect(h.dock.openRunLog).toHaveBeenCalledTimes(1);

    h.run = { ...h.run, hasRun: false, ref: null };
    renderAt('/dags/x');
    expect(cmd('run-log')).toBeUndefined();
    expect(cmd('toggle-logs')).toBeDefined();
  });

  // The dock is the same for every view: its commands show with a run.
  it('offers the dock and run commands on the editor', () => {
    renderAt('/dags/x');
    for (const id of ['toggle-logs', 'run-log', 'follow-latest']) {
      expect(cmd(id)).toBeDefined();
    }
    expect(commands().filter((c) => c.id.startsWith('dock:'))).not.toEqual([]);
  });

  // Log has its own command, 'Open run log'.
  it('opens each dock tab that the run has and that has a body', () => {
    renderAt('/dags/x');
    expect(
      commands()
        .filter((c) => c.id.startsWith('dock:'))
        .map((c) => c.label)
    ).toEqual(['Open Timeline']);

    act(() => must('dock:timeline').run());

    expect(h.dock.setTab).toHaveBeenCalledWith('timeline', { user: true });
  });

  it('has no entry for a mode of the page', () => {
    renderAt('/dags/x?run=r1');
    expect(commands().filter((c) => c.id.startsWith('mode:'))).toEqual([]);
    expect(commands().map((c) => c.label)).not.toContain('Editor');
    expect(commands().map((c) => c.label)).not.toContain('Executions');
  });

  // The runtime profile is in the Overview tab of the Workflow panel.
  it('has no settings command', () => {
    renderAt('/dags/x');
    expect(commands().filter((c) => c.id.startsWith('settings:'))).toEqual([]);
    expect(commands().map((c) => c.label)).not.toContain(
      'Settings > Runtime profile'
    );
  });

  // Wiki, incidents and notifications are tabs of the Workflow panel.
  it('opens the tabs of the Workflow panel', () => {
    renderAt('/dags/x?run=r1');
    expect(
      commands()
        .filter((c) => c.id.startsWith('workflow-tab:'))
        .map((c) => c.label)
    ).toEqual([
      'Workflow > Overview',
      'Workflow > Wiki',
      'Workflow > Incidents',
      'Workflow > Notifications',
    ]);

    act(() => must('workflow-tab:wiki').run());

    // The selected run stays.
    expect(location()).toBe('/dags/x?run=r1&panel=workflow&wtab=wiki');
  });

  it('follows the latest run while a run is pinned', () => {
    renderAt('/dags/x');
    expect(must('follow-latest').shortcut).toBe('Shift L');
    act(() => must('follow-latest').run());
    expect(h.run.select).toHaveBeenCalledWith('latest');

    h.run = { ...h.run, selection: 'latest' };
    renderAt('/dags/x');
    expect(must('follow-latest').disabled).toBe(true);

    h.run = { ...h.run, hasRun: false, ref: null };
    renderAt('/dags/x');
    expect(cmd('follow-latest')).toBeUndefined();
  });

  it('refreshes the page', () => {
    renderAt();
    expect(must('refresh').shortcut).toBe('R');
    act(() => must('refresh').run());
    expect(h.refresh).toHaveBeenCalledTimes(1);
  });

  // D8: the leave-the-page command is removed.
  it('has no command for the run page', () => {
    renderAt();
    expect(commands().map((c) => c.id)).not.toContain('run-page');
  });

  it('has no command for the classic page', () => {
    renderAt();
    expect(commands().map((c) => c.id)).not.toContain('classic-page');
  });

  it('clicks the Run, Stop and Retry controls of the bar', () => {
    renderAt();
    for (const action of ['start', 'stop', 'retry']) {
      const command = must(`bar:${action}`);
      // Absent control: hidden.
      expect(command.available?.()).toBe(false);
      const disabled = barButton(action, true);
      expect(command.available?.()).toBe(false);
      disabled.remove();
      const button = barButton(action);
      expect(command.available?.()).toBe(true);

      act(() => command.run());

      expect(button.onclick).toHaveBeenCalledTimes(1);
    }
    expect((must('bar:stop').icon as ReactElement).type).toBe(CircleStop);
    expect((must('bar:retry').icon as ReactElement).type).toBe(RotateCcw);
    expect(must('bar:start').label).toBe('Run…');
    expect(must('bar:stop').label).toBe('Stop');
    expect(must('bar:retry').label).toBe('Retry');
  });

  it('hides a bar control with aria-disabled', () => {
    renderAt();
    barButton('stop').setAttribute('aria-disabled', 'true');
    expect(must('bar:stop').available?.()).toBe(false);
  });
});
