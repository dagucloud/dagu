// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  dagPage,
  FILE_NAME,
  idleSSE,
  run,
} from '../../data/__tests__/fixtures';
import {
  renderWorkspace,
  serve,
  type FakeServer,
} from '../../data/__tests__/harness';
import { useWorkspaceUrl } from '../../state/workspaceUrl';
import { useWorkspaceKeys, workspaceKeyAction } from '../useWorkspaceKeys';

const h = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => idleSSE }));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => idleSSE,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

// The dock that the 'l' key opens.
const dock = vi.hoisted(() => ({ openStep: vi.fn() }));
vi.mock('../../dock/DockContext', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../dock/DockContext')>()),
  useDock: () => dock,
  useDockCommands: () => dock,
}));

const HISTORY_PATH = '/dags/{fileName}/dag-runs';

let server: FakeServer;
let extra: HTMLElement[] = [];

function historyCalls() {
  return h.GET.mock.calls.filter((call) => call[0] === HISTORY_PATH).length;
}

function mount(tag: string, attrs: Record<string, string> = {}) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) el.setAttribute(key, value);
  document.body.appendChild(el);
  extra.push(el);
  return el;
}

function key(
  init: Partial<KeyboardEvent> & { key: string },
  target: EventTarget = document.body
) {
  return workspaceKeyAction({
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    defaultPrevented: false,
    target,
    ...init,
  });
}

beforeEach(() => {
  window.localStorage.clear();
  h.GET.mockReset();
  // Newest first, as the server sends the history.
  const runs = [run('run-2'), run('run-1'), run('run-0')];
  server = {
    dag: dagPage(runs[0]!),
    runs: Object.fromEntries(runs.map((r) => [r.dagRunId, r])),
    history: { dagRuns: runs, gridData: [] },
    subRuns: [],
  };
  h.GET.mockImplementation(serve(server));
});

afterEach(() => {
  for (const el of extra) el.remove();
  extra = [];
});

// parity:shell-refresh-shortcut parity:chg-refresh-key
// The keys act on the editor view, the only view of a workflow.
function renderKeys(options: Parameters<typeof renderWorkspace>[1] = {}) {
  return renderWorkspace(() => useWorkspaceKeys(), {
    url: `/dags/${FILE_NAME}`,
    ...options,
  });
}

describe('workspaceKeyAction', () => {
  it('maps the page keys', () => {
    expect(key({ key: '[' })).toBe('older');
    expect(key({ key: ']' })).toBe('newer');
    expect(key({ key: 'L', shiftKey: true })).toBe('latest');
    expect(key({ key: 'r' })).toBe('refresh');
    expect(key({ key: 'l' })).toBe('stepLog');
    expect(key({ key: 'L' })).toBeNull();
    expect(key({ key: 'R', shiftKey: true })).toBeNull();
  });

  it('ignores keys with Ctrl, Meta or Alt', () => {
    for (const mod of ['ctrlKey', 'metaKey', 'altKey'] as const) {
      expect(key({ key: '[', [mod]: true })).toBeNull();
      expect(key({ key: 'r', [mod]: true })).toBeNull();
    }
  });

  it('ignores keys in a text entry', () => {
    const targets = [
      mount('input'),
      mount('textarea'),
      mount('select'),
      mount('div', { contenteditable: 'true' }),
    ];
    for (const target of targets) {
      expect(key({ key: 'r' }, target)).toBeNull();
      expect(key({ key: '[' }, target)).toBeNull();
    }
  });

  it('ignores keys in Monaco', () => {
    const editor = mount('div', { class: 'monaco-editor' });
    const inner = document.createElement('div');
    editor.appendChild(inner);
    expect(key({ key: ']' }, inner)).toBeNull();
  });

  it('ignores keys in a dialog', () => {
    const dialog = mount('div', { role: 'dialog' });
    const button = document.createElement('button');
    dialog.appendChild(button);
    expect(key({ key: 'r' }, button)).toBeNull();
    const alert = mount('div', { role: 'alertdialog' });
    expect(key({ key: 'r' }, alert)).toBeNull();
  });

  it('ignores keys while a modal dialog is open', () => {
    mount('div', { role: 'dialog', 'aria-modal': 'true' });
    expect(key({ key: 'L', shiftKey: true })).toBeNull();
  });

  // A hidden pane stays mounted with its dialogs; its aria-modal node must
  // not block the page keys.
  it('ignores a modal dialog inside an inert or hidden pane', () => {
    for (const attr of ['inert', 'hidden']) {
      const pane = mount('div', { [attr]: '' });
      const dialog = document.createElement('div');
      dialog.setAttribute('role', 'dialog');
      dialog.setAttribute('aria-modal', 'true');
      pane.appendChild(dialog);
      expect(key({ key: 'l' })).toBe('stepLog');
      expect(key({ key: '[' })).toBe('older');
      pane.remove();
    }
  });

  it('still honours a modal dialog outside an inert pane', () => {
    const pane = mount('div', { inert: '' });
    pane.appendChild(document.createElement('div'));
    mount('div', { role: 'dialog', 'aria-modal': 'true' });
    expect(key({ key: 'l' })).toBeNull();
  });

  it('ignores keys from inside an inert or hidden pane', () => {
    for (const attr of ['inert', 'hidden']) {
      const pane = mount('div', { [attr]: '' });
      const button = document.createElement('button');
      pane.appendChild(button);
      expect(key({ key: 'l' }, button)).toBeNull();
      expect(key({ key: 'r' }, button)).toBeNull();
    }
  });

  // parity:artifacts-keyboard
  it('leaves keys to a dock panel and the artifacts tree', () => {
    const panel = mount('div', { role: 'tabpanel' });
    const row = document.createElement('div');
    panel.appendChild(row);
    expect(key({ key: '[' }, row)).toBeNull();
    const tree = mount('div', { role: 'tree' });
    expect(key({ key: ']' }, tree)).toBeNull();
  });

  it('ignores keys in an open menu, a listbox or a text box', () => {
    for (const role of ['menu', 'listbox', 'textbox']) {
      const owner = mount('div', { role });
      const item = document.createElement('div');
      owner.appendChild(item);
      expect(key({ key: 'r' }, item)).toBeNull();
    }
  });

  it('ignores a key that a handler already consumed', () => {
    expect(key({ key: 'r', defaultPrevented: true })).toBeNull();
  });
});

describe('useWorkspaceKeys', () => {
  it('loads the history at the first press and selects the older run', async () => {
    const view = renderKeys();
    // The page names the latest run in the URL first.
    await waitFor(() =>
      expect(view.result.current?.location.search).toBe('?run=run-2')
    );
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    expect(historyCalls()).toBe(0);

    fireEvent.keyDown(document.body, { key: '[' });

    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-1')
    );
    expect(historyCalls()).toBe(1);
    expect(view.result.current.navigationType).toBe('PUSH');

    fireEvent.keyDown(document.body, { key: '[' });
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-0')
    );

    fireEvent.keyDown(document.body, { key: ']' });
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-1')
    );

    fireEvent.keyDown(document.body, { key: 'L', shiftKey: true });
    // Latest names the latest run: the URL always has a ?run.
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-2')
    );
  });

  it('does not load the history for a key in an input', async () => {
    const view = renderKeys();
    await waitFor(() =>
      expect(view.result.current?.location.search).toBe('?run=run-2')
    );
    const input = mount('input');

    fireEvent.keyDown(input, { key: '[' });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });

    expect(historyCalls()).toBe(0);
    expect(view.result.current.location.search).toBe('?run=run-2');
  });

  it("opens the dock on the log of the selected step with 'l'", async () => {
    dock.openStep.mockClear();
    const view = renderKeys({
      url: `/dags/${FILE_NAME}?step=build`,
    });
    await waitFor(() => expect(view.result.current?.location).toBeDefined());

    fireEvent.keyDown(document.body, { key: 'l' });
    expect(dock.openStep).toHaveBeenCalledWith('build');
  });

  // The router renders a URL write in a transition. A key that comes right
  // after a click on a step, or on the empty canvas, acts on the written URL.
  it("acts with 'l' on a step that the router has not rendered yet", async () => {
    dock.openStep.mockClear();
    const view = renderWorkspace(
      () => {
        useWorkspaceKeys();
        return useWorkspaceUrl()[1];
      },
      { url: `/dags/${FILE_NAME}` }
    );
    await waitFor(() =>
      expect(view.result.current?.location.search).toBe('?run=run-2')
    );

    act(() => {
      view.result.current.value({ step: 'build' }, { replace: true });
      fireEvent.keyDown(document.body, { key: 'l' });
    });
    expect(dock.openStep).toHaveBeenCalledWith('build');

    dock.openStep.mockClear();
    act(() => {
      view.result.current.value({ step: null }, { replace: true });
      fireEvent.keyDown(document.body, { key: 'l' });
    });
    expect(dock.openStep).not.toHaveBeenCalled();
  });

  it("does not open the dock with 'l' without a selected step", async () => {
    dock.openStep.mockClear();
    const view = renderKeys();
    await waitFor(() => expect(view.result.current?.location).toBeDefined());

    fireEvent.keyDown(document.body, { key: 'l' });
    expect(dock.openStep).not.toHaveBeenCalled();
  });

  it("refreshes the page with 'r'", async () => {
    const refreshDag = vi.fn(async () => undefined);
    const view = renderKeys({ refreshDag });
    await waitFor(() => expect(view.result.current?.location).toBeDefined());

    const event = fireEvent.keyDown(document.body, { key: 'r' });

    expect(event).toBe(false);
    await waitFor(() => expect(refreshDag).toHaveBeenCalledTimes(1));
  });

  // parity:spec-ctrl-s
  it('never prevents Ctrl+S or Cmd+S', async () => {
    const view = renderKeys();
    await waitFor(() => expect(view.result.current?.location).toBeDefined());
    expect(fireEvent.keyDown(document.body, { key: 's', ctrlKey: true })).toBe(
      true
    );
    expect(fireEvent.keyDown(document.body, { key: 's', metaKey: true })).toBe(
      true
    );
  });

  // The run keys no longer need the Executions view.
  it('acts on the run and dock keys in the editor', async () => {
    dock.openStep.mockClear();
    const view = renderKeys({ url: `/dags/${FILE_NAME}?step=build` });
    await waitFor(() =>
      expect(view.result.current?.location.search).toBe('?step=build&run=run-2')
    );

    expect(fireEvent.keyDown(document.body, { key: 'l' })).toBe(false);
    expect(dock.openStep).toHaveBeenCalledWith('build');
    expect(fireEvent.keyDown(document.body, { key: '[' })).toBe(false);
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?step=build&run=run-1')
    );
    expect(historyCalls()).toBe(1);
    expect(fireEvent.keyDown(document.body, { key: 'L', shiftKey: true })).toBe(
      false
    );
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?step=build&run=run-2')
    );
  });

  // The keys use the runs of the timeline: a test run is one of them while
  // the timeline shows the test runs, and none while it hides them.
  describe('with a test run in the history', () => {
    beforeEach(() => {
      const test = run('test-1', [], undefined, { ephemeral: true });
      server.history = {
        dagRuns: [test, ...server.history.dagRuns],
        gridData: [],
      };
      // The test run is not the latest run of the workflow.
      server.runs['test-1'] = test;
    });

    it('goes from a normal run to a test run and back', async () => {
      const view = renderKeys({ url: `/dags/${FILE_NAME}?run=run-1` });
      await waitFor(() =>
        expect(view.result.current?.location.search).toBe('?run=run-1')
      );
      fireEvent.keyDown(document.body, { key: ']' });
      await waitFor(() =>
        expect(view.result.current.location.search).toBe('?run=run-2')
      );
      fireEvent.keyDown(document.body, { key: ']' });
      await waitFor(() =>
        expect(view.result.current.location.search).toBe('?run=test-1')
      );
      fireEvent.keyDown(document.body, { key: '[' });
      await waitFor(() =>
        expect(view.result.current.location.search).toBe('?run=run-2')
      );
    });

    it('skips the test runs that the timeline hides', async () => {
      window.localStorage.setItem('dagu.workspace.runTimelineTests', '0');
      const view = renderKeys({ url: `/dags/${FILE_NAME}?run=run-1` });
      await waitFor(() =>
        expect(view.result.current?.location.search).toBe('?run=run-1')
      );
      fireEvent.keyDown(document.body, { key: ']' });
      await waitFor(() =>
        expect(view.result.current.location.search).toBe('?run=run-2')
      );
      fireEvent.keyDown(document.body, { key: ']' });
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
      expect(view.result.current.location.search).toBe('?run=run-2');
    });
  });
});
