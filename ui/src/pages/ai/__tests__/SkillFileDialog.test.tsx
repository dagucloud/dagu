// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SkillFileDialog } from '../SkillFileDialog';

describe('SkillFileDialog', () => {
  // A cancel cannot stop the write: the file would appear after it.
  it('keeps Cancel disabled while the file is written', async () => {
    render(
      <SkillFileDialog
        mode="add"
        exists={['SKILL.md']}
        onSubmit={vi.fn(() => new Promise<string | null>(() => {}))}
        onClose={() => {}}
      />
    );
    fireEvent.change(screen.getByLabelText('Path in the skill'), {
      target: { value: 'references/notes.md' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    );
  });
});
