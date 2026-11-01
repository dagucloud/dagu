// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { DialogLayerProvider } from '@/components/ui/dialog';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import { dispatchHandledEscape } from '@/test/keyboard';
import { SubRunStackModal } from '../SubRunStackModal';
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
