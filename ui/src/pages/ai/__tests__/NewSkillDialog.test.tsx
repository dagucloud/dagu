// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { NewSkillDialog } from '../NewSkillDialog';

function renderDialog(onCreate = vi.fn(async () => null)) {
  render(
    <NewSkillDialog
      existing={['triage']}
      onCreate={onCreate}
      onClose={() => {}}
    />
  );
  return onCreate;
}

const type = (value: string) =>
  fireEvent.change(screen.getByLabelText('Skill name'), {
    target: { value },
  });

describe('NewSkillDialog', () => {
  // The server accepts lower case letters of any script, and the name "new".
  it('creates a skill with a name that the server accepts', async () => {
    const onCreate = renderDialog();
    for (const name of ['données', 'new']) {
      type(name);
      expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled();
    }
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith('new'));
  });

  // A cancel cannot stop the write: the skill would appear after it.
  it('keeps Cancel disabled while the skill is created', async () => {
    renderDialog(vi.fn(() => new Promise<null>(() => {})));
    type('new');
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    );
  });

  it('refuses a name that breaks the rule or exists', () => {
    renderDialog();
    type('Données');
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled();
    expect(screen.getByLabelText('Skill name')).toHaveAttribute(
      'aria-invalid',
      'true'
    );
    type('triage');
    expect(
      screen.getByText('A skill named "triage" exists.')
    ).toBeInTheDocument();
  });
});
