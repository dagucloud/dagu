// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import { findJsxLiterals } from '@/testing/jsxLiterals';
import type {
  ApiDAGDetails,
  ApiDAGRunDetails,
  BarSlotProps,
  DAGPageData,
} from '../../contracts';
import { WorkflowBar } from '../WorkflowBar';

const mocks = vi.hoisted(() => ({
  refresh: vi.fn(),
  details: null as ApiDAGRunDetails | null,
  hasRun: false,
  setTab: vi.fn(),
  select: vi.fn(),
  stale: false,
  // The shown run is not the latest one.
  pinned: false,
}));

vi.mock('../../data', () => ({
  useWorkspaceRefresh: () => mocks.refresh,
  useWorkspaceRun: () => ({
    details: mocks.details,
    hasRun: mocks.hasRun,
    selection: mocks.pinned ? { runId: 'run-9' } : 'latest',
    ref: mocks.details ? { name: 'etl', runId: mocks.details.dagRunId } : null,
    select: mocks.select,
  }),
}));
vi.mock('@/features/visual-editor/run/useRunSpecStale', () => ({
  useRunSpecStale: () => mocks.stale,
  useRunSpecDiff: () => null,
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ({ setTab: mocks.setTab }),
  useDockCommands: () => ({ setTab: mocks.setTab }),
}));

// The slots that other files own render their props only.
function slotStub(testId: string) {
  return (p: BarSlotProps & { pinned?: boolean }) => (
    <span
      data-testid={testId}
      data-file-name={p.fileName}
      data-dag-name={p.dag.name}
      data-workspace={p.workspaceName ?? ''}
      data-pinned={String(p.pinned)}
    />
  );
}
vi.mock('../DAGIdentity', () => ({ DAGIdentity: slotStub('identity') }));
vi.mock('../LabelChips', () => ({ LabelChips: slotStub('labels') }));
vi.mock('../TriggerChip', () => ({ TriggerChip: slotStub('trigger') }));
vi.mock('../RunChip', () => ({ RunChip: slotStub('run-chip') }));
vi.mock('../RunActionsCluster', () => ({
  RunActionsCluster: slotStub('run-actions'),
}));
vi.mock('../RunSplitButton', () => ({ RunSplitButton: slotStub('run-split') }));
vi.mock('../WorkflowEditButtons', () => ({
  WorkflowDeleteButton: slotStub('edit'),
}));

const dagData: DAGPageData = {
  dag: { name: 'etl' } as ApiDAGDetails,
  localDags: [],
  latestDAGRun: {} as ApiDAGRunDetails,
  suspended: false,
  pinned: true,
  errors: [],
};

function Probe() {
  const location = useLocation();
  const navigationType = useNavigationType();
  return (
    <output data-testid="location" data-navigation={navigationType}>
      {location.pathname + location.search}
    </output>
  );
}

function renderBar(url = '/dags/etl', data: DAGPageData = dagData) {
  const element = (
    <>
      <Probe />
      <WorkflowBar fileName="etl" dagData={data} workspaceName="ops" />
    </>
  );
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/dags/:fileName" element={element} />
        <Route path="/dags/:fileName/:tab" element={element} />
      </Routes>
    </MemoryRouter>
  );
}

beforeEach(() => {
  mocks.refresh.mockReset().mockResolvedValue(undefined);
  mocks.details = null;
  mocks.hasRun = false;
  mocks.setTab.mockReset();
  mocks.select.mockReset();
  mocks.stale = false;
  mocks.pinned = false;
});

afterEach(() => {
  vi.useRealTimers();
});

const failed = {
  dagRunId: 'run-9',
  status: Status.Failed,
  statusLabel: StatusLabel.failed,
  startedAt: new Date(Date.now() - 3 * 60_000).toISOString(),
  nodes: [],
} as unknown as ApiDAGRunDetails;

function withRun() {
  mocks.details = failed;
  mocks.hasRun = true;
}

// parity:test-dag-header
describe('WorkflowBar', () => {
  it('mounts every slot with the bar slot props', () => {
    withRun();
    renderBar('/dags/etl');
    for (const id of [
      'identity',
      'labels',
      'trigger',
      'run-chip',
      'run-actions',
      'run-split',
      'edit',
    ]) {
      const slot = screen.getByTestId(id);
      expect(slot).toHaveAttribute('data-file-name', 'etl');
      expect(slot).toHaveAttribute('data-dag-name', 'etl');
      expect(slot).toHaveAttribute('data-workspace', 'ops');
    }
    expect(screen.getByTestId('identity')).toHaveAttribute(
      'data-pinned',
      'true'
    );
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeInTheDocument();
  });

  // One tab: the bar has no mode switch and no link to the run page. The
  // tab bar rows of the checklist end here.
  // parity:tabs-row parity:shell-tab-bar
  it('has no tab named Editor or Executions', () => {
    withRun();
    renderBar('/dags/etl');
    expect(screen.queryByTestId('ww-mode-tabs')).toBeNull();
    for (const name of ['Editor', 'Executions']) {
      expect(screen.queryByRole('link', { name })).toBeNull();
      expect(screen.queryByRole('tab', { name })).toBeNull();
    }
    expect(screen.queryByTestId('ww-bar-last-run')).toBeNull();
  });

  // The name stays Refresh; the tooltip says what the button reloads.
  it('explains the Refresh button in its tooltip', () => {
    renderBar();
    expect(screen.getByRole('button', { name: 'Refresh' })).toHaveAttribute(
      'title',
      'Reload the workflow, its runs and the latest run from the server'
    );
  });

  // The chip and the run icons follow the trigger chip; the compact identity
  // takes the place of the chip below a bar of 1024px.
  it('keeps the left-to-right order of the bar', () => {
    withRun();
    renderBar('/dags/etl');
    const order = [
      screen.getByTestId('identity'),
      screen.getByTestId('labels'),
      screen.getByTestId('trigger'),
      screen.getByTestId('run-chip'),
      screen.getByTestId('ww-bar-status-dot'),
      screen.getByTestId('run-actions'),
      screen.getByTestId('run-split'),
      screen.getByRole('button', { name: 'Refresh' }),
      screen.getByTestId('edit'),
    ];
    for (let i = 1; i < order.length; i++) {
      expect(
        order[i - 1]!.compareDocumentPosition(order[i]!) &
          Node.DOCUMENT_POSITION_FOLLOWING
      ).toBeTruthy();
    }
  });

  // The 390px screenshot is taken elsewhere; here the classes say what hides.
  // The run icons stay at every width: no menu holds them.
  it('hides the secondary items below md', () => {
    withRun();
    renderBar('/dags/etl');
    expect(screen.getByTestId('trigger').parentElement).toHaveClass(
      'hidden',
      'md:not-empty:flex'
    );
    expect(
      screen.getByRole('button', { name: 'Refresh' }).parentElement
    ).toHaveClass('hidden', 'md:flex');
    expect(screen.getByTestId('run-actions').parentElement).not.toHaveClass(
      'hidden'
    );
    for (const id of ['identity', 'run-split', 'edit']) {
      expect(screen.getByTestId(id).parentElement).not.toHaveClass('hidden');
    }
  });

  // From md up the width of the bar decides (the side bar takes a part of
  // the window): the run chip needs a bar of 1024px. The labels need 672px,
  // so that they show on a laptop; the bar hides them below it.
  it('shows the run chip from a bar of 1024px', () => {
    withRun();
    renderBar('/dags/etl');
    const slot = screen.getByTestId('run-chip').parentElement;
    expect(slot).toHaveClass('hidden', '@5xl:flex');
    expect(slot).not.toHaveClass('md:flex');
  });

  // At a bar under 1280px the trigger chip is icons only: it does not shrink,
  // so that the icons do not clip by a pixel. From 1280px the text may shrink.
  it('keeps the trigger icons whole below a bar of 1280px', () => {
    renderBar('/dags/etl');
    const slot = screen.getByTestId('trigger').parentElement;
    expect(slot).toHaveClass('hidden', 'md:not-empty:flex', 'shrink-0');
    expect(slot).toHaveClass('@7xl:min-w-0', '@7xl:shrink');
    expect(slot).not.toHaveClass('min-w-0');
  });

  it('shows the labels from a bar of 672px', () => {
    renderBar('/dags/etl');
    const slot = screen.getByTestId('labels').parentElement;
    // The box shows only with labels in it: it leaves no gap when empty.
    expect(slot).toHaveClass('hidden', '@2xl:not-empty:flex');
    expect(slot).not.toHaveClass('md:flex');
    expect(slot).not.toHaveClass('@5xl:flex');
  });

  // The '...' menu is gone: Rename and Delete are buttons of the bar, and
  // no entry of the bar leads to the run page.
  it('has no menu and no run page entry', () => {
    withRun();
    renderBar('/dags/etl');
    expect(screen.queryByTestId('ww-workflow-menu')).toBeNull();
    expect(screen.queryByRole('button', { name: 'More actions' })).toBeNull();
    expect(screen.queryByText('Open run page')).toBeNull();
    expect(screen.queryByText('Open in editor')).toBeNull();
    expect(screen.queryByRole('link', { name: /run page/i })).toBeNull();
  });

  it('keeps the bar to one row of 40px', () => {
    renderBar();
    const bar = screen.getByTestId('ww-bar');
    expect(bar).toHaveClass('@container', 'flex', 'h-10', 'items-center');
    expect(bar).not.toHaveClass('flex-wrap');
    expect(bar).not.toHaveClass('py-1.5');
  });

  // The slot never gets less than the smallest chip. The run facts are in
  // the tooltip of the chip, so no meta line needs the width of the bar.
  it('keeps the smallest width of the run chip', () => {
    withRun();
    renderBar('/dags/etl');
    const slot = screen.getByTestId('run-chip').parentElement!;
    expect(slot).toHaveClass('min-w-min', 'whitespace-nowrap');
    expect(slot).not.toHaveClass('min-w-0');
    expect(slot.parentElement).toBe(screen.getByTestId('ww-bar-run'));
    // No text of the run identity wraps, so that its smallest width is its
    // width: the run icons are never cut when a long name fills the bar.
    expect(screen.getByTestId('ww-bar-run')).toHaveClass('whitespace-nowrap');
    expect(slot.parentElement!.parentElement).toBe(
      screen.getByTestId('ww-bar')
    );
  });

  it('renders nothing without a DAG', () => {
    renderBar('/dags/etl', { ...dagData, dag: undefined });
    expect(screen.queryByTestId('ww-bar')).not.toBeInTheDocument();
  });
});

// One tab: the run chip shows in every mode, whenever the workflow has a run.
// parity:header-last-run-group
describe('WorkflowBar run identity', () => {
  it('shows the run chip on the editor path', () => {
    withRun();
    renderBar('/dags/etl');
    expect(screen.getByTestId('run-chip')).toBeInTheDocument();
    expect(screen.getByTestId('run-actions')).toBeInTheDocument();
  });

  it('shows the run chip on the legacy Executions path too', () => {
    withRun();
    renderBar('/dags/etl/executions');
    expect(screen.getByTestId('run-chip')).toBeInTheDocument();
  });

  it('shows no run chip and no status dot for a workflow that never ran', () => {
    renderBar('/dags/etl');
    expect(screen.queryByTestId('run-chip')).toBeNull();
    expect(screen.queryByTestId('ww-bar-run-chip')).toBeNull();
    expect(screen.queryByTestId('ww-bar-status-dot')).toBeNull();
    // The run split button and the delete button stay.
    expect(screen.getByTestId('run-split')).toBeInTheDocument();
    expect(screen.getByTestId('edit')).toBeInTheDocument();
  });

  // Below a bar of 1024px the status dot, the age of the run and a pin when
  // ?run is set stand for the chip. A click opens the dock on the timeline.
  describe('narrow bar', () => {
    it('shows the status dot and the age of the run, no pin without ?run', () => {
      withRun();
      renderBar('/dags/etl');
      const dot = screen.getByRole('button', { name: 'Run status: Failed' });
      expect(dot).toHaveAttribute('data-testid', 'ww-bar-status-dot');
      // The whole compact identity hides from a bar of 1024px.
      expect(screen.getByTestId('ww-bar-compact-run')).toHaveClass(
        '@5xl:hidden'
      );
      expect(dot).not.toHaveClass('hidden');
      expect(dot).toHaveTextContent('3m');
      expect(dot.querySelector('svg')).toBeNull();
    });

    // The URL always names the shown run: a ?run of the latest run is no pin.
    it('adds no pin for the latest run, also with ?run in the URL', () => {
      withRun();
      renderBar('/dags/etl?run=run-9');
      const dot = screen.getByRole('button', { name: 'Run status: Failed' });
      expect(dot.querySelector('svg')).toBeNull();
    });

    it('adds a pin when the shown run is not the latest', () => {
      withRun();
      mocks.pinned = true;
      renderBar('/dags/etl?run=run-9');
      const dot = screen.getByRole('button', {
        name: 'Run status: Failed. Pinned run',
      });
      const pin = dot.querySelector('svg');
      expect(pin).not.toBeNull();
      // From a bar of 896px the back-to-latest icon marks the pinned run.
      expect(pin!.parentElement).toHaveClass('@4xl:hidden');
    });

    it('opens the dock on the timeline', () => {
      withRun();
      renderBar('/dags/etl');
      fireEvent.click(screen.getByTestId('ww-bar-status-dot'));
      expect(mocks.setTab).toHaveBeenCalledWith('timeline', { user: true });
    });

    // At 1280px with the side bar open the bar is about 1040px: the compact
    // identity is what shows. It keeps the two ways that the full chip has:
    // the stale dot and the way back to the latest run.
    it('has a back-to-latest icon button while an older run is shown', () => {
      withRun();
      mocks.pinned = true;
      renderBar('/dags/etl?run=run-9');
      const compact = screen.getByTestId('ww-bar-compact-run');
      const back = within(compact).getByRole('button', {
        name: 'Back to latest',
      });
      expect(back.querySelector('svg')).not.toBeNull();
      expect(back).toHaveTextContent('');
      fireEvent.click(back);
      expect(mocks.select).toHaveBeenCalledWith('latest');
    });

    it('has no back-to-latest button for the latest run', () => {
      withRun();
      renderBar('/dags/etl');
      expect(
        screen.queryByRole('button', { name: 'Back to latest' })
      ).toBeNull();
    });

    it('has the stale dot that toggles ?def=run', () => {
      withRun();
      mocks.stale = true;
      renderBar('/dags/etl?run=run-9');
      const compact = screen.getByTestId('ww-bar-compact-run');
      const dot = within(compact).getByTestId('ww-bar-compact-stale-dot');
      expect(dot).toHaveAttribute('aria-pressed', 'false');
      fireEvent.click(dot);
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dags/etl?run=run-9&def=run'
      );
      expect(
        within(compact).getByTestId('ww-bar-compact-stale-dot')
      ).toHaveAttribute('aria-pressed', 'true');
    });

    // A bar under 896px keeps the name and the identity (a window of 1024px
    // with the side bar open): the stale dot and the way back show from 896px,
    // where the compact identity has the room.
    it('shows the stale dot and the way back from a bar of 896px', () => {
      withRun();
      mocks.stale = true;
      mocks.pinned = true;
      renderBar('/dags/etl?run=run-9');
      const more = screen.getByTestId('ww-bar-compact-more');
      expect(more).toHaveClass('hidden', '@4xl:contents');
      expect(
        within(more).getByTestId('ww-bar-compact-stale-dot')
      ).toBeInTheDocument();
      expect(
        within(more).getByRole('button', { name: 'Back to latest' })
      ).toBeInTheDocument();
      expect(screen.getByTestId('ww-bar-status-dot').parentElement).not.toBe(
        more
      );
    });

    // The identity keeps its width: the stale dot, the way back and the pin
    // have a slot that is there, empty, while the part is absent.
    it('keeps the empty slots of the stale dot, the way back and the pin', () => {
      withRun();
      renderBar('/dags/etl');
      const compact = screen.getByTestId('ww-bar-compact-run');
      expect(
        within(compact).getByTestId('ww-bar-compact-stale-slot')
      ).toBeEmptyDOMElement();
      expect(
        within(compact).getByTestId('ww-bar-compact-back-slot')
      ).toBeEmptyDOMElement();
      expect(
        within(compact).getByTestId('ww-bar-compact-pin-slot')
      ).toBeEmptyDOMElement();
    });

    // The Run details button is the last item of the compact identity, in a
    // slot of 28px that the identity keeps.
    it('ends the compact identity with the Run details slot', () => {
      withRun();
      renderBar('/dags/etl');
      const compact = screen.getByTestId('ww-bar-compact-run');
      const slot = within(compact).getByTestId('ww-bar-compact-details-slot');
      expect(slot).toHaveClass('size-7', 'shrink-0');
      expect(compact.lastElementChild).toBe(slot);
    });

    // The bar keeps 40px and does not scroll sideways: the compact identity
    // is one row of icon buttons that never wraps.
    it('keeps the compact identity on one row', () => {
      withRun();
      mocks.stale = true;
      renderBar('/dags/etl?run=run-9');
      const compact = screen.getByTestId('ww-bar-compact-run');
      expect(compact).toHaveClass('flex', 'shrink-0', 'items-center');
      expect(compact).not.toHaveClass('flex-wrap');
    });

    it('keeps the dot next to the run icons', () => {
      withRun();
      renderBar('/dags/etl');
      const group = screen.getByTestId('ww-bar-run');
      expect(
        within(group).getByTestId('ww-bar-status-dot')
      ).toBeInTheDocument();
      expect(within(group).getByTestId('run-actions')).toBeInTheDocument();
    });
  });
});

// parity:shell-refresh-button parity:dup-20
describe('refresh button', () => {
  it('spins until the refresh settles plus 500 ms', async () => {
    vi.useFakeTimers();
    let settle!: () => void;
    mocks.refresh.mockReturnValue(
      new Promise<void>((resolve) => {
        settle = resolve;
      })
    );
    renderBar();
    const button = screen.getByRole('button', { name: 'Refresh' });
    const icon = () => button.querySelector('svg');

    await act(async () => {
      fireEvent.click(button);
    });
    expect(mocks.refresh).toHaveBeenCalledTimes(1);
    expect(button).toBeDisabled();
    expect(icon()).toHaveClass('animate-spin');

    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    expect(button).toBeDisabled();

    await act(async () => {
      settle();
    });
    await act(async () => {
      vi.advanceTimersByTime(499);
    });
    expect(button).toBeDisabled();
    expect(icon()).toHaveClass('animate-spin');

    await act(async () => {
      vi.advanceTimersByTime(1);
    });
    expect(button).toBeEnabled();
    expect(icon()).not.toHaveClass('animate-spin');
  });

  it('stops after a failed refresh too', async () => {
    vi.useFakeTimers();
    mocks.refresh.mockRejectedValue(new Error('down'));
    renderBar();
    const button = screen.getByRole('button', { name: 'Refresh' });
    await act(async () => {
      fireEvent.click(button);
    });
    await act(async () => {
      vi.advanceTimersByTime(500);
    });
    expect(button).toBeEnabled();
  });

  it('shows for a workflow that never ran', () => {
    renderBar('/dags/etl', {
      ...dagData,
      latestDAGRun: undefined as unknown as ApiDAGRunDetails,
    });
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeInTheDocument();
  });
});

// The files of the bar and of the layout that T33 owns.
const SHELL_FILES = [
  'features/workflow-workspace/WorkflowWorkspace.tsx',
  'features/workflow-workspace/layout/WorkspaceLayout.tsx',
  'features/workflow-workspace/bar/WorkflowBar.tsx',
  'features/workflow-workspace/bar/DAGIdentity.tsx',
  'features/workflow-workspace/bar/WorkflowEditButtons.tsx',
  'features/workflow-workspace/bar/TriggerChip.tsx',
  'features/workflow-workspace/bar/LabelChips.tsx',
];

describe('shell text', () => {
  it('writes no untranslated text in the JSX of the shell', () => {
    expect(findJsxLiterals(SHELL_FILES)).toEqual([]);
  });
});
