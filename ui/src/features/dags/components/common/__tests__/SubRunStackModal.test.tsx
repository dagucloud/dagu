// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DialogLayerProvider } from '@/components/ui/dialog';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import { dispatchHandledEscape } from '@/test/keyboard';
import { SUB_RUN_WIDTH_KEY } from '@/features/peek/OverlayResizeHandle';
import {
  SUB_RUN_STACK_EXPANDED_KEY,
  SubRunStackModal,
  useOpenSubRun,
} from '../SubRunStackModal';
import DAGRunDetailsContent from '@/features/dag-runs/components/dag-run-details/DAGRunDetailsContent';
import { escFree } from '@/features/workflow-workspace/canvas/escFree';

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));

vi.mock(
  '@/features/dag-runs/components/dag-run-details/DAGRunDetailsContent',
  () => ({ default: vi.fn(() => null) })
);

function mockRun(data: unknown) {
  vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
    data,
    isValidating: false,
    refresh: vi.fn(),
  } as unknown as ReturnType<typeof useBoundedDAGRunDetails>);
}

const stackProps = {
  rootName: 'root',
  rootDAGRunId: 'root-run',
  rootLabel: 'root',
  onChange: vi.fn(),
};

describe('SubRunStackModal', () => {
  afterEach(() => localStorage.clear());

  // The choice of the whole window stays for the next stack.
  it('covers the whole window from its toggle and keeps the choice', () => {
    mockRun(undefined);
    const stack = [{ name: 'child', dagRunId: 'child-run' }];
    const first = render(<SubRunStackModal {...stackProps} stack={stack} />);

    fireEvent.click(first.getByRole('button', { name: 'Maximize dialog' }));
    expect(localStorage.getItem(SUB_RUN_STACK_EXPANDED_KEY)).toBe('1');
    first.unmount();

    const next = render(<SubRunStackModal {...stackProps} stack={stack} />);
    fireEvent.click(next.getByRole('button', { name: 'Restore dialog' }));
    expect(localStorage.getItem(SUB_RUN_STACK_EXPANDED_KEY)).toBeNull();
  });

  // The workspace layers under the drawer leave Escape to it.
  it('is an open dialog named after its run', () => {
    mockRun(undefined);
    const view = render(
      <SubRunStackModal
        {...stackProps}
        stack={[{ name: 'child', dagRunId: 'child-run' }]}
      />
    );

    expect(view.getByRole('dialog', { name: 'child' })).toBeInTheDocument();
    expect(escFree(new KeyboardEvent('keydown', { key: 'Escape' }))).toBe(
      false
    );
  });

  // Over the whole window the stack has no edges: each level stays in the
  // breadcrumb as the way back to it.
  it('lists every level in the breadcrumb over the whole window', () => {
    localStorage.setItem(SUB_RUN_STACK_EXPANDED_KEY, '1');
    mockRun(undefined);
    const stack = ['a', 'b', 'c', 'd', 'e'].map((name) => ({
      name,
      dagRunId: `${name}-run`,
    }));
    const view = render(<SubRunStackModal {...stackProps} stack={stack} />);

    for (const name of ['a', 'b', 'c', 'd', 'e']) {
      expect(view.getByRole('button', { name })).toBeInTheDocument();
    }
    expect(view.queryByText(/more/)).toBeNull();
  });

  // The user sizes the panel by its left edge; the width stays for the next
  // stack.
  it('opens at the width that the user left, with an edge to resize it', () => {
    localStorage.setItem(SUB_RUN_WIDTH_KEY, '900');
    mockRun(undefined);
    render(
      <SubRunStackModal
        {...stackProps}
        stack={[{ name: 'child', dagRunId: 'child-run' }]}
      />
    );

    expect(screen.getByRole('dialog', { name: 'child' })).toHaveStyle({
      '--sub-run-width': '900px',
    });
    expect(
      screen.getByRole('separator', { name: 'Resize sub-run view' })
    ).toBeInTheDocument();
  });

  // A path of runs, as to a step that waits two calls down, opens in one go.
  it('opens a path of runs at once', () => {
    mockRun({ name: 'a', dagRunId: 'a-run' });
    const onChange = vi.fn();
    function OpenPath() {
      const open = useOpenSubRun();
      return (
        <button
          type="button"
          onClick={() => {
            open?.({ name: 'b', dagRunId: 'b-run' });
            open?.({ name: 'c', dagRunId: 'c-run', step: 'send' });
          }}
        >
          open path
        </button>
      );
    }
    const stack = [{ name: 'a', dagRunId: 'a-run' }];
    render(
      <SubRunStackModal
        {...stackProps}
        stack={stack}
        onChange={onChange}
        renderRun={() => <OpenPath />}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'open path' }));

    expect(onChange).toHaveBeenLastCalledWith([
      ...stack,
      { name: 'b', dagRunId: 'b-run' },
      { name: 'c', dagRunId: 'c-run', step: 'send' },
    ]);
  });

  it('shows the step that its level names', async () => {
    mockRun({ name: 'child', dagRunId: 'child-run' });
    render(
      <SubRunStackModal
        {...stackProps}
        stack={[{ name: 'child', dagRunId: 'child-run', step: 'deploy' }]}
      />
    );

    await vi.waitFor(() => {
      expect(vi.mocked(DAGRunDetailsContent)).toHaveBeenCalled();
    });
    expect(vi.mocked(DAGRunDetailsContent).mock.lastCall?.[0]).toMatchObject({
      dagRunId: 'child-run',
      initialStepName: 'deploy',
    });
  });

  it('pops a level on Escape unless a dialog inside it handled the key', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: undefined,
      isValidating: false,
      refresh: vi.fn(),
    } as unknown as ReturnType<typeof useBoundedDAGRunDetails>);
    const onChange = vi.fn();
    const stack = [
      { name: 'child', dagRunId: 'child-run' },
      { name: 'grandchild', dagRunId: 'grandchild-run' },
    ];
    render(
      <SubRunStackModal
        rootName="root"
        rootDAGRunId="root-run"
        rootLabel="root"
        stack={stack}
        onChange={onChange}
      />
    );

    dispatchHandledEscape();
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onChange).toHaveBeenCalledWith(stack.slice(0, 1));
  });

  // The drawer must paint above the workspace node view (z-[60]).
  it('sits above the node view in a raised layer', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: undefined,
      isValidating: false,
      refresh: vi.fn(),
    } as unknown as ReturnType<typeof useBoundedDAGRunDetails>);
    const props = {
      rootName: 'root',
      rootDAGRunId: 'root-run',
      rootLabel: 'root',
      stack: [{ name: 'child', dagRunId: 'child-run' }],
      onChange: vi.fn(),
    };
    const plain = render(<SubRunStackModal {...props} />);
    expect(plain.container.querySelector('.z-50')).not.toBeNull();
    plain.unmount();
    const raised = render(
      <DialogLayerProvider value="raised">
        <SubRunStackModal {...props} />
      </DialogLayerProvider>
    );
    expect(raised.container.querySelector('.z-50')).toBeNull();
    expect(raised.container.querySelector('.z-\\[68\\]')).not.toBeNull();
  });
});
