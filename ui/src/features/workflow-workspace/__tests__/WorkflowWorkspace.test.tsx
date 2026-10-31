// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useContext, type ComponentProps } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
  useParams,
} from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { useRightPanel } from '@/contexts/RightPanelContext';
import { DAGContext } from '@/features/dags/contexts/DAGContext';

import type {
  ApiDAGDetails,
  ApiDAGRunDetails,
  BarSlotProps,
  DAGPageData,
} from '../contracts';
import { run as runDetails } from '../data/__tests__/fixtures';
import { WorkflowWorkspace } from '../WorkflowWorkspace';

const h = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));

// The real swr-openapi hook over a fake client, as in the data tests.
vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET, POST: h.POST }),
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

// The editor slot reads the contexts that the workspace gives to its body.
vi.mock('../canvas/WorkspaceCanvas', () => ({
  WorkspaceCanvas: (p: { fileName: string; dagData: DAGPageData }) => {
    const ctx = useContext(DAGContext);
    const ndv = useRightPanel('ndv', ndvEvicted);
    const yaml = useRightPanel('yaml', () => undefined);
    return (
      <div
        data-testid="ww-canvas"
        data-file-name={p.fileName}
        data-dag-name={p.dagData.dag?.name}
        data-ctx-name={ctx.name}
        data-ctx-file-name={ctx.fileName}
        data-ctx-auto-open={String(ctx.autoOpenStartModal)}
        data-ctx-force-enqueue={String(ctx.forceEnqueue)}
        data-ctx-on-enqueue={typeof ctx.onEnqueue}
      >
        <button type="button" onClick={ndv.claim}>
          claim ndv
        </button>
        <Dialog>
          <DialogTrigger>open shared dialog</DialogTrigger>
          <DialogContent aria-describedby={undefined}>
            <DialogTitle>shared dialog</DialogTitle>
          </DialogContent>
        </Dialog>
        <button type="button" onClick={yaml.claim}>
          claim yaml
        </button>
        <button type="button" onClick={() => void ctx.refresh()}>
          context refresh
        </button>
        <button
          type="button"
          onClick={() =>
            void Promise.resolve(
              ctx.onEnqueue?.('p=1', 'chosen-id', true, undefined, undefined)
            ).catch(startFailed)
          }
        >
          start now
        </button>
        <button
          type="button"
          onClick={() =>
            void Promise.resolve(ctx.onEnqueue?.('', undefined, false)).catch(
              startFailed
            )
          }
        >
          start queued
        </button>
      </div>
    );
  },
}));

// Bar slots that other files own.
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

const onEnqueue = vi.fn();
// What the page puts in its DAGContext; a test can clear it.
let pageEnqueue: typeof onEnqueue | undefined = onEnqueue;
const ndvEvicted = vi.fn();
const parentRefresh = vi.fn();
// The Start dialog of the canvas shows the error of a failed start.
const startFailed = vi.fn();

const RUN_PATH = '/dag-runs/{name}/{dagRunId}';
const HISTORY_PATH = '/dags/{fileName}/dag-runs';

const dagData: DAGPageData = {
  dag: { name: 'x' } as ApiDAGDetails,
  localDags: [],
  latestDAGRun: {} as ApiDAGRunDetails,
  suspended: false,
  pinned: false,
  errors: [],
};

function Page({
  refreshDag,
  legacyHistoryIdx = null,
}: {
  refreshDag: () => Promise<unknown>;
  legacyHistoryIdx?: number | null;
}) {
  const { fileName = '', tab } = useParams();
  const location = useLocation();
  const navigationType = useNavigationType();
  return (
    <>
      <output data-testid="location" data-navigation={navigationType}>
        {location.pathname + location.search}
      </output>
      <WorkflowWorkspace
        fileName={fileName}
        tab={tab}
        dagData={dagData}
        refreshDag={refreshDag}
        workspaceName={null}
        legacyHistoryIdx={legacyHistoryIdx}
      />
    </>
  );
}

function renderAt(
  url: string,
  refreshDag: ComponentProps<typeof Page>['refreshDag'] = async () => undefined,
  legacyHistoryIdx: number | null = null
) {
  const page = (
    <Page refreshDag={refreshDag} legacyHistoryIdx={legacyHistoryIdx} />
  );
  // The page keeps a DAGContext of its own; the workspace overrides part of it.
  return render(
    <SWRConfig value={{ provider: () => new Map(), shouldRetryOnError: false }}>
      <DAGContext.Provider
        value={{
          refresh: parentRefresh,
          name: 'page-name',
          fileName: 'page-file',
          autoOpenStartModal: true,
          forceEnqueue: true,
          onEnqueue: pageEnqueue,
        }}
      >
        <MemoryRouter initialEntries={[url]}>
          <Routes>
            <Route path="/dags/:fileName" element={page} />
            <Route path="/dags/:fileName/:tab" element={page} />
          </Routes>
        </MemoryRouter>
      </DAGContext.Provider>
    </SWRConfig>
  );
}

function count(path: string) {
  return h.GET.mock.calls.filter((call) => call[0] === path).length;
}

beforeEach(() => {
  h.GET.mockReset();
  h.POST.mockReset();
  onEnqueue.mockReset();
  pageEnqueue = onEnqueue;
  parentRefresh.mockReset();
  ndvEvicted.mockReset();
  startFailed.mockReset();
  // Every pinned run exists, so ?run stays in the URL.
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

// parity:test-dag-page-untested-shell
// parity:test-details-content-tabs
describe('WorkflowWorkspace', () => {
  // One pane: the canvas, with the dock under it, collapsed at load.
  it('renders the bar, one canvas pane and the collapsed dock', () => {
    renderAt('/dags/x');
    expect(screen.getByTestId('ww-bar')).toBeInTheDocument();
    expect(screen.getAllByTestId('ww-pane')).toHaveLength(1);
    expect(screen.getByTestId('ww-pane')).toContainElement(
      screen.getByTestId('ww-canvas')
    );
    expect(screen.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );
    expect(screen.queryByTestId('ww-executions')).not.toBeInTheDocument();
  });

  // A shared dialog opened from the node view must show above it (z-[60]).
  it('raises the shared dialogs above the node view', async () => {
    renderAt('/dags/x');
    fireEvent.click(screen.getByRole('button', { name: 'open shared dialog' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveClass('z-[80]');
  });

  // The page redirects /executions before the workspace renders; a workspace
  // that still gets the path shows the same single pane.
  it('shows one pane and the dock on an /executions path', () => {
    renderAt('/dags/x/executions');
    expect(screen.getAllByTestId('ww-pane')).toHaveLength(1);
    expect(screen.queryByTestId('ww-executions')).not.toBeInTheDocument();
    expect(screen.getByTestId('ww-bar')).toBeInTheDocument();
    expect(screen.getByTestId('ww-dock')).toBeInTheDocument();
  });

  // The page passes the index of a /history?idx=n or /executions?idx=n link.
  // The workspace resolves it on the run of that history column and replaces
  // the URL.
  it.each(['history', 'executions'])(
    'resolves a legacy %s index at the root',
    async (tab) => {
      const older = runDetails('older-run');
      h.GET.mockImplementation(async (path: string) => {
        if (path === HISTORY_PATH) {
          return {
            data: {
              dagRuns: [runDetails('newer-run'), older],
              gridData: [],
            },
          };
        }
        return { data: { dagRunDetails: older } };
      });
      renderAt(`/dags/x/${tab}?idx=0&remoteNode=edge`, undefined, 0);
      await waitFor(() =>
        expect(screen.getByTestId('location')).toHaveTextContent(
          '/dags/x?run=older-run&dock=timeline&remoteNode=edge'
        )
      );
      expect(screen.getByTestId('location')).toHaveAttribute(
        'data-navigation',
        'REPLACE'
      );
      expect(screen.getAllByTestId('ww-pane')).toHaveLength(1);
      // D6: a link to a run opens the Run panel, also after the redirect.
      expect(screen.getByTestId('ww-dock')).toHaveAttribute(
        'data-state',
        'open'
      );
    }
  );

  it('reads no history without a legacy index', () => {
    renderAt('/dags/x');
    expect(count(HISTORY_PATH)).toBe(0);
  });

  // The page redirects /settings to the Workflow panel before the workspace
  // renders: the workspace itself has no settings dialog and no such path.
  it('shows the editor, with no settings dialog, on a /settings path', () => {
    renderAt('/dags/x/settings');
    expect(screen.getByTestId('ww-pane')).not.toHaveAttribute('hidden');
    expect(screen.queryByTestId('ww-settings-dialog')).not.toBeInTheDocument();
  });
});

// The workflow and its selected run share one pane.
describe('WorkflowWorkspace workflow and run selection', () => {
  it('shows no run chip, but the dock, for a workflow that never ran', () => {
    renderAt('/dags/x');
    expect(screen.queryByTestId('ww-bar-run-chip')).not.toBeInTheDocument();
    expect(screen.queryByTestId('run-chip')).not.toBeInTheDocument();
    expect(screen.getByTestId('ww-dock')).toBeInTheDocument();
  });

  // One tab: the bar has no mode tabs, and the chip shows on every path.
  it('shows the run chip and the run actions on the editor path', () => {
    dagData.latestDAGRun = runDetails('latest-1');
    try {
      renderAt('/dags/x');
      expect(screen.getByTestId('run-chip')).toBeInTheDocument();
      expect(screen.getByTestId('run-actions')).toBeInTheDocument();
      expect(screen.getByTestId('ww-dock')).toBeInTheDocument();
      expect(screen.queryByRole('link', { name: 'Executions' })).toBeNull();
      expect(screen.queryByRole('link', { name: 'Editor' })).toBeNull();
    } finally {
      dagData.latestDAGRun = {} as ApiDAGRunDetails;
    }
  });

  // ?run opens the dock on that run, on the same pane.
  it('shows run r1 in the opened dock for ?run=r1', async () => {
    renderAt('/dags/x?run=r1&remoteNode=n1&step=extract');
    const where = screen.getByTestId('location');
    expect(where).toHaveTextContent(
      /^\/dags\/x\?run=r1&remoteNode=n1&step=extract$/
    );
    expect(where).toHaveAttribute('data-navigation', 'POP');
    expect(screen.getAllByTestId('ww-pane')).toHaveLength(1);
    expect(screen.getByTestId('ww-dock')).toHaveAttribute('data-state', 'open');
    await waitFor(() =>
      expect(h.GET).toHaveBeenCalledWith(
        RUN_PATH,
        expect.objectContaining({
          params: expect.objectContaining({
            path: expect.objectContaining({ dagRunId: 'r1' }),
          }),
        })
      )
    );
  });

  it('keeps the workflow route without ?run', () => {
    renderAt('/dags/x?step=extract');
    expect(screen.getByTestId('location')).toHaveTextContent(
      /^\/dags\/x\?step=extract$/
    );
    expect(screen.getByTestId('ww-pane')).not.toHaveAttribute('hidden');
    expect(screen.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );
  });
});

// Starting a run from the bar ends on the run, in the same pane.
describe('WorkflowWorkspace start', () => {
  const clickStart = async (name: string) => {
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name }));
    });
  };

  it('selects the started run through the page enqueue', async () => {
    renderAt('/dags/x?step=extract');
    await clickStart('start now');
    expect(onEnqueue).toHaveBeenCalledWith(
      'p=1',
      'chosen-id',
      true,
      undefined,
      undefined
    );
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dags/x?run=chosen-id'
      )
    );
    // The start pushes ?run and the page stays on the editor.
    expect(screen.getByTestId('location')).toHaveAttribute(
      'data-navigation',
      'PUSH'
    );
  });

  it('prefers the run ID that the page enqueue returns', async () => {
    onEnqueue.mockResolvedValue('server-id');
    renderAt('/dags/x');
    await clickStart('start now');
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dags/x?run=server-id'
      )
    );
  });

  it.each([
    [
      'start now',
      '/dags/{fileName}/start',
      { params: 'p=1', dagRunId: 'chosen-id' },
    ],
    ['start queued', '/dags/{fileName}/enqueue', { params: '' }],
  ])(
    'posts %s and selects the new run when the page has no enqueue',
    async (button, path, body) => {
      pageEnqueue = undefined;
      h.POST.mockResolvedValue({ data: { dagRunId: 'srv-1' } });
      renderAt('/dags/x');
      await clickStart(button);
      await waitFor(() =>
        expect(screen.getByTestId('location')).toHaveTextContent(
          '/dags/x?run=srv-1'
        )
      );
      expect(h.POST).toHaveBeenCalledWith(path, {
        params: { path: { fileName: 'x' }, query: { remoteNode: 'local' } },
        body,
      });
    }
  );

  it('stays on the editor when the start request fails', async () => {
    pageEnqueue = undefined;
    h.POST.mockResolvedValue({ error: { message: 'boom' } });
    renderAt('/dags/x');
    await clickStart('start now');
    expect(h.POST).toHaveBeenCalled();
    await waitFor(() =>
      expect(startFailed).toHaveBeenCalledWith(
        expect.objectContaining({ message: 'boom' })
      )
    );
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/dags\/x$/);
  });
});

// Browsers apply the classes; layout.spec.ts measures the result.
describe('WorkflowWorkspace layout', () => {
  it.each([
    ['the workflow', '/dags/x'],
    ['a pinned run', '/dags/x?run=r1'],
  ])('stacks the bar, the main zone and the dock on %s', (_name, path) => {
    renderAt(path);
    const root = screen.getByTestId('workflow-workspace');
    const zone = screen.getByTestId('ww-zone-main');
    expect(Array.from(root.children)).toEqual([
      screen.getByTestId('ww-bar'),
      zone,
      screen.getByTestId('ww-dock'),
    ]);
    expect(zone).toContainElement(screen.getByTestId('ww-pane'));
    expect(root).toHaveClass('flex', 'h-full', 'min-h-0', 'flex-col');
    expect(zone).toHaveClass('min-h-0', 'flex-1');
  });

  // The canvas always has the full zone, so the Mermaid expand dialog has no
  // twin. parity:graph-expand-modal
  it('gives the canvas the whole zone between the bar and the dock', () => {
    renderAt('/dags/x');
    const zone = screen.getByTestId('ww-zone-main');
    expect(zone.previousElementSibling).toBe(screen.getByTestId('ww-bar'));
    expect(zone.nextElementSibling).toBe(screen.getByTestId('ww-dock'));
    expect(zone).toHaveClass('flex', 'flex-col', 'flex-1', 'min-h-0');

    // Every box from the zone down to the canvas takes the free height.
    const boxes: string[] = [];
    let box = screen.getByTestId('ww-canvas').parentElement;
    while (box && box !== zone) {
      expect(box).toHaveClass('flex', 'flex-col', 'flex-1', 'min-h-0');
      boxes.push(box.dataset.testid ?? '');
      box = box.parentElement;
    }
    expect(box).toBe(zone);
    expect(boxes).toEqual(['ww-pane']);
  });

  it('hides the minimap of the canvas zone below md', () => {
    renderAt('/dags/x');
    expect(screen.getByTestId('ww-zone-main')).toHaveClass(
      'max-md:[&_[data-testid=flow-minimap]]:hidden'
    );
  });
});

// parity:shell-dag-context parity:overview-workflow-embed
describe('WorkflowWorkspace contexts', () => {
  it('gives the body the DAG name and the run controls of the page', () => {
    renderAt('/dags/x');
    const canvas = screen.getByTestId('ww-canvas');
    expect(canvas).toHaveAttribute('data-ctx-name', 'x');
    expect(canvas).toHaveAttribute('data-ctx-file-name', 'x');
    expect(canvas).toHaveAttribute('data-ctx-auto-open', 'true');
    expect(canvas).toHaveAttribute('data-ctx-force-enqueue', 'true');
    expect(canvas).toHaveAttribute('data-ctx-on-enqueue', 'function');
  });

  // Only a RightPanelProvider closes one right-side panel for another.
  it('makes the right-side panels of the body exclusive', () => {
    renderAt('/dags/x');
    fireEvent.click(screen.getByRole('button', { name: 'claim ndv' }));
    expect(ndvEvicted).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'claim yaml' }));
    expect(ndvEvicted).toHaveBeenCalledTimes(1);
  });

  it('refreshes the DAG and the pinned run from DAGContext.refresh', async () => {
    const refreshDag = vi.fn(async () => undefined);
    renderAt('/dags/x?run=r1', refreshDag);
    await waitFor(() => expect(count(RUN_PATH)).toBe(1));

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'context refresh' }));
    });

    expect(refreshDag).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(count(RUN_PATH)).toBe(2));
    expect(parentRefresh).not.toHaveBeenCalled();
  });

  it('refreshes through the same path from the bar button', async () => {
    const refreshDag = vi.fn(async () => undefined);
    renderAt('/dags/x?run=r1', refreshDag);
    await waitFor(() => expect(count(RUN_PATH)).toBe(1));

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    });

    expect(refreshDag).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(count(RUN_PATH)).toBe(2));
  });
});

// parity:api-dag-management
describe('WorkflowWorkspace DAG management', () => {
  it('offers the pin, Rename and Delete in the bar, with no menu', async () => {
    renderAt('/dags/x');
    expect(
      screen.getByRole('button', { name: 'Pin workflow x' })
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More actions' })).toBeNull();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Rename' }));
    expect(
      await screen.findByRole('dialog', { name: 'Rename workflow' })
    ).toBeInTheDocument();
  });

  it('offers Delete in the bar', () => {
    renderAt('/dags/x');
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });
});
