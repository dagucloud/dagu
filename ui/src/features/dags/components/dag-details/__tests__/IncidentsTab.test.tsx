// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import IncidentsTab from '../IncidentsTab';

const state = vi.hoisted(() => ({
  licensed: true,
  policySet: undefined as unknown,
}));

const putMock = vi.fn();
const deleteMock = vi.fn();
const mutateMock = vi.fn();

const providers = {
  providers: [
    { id: 'pd-1', name: 'PagerDuty Ops', type: 'pagerduty', enabled: true },
  ],
};

vi.mock('@/hooks/useLicense', () => ({
  useLicense: () =>
    state.licensed
      ? { valid: true, plan: 'pro', community: false, features: [] }
      : { valid: false, plan: '', community: true, features: [] },
}));

vi.mock('@/components/LicenseFeaturePrompt', () => ({
  LicenseFeaturePrompt: ({ feature }: { feature: string }) => (
    <div data-testid="license-prompt">{feature}</div>
  ),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

// Stable objects: the tab resets its draft when the query data changes.
vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PUT: putMock, DELETE: deleteMock }),
  useQuery: (path: string, init: unknown) => {
    if (init === null) return { data: undefined, mutate: mutateMock };
    if (path === '/incident-providers') {
      return { data: providers, isLoading: false, mutate: mutateMock };
    }
    return { data: state.policySet, isLoading: false, mutate: mutateMock };
  },
}));

function renderTab(onDirtyChange = vi.fn()) {
  render(
    <MemoryRouter>
      <IncidentsTab
        fileName="example"
        workspaceName="ops"
        onDirtyChange={onDirtyChange}
      />
    </MemoryRouter>
  );
  return onDirtyChange;
}

const savedPolicySet = {
  id: 'policy-set-1',
  scope: 'dag',
  dagName: 'example',
  enabled: false,
  inheritParent: false,
  policies: [],
};

beforeEach(() => {
  vi.clearAllMocks();
  state.licensed = true;
  state.policySet = undefined;
  putMock.mockResolvedValue({ data: savedPolicySet });
  deleteMock.mockResolvedValue({});
  mutateMock.mockResolvedValue(undefined);
});

// parity:test-incidents-tab-untested
describe('IncidentsTab', () => {
  // parity:incidents-license
  it('shows the license prompt without a license', () => {
    state.licensed = false;
    renderTab();

    expect(screen.getByTestId('license-prompt')).toHaveTextContent('incidents');
    expect(screen.queryByText('Workflow incidents')).toBeNull();
  });

  // parity:incidents-header parity:dup-19
  // The tab sits in the Workflow panel (360 to 560 px) in a window of any
  // width, so the header row follows the width of its own box.
  it('stacks the header by container width, not by window width', () => {
    const { container } = render(
      <MemoryRouter>
        <IncidentsTab fileName="example" workspaceName="ops" />
      </MemoryRouter>
    );

    const row = screen.getByText('Workflow incidents').closest('h2')!
      .parentElement!.parentElement!;
    expect(row).toHaveClass('flex-col', '@3xl:flex-row');
    expect(row.className).not.toMatch(/(^|\s)(sm|md|lg|xl):/);
    expect(container.firstElementChild).toHaveClass('@container');
    expect(row.children[1]).toHaveClass('flex-wrap', 'min-w-0');
  });

  it('inherits the workspace routing until an override is configured', async () => {
    const user = userEvent.setup();
    const onDirtyChange = renderTab();

    expect(screen.getByText('Workflow incidents')).toBeVisible();
    expect(
      screen.getByText(/uses workspace ops routing unless you set/)
    ).toBeVisible();
    expect(screen.getByText('Parent routing is active.')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Save changes' })).toBeNull();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);

    await user.click(
      screen.getByRole('button', { name: 'Configure workflow override' })
    );

    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeVisible();
    expect(
      screen.getByRole('button', { name: 'Reset to inherit' })
    ).toBeVisible();

    // Reset of an override that was never saved stays local.
    await user.click(screen.getByRole('button', { name: 'Reset to inherit' }));
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(deleteMock).not.toHaveBeenCalled();
    expect(
      screen.getByRole('button', { name: 'Configure workflow override' })
    ).toBeVisible();
  });

  // parity:incidents-mode parity:incidents-policies
  it('is dirty after an edit and clean after a save', async () => {
    const user = userEvent.setup();
    const onDirtyChange = renderTab();
    const modes = screen.getByRole('group', { name: 'Incident routing mode' });

    await user.click(
      within(modes).getByRole('button', { name: 'Send incidents' })
    );
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    expect(screen.getByText('Send Incidents To')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Add connection' }));
    expect(screen.getByText('PagerDuty Ops')).toBeVisible();

    await user.click(within(modes).getByRole('button', { name: 'Off' }));
    expect(screen.getByText('No new incidents from this scope.')).toBeVisible();

    await user.click(screen.getByRole('button', { name: 'Save changes' }));

    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(false));
    expect(putMock).toHaveBeenCalledWith('/dags/{fileName}/incidents', {
      params: { path: { fileName: 'example' }, query: { remoteNode: 'local' } },
      body: { enabled: false, inheritParent: false, policies: [] },
    });
    expect(mutateMock).toHaveBeenCalled();
  });

  // The draft is read when Save is pressed and replaced by the saved routing
  // after it: an edit in between would be lost.
  it('locks the editor until the save completes', async () => {
    let finishPut: (value: unknown) => void = () => {};
    putMock.mockReturnValue(new Promise((resolve) => (finishPut = resolve)));
    const user = userEvent.setup();
    renderTab();
    await user.click(
      screen.getByRole('button', { name: 'Configure workflow override' })
    );
    const modes = screen.getByRole('group', { name: 'Incident routing mode' });

    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(within(modes).getByRole('button', { name: 'Off' })).toBeDisabled();

    finishPut({ data: savedPolicySet });
    await waitFor(() =>
      expect(within(modes).getByRole('button', { name: 'Off' })).toBeEnabled()
    );
  });

  it('deletes a saved override after confirmation', async () => {
    state.policySet = savedPolicySet;
    const user = userEvent.setup();
    const onDirtyChange = renderTab();

    await user.click(screen.getByRole('button', { name: 'Reset to inherit' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Reset' }));

    await waitFor(() =>
      expect(deleteMock).toHaveBeenCalledWith('/dags/{fileName}/incidents', {
        params: {
          path: { fileName: 'example' },
          query: { remoteNode: 'local' },
        },
      })
    );
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });
});
