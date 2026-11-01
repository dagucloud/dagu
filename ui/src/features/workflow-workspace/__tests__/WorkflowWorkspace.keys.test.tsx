// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useParams,
} from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  ApiDAGDetails,
  ApiDAGRunDetails,
  BarSlotProps,
  DAGPageData,
} from '../contracts';
import { run as runDetails } from '../data/__tests__/fixtures';
import { WorkflowWorkspace } from '../WorkflowWorkspace';

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  refresh: vi.fn(async () => undefined),
  palette: vi.fn(),
}));

vi.mock('@/components/command-palette/CommandPaletteProvider', () => ({
  usePaletteCommands: h.palette,
}));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: () => ({
    data: null,
    error: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: false,
  }),
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ basePath: '' }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
}));
vi.mock('../data/useWorkspaceRefresh', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/useWorkspaceRefresh')>()),
  useWorkspaceRefresh: () => h.refresh,
}));
vi.mock('../canvas/WorkspaceCanvas', () => ({
  WorkspaceCanvas: () => (
    <div data-testid="ww-canvas">
      <input aria-label="step name" />
    </div>
  ),
}));
function slotStub(testId: string) {
  return (p: BarSlotProps) => (
    <span data-testid={testId} data-dag-name={p.dag.name} />
  );
}
// The Run panel frame is under test: the timeline and the steps have their own.
vi.mock('../dock/RunTimeline', () => ({ RunTimeline: () => null }));
vi.mock('../dock/RunStepsNav', () => ({ RunStepsNav: () => null }));
vi.mock('../bar/LabelChips', () => ({ LabelChips: slotStub('labels') }));
vi.mock('../bar/TriggerChip', () => ({ TriggerChip: slotStub('trigger') }));
vi.mock('../bar/RunChip', () => ({ RunChip: slotStub('run-chip') }));
vi.mock('../bar/RunActionsCluster', () => ({
  RunActionsCluster: slotStub('run-actions'),
}));
vi.mock('../bar/RunSplitButton', () => ({
  RunSplitButton: slotStub('run-split'),
}));

const HISTORY_PATH = '/dags/{fileName}/dag-runs';
const RUN_PATH = '/dag-runs/{name}/{dagRunId}';

const dagData: DAGPageData = {
  dag: { name: 'x' } as ApiDAGDetails,
  localDags: [],
  latestDAGRun: {} as ApiDAGRunDetails,
  suspended: false,
  pinned: false,
  errors: [],
};

function Page() {
  const { fileName = '', tab } = useParams();
  const location = useLocation();
  return (
    <>
      <output data-testid="location">
        {location.pathname + location.search}
      </output>
      <WorkflowWorkspace
        fileName={fileName}
        tab={tab}
        dagData={dagData}
        refreshDag={async () => undefined}
        workspaceName={null}
        legacyHistoryIdx={null}
      />
    </>
  );
}

function renderAt(url: string) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), shouldRetryOnError: false }}>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path="/dags/:fileName" element={<Page />} />
          <Route path="/dags/:fileName/:tab" element={<Page />} />
        </Routes>
      </MemoryRouter>
    </SWRConfig>
  );
}

beforeEach(() => {
  h.GET.mockReset();
  h.refresh.mockClear();
  h.palette.mockClear();
  h.GET.mockImplementation(
    async (
      path: string,
      init?: { params?: { path?: Record<string, string> } }
    ) => {
      if (path === HISTORY_PATH) {
        return { data: { dagRuns: [], gridData: [] } };
      }
      if (path === RUN_PATH) {
        const id = init?.params?.path?.dagRunId ?? '';
        return { data: { dagRunDetails: runDetails(id) } };
      }
      throw new Error(`unexpected GET ${path}`);
    }
  );
});

describe('WorkflowWorkspace commands', () => {
  // The palette 'Refresh' and the 'r' key call the same page refresh.
  it('registers the workflow commands inside the workspace providers', () => {
    renderAt('/dags/x');
    const [group, commands] = h.palette.mock.lastCall ?? [];
    expect(group).toEqual({ id: 'workflow', label: 'This workflow' });
    const refresh = (commands as Array<{ id: string; run(): void }>).find(
      (c) => c.id === 'refresh'
    );
    refresh?.run();
    expect(h.refresh).toHaveBeenCalledTimes(1);
  });
});

// The 'r' key refreshes the page with or without a run; before the workspace it
// refreshed only the Overview run view.
// parity:chg-refresh-key parity:shell-refresh-shortcut parity:dup-20
describe('WorkflowWorkspace page keys', () => {
  it("refreshes once with 'r' on a pinned run", () => {
    renderAt('/dags/x?run=r1');

    fireEvent.keyDown(document.body, { key: 'r' });

    expect(h.refresh).toHaveBeenCalledTimes(1);
  });

  it("refreshes once with 'r' on the editor", () => {
    renderAt('/dags/x');

    fireEvent.keyDown(document.body, { key: 'r' });

    expect(h.refresh).toHaveBeenCalledTimes(1);
  });

  // The run keys act on the editor view: there is no other view.
  it("loads the history with '[' on the editor", async () => {
    renderAt('/dags/x');

    fireEvent.keyDown(document.body, { key: '[' });

    await waitFor(() =>
      expect(
        h.GET.mock.calls.filter((call) => call[0] === HISTORY_PATH)
      ).toHaveLength(1)
    );
  });

  it("does nothing for 'r' in an input", () => {
    renderAt('/dags/x');
    const input = screen.getByRole('textbox', { name: 'step name' });

    fireEvent.keyDown(input, { key: 'r' });

    expect(h.refresh).not.toHaveBeenCalled();
  });

  it("does nothing for 'l' without a selected step", () => {
    renderAt('/dags/x');

    fireEvent.keyDown(document.body, { key: 'l' });

    expect(screen.getByTestId('location')).toHaveTextContent(/^\/dags\/x$/);
  });

  // parity:spec-ctrl-s
  it('leaves Ctrl+S and Cmd+S to the editor', () => {
    renderAt('/dags/x');
    const input = screen.getByRole('textbox', { name: 'step name' });
    expect(fireEvent.keyDown(input, { key: 's', ctrlKey: true })).toBe(true);
    expect(fireEvent.keyDown(document.body, { key: 's', metaKey: true })).toBe(
      true
    );
  });

  // parity:artifacts-keyboard
  it('leaves the run keys to the artifacts tree in the dock', async () => {
    renderAt('/dags/x?run=r1');
    const panel = screen.getByRole('tabpanel', { hidden: true });
    const tree = document.createElement('div');
    tree.setAttribute('role', 'tree');
    tree.tabIndex = 0;
    panel.appendChild(tree);

    for (const key of ['[', ']', 'r']) {
      expect(fireEvent.keyDown(tree, { key })).toBe(true);
    }
    expect(fireEvent.keyDown(tree, { key: 'L', shiftKey: true })).toBe(true);
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });

    expect(h.refresh).not.toHaveBeenCalled();
    expect(
      h.GET.mock.calls.filter((call) => call[0] === HISTORY_PATH)
    ).toHaveLength(0);
    expect(screen.getByTestId('location')).toHaveTextContent('/dags/x?run=r1');
  });
});
