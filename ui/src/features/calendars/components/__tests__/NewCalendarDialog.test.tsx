// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NewCalendarDialog } from '../NewCalendarDialog';

const post = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: post }),
}));

function renderDialog(onCreated = vi.fn()) {
  render(
    <NewCalendarDialog
      open
      remoteNode="local"
      existingNames={['ops']}
      onClose={vi.fn()}
      onCreated={onCreated}
    />
  );
  return onCreated;
}

function submit(name: string) {
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: name } });
  fireEvent.click(screen.getByRole('button', { name: 'Create' }));
}

describe('NewCalendarDialog', () => {
  beforeEach(() => {
    post.mockReset();
  });

  it.each([
    ['bad name', 'Use letters, digits, - and _ only.'],
    ['weekdays', 'weekdays is reserved.'],
    ['OPS', 'A calendar named OPS already exists.'],
  ])('refuses %s before asking the server', (name, problem) => {
    renderDialog();
    submit(name);
    expect(screen.getByText(problem)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it('lets the user try again after the request fails', async () => {
    post.mockRejectedValueOnce(new Error('timeout'));
    renderDialog();

    submit('jp-banking');

    expect(
      await screen.findByText('Failed to create the calendar.')
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Name'), {
      target: { value: 'jp-banking' },
    });
    expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled();
  });

  // Reopening the dialog must not allow a second request while the first
  // runs, and a request that ends after the dialog closed changes nothing.
  it('stays busy across reopening and ignores a request it no longer shows', async () => {
    let answer: (value: unknown) => void = () => undefined;
    post.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const onCreated = vi.fn();
    const props = {
      remoteNode: 'local',
      existingNames: [],
      onClose: vi.fn(),
      onCreated,
    };
    const { rerender } = render(<NewCalendarDialog open {...props} />);
    submit('jp-banking');

    rerender(<NewCalendarDialog open={false} {...props} />);
    rerender(<NewCalendarDialog open {...props} />);
    expect(screen.getByRole('button', { name: 'Creating...' })).toBeDisabled();

    rerender(<NewCalendarDialog open={false} {...props} />);
    await act(async () => answer({ data: { name: 'jp-banking' } }));
    expect(onCreated).not.toHaveBeenCalled();
  });

  it('ignores a request that ends after the page closed', async () => {
    let answer: (value: unknown) => void = () => undefined;
    post.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const onCreated = vi.fn();
    const { unmount } = render(
      <NewCalendarDialog
        open
        remoteNode="local"
        existingNames={[]}
        onClose={vi.fn()}
        onCreated={onCreated}
      />
    );
    submit('jp-banking');

    unmount();
    await act(async () => answer({ data: { name: 'jp-banking' } }));
    expect(onCreated).not.toHaveBeenCalled();
  });

  it('creates the calendar and hands over its name', async () => {
    post.mockResolvedValue({ data: { name: 'jp-banking' } });
    const onCreated = renderDialog();

    submit('jp-banking');

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('jp-banking'));
    expect(post).toHaveBeenCalledWith('/calendars', {
      params: { query: { remoteNode: 'local' } },
      body: { name: 'jp-banking' },
    });
  });
});
