// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { SkillMeter } from '../SkillMeter';

describe('SkillMeter', () => {
  // The recommended limits appear only in the hints.
  it('opens the hint of each bar from the keyboard', async () => {
    render(<SkillMeter content={'---\nname: triage\n---\nBody\n'} />);
    const [tokens, lines] = screen.getAllByTestId('skill-meter-item');

    await userEvent.tab();
    expect(tokens).toHaveFocus();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'The format recommends 5,000 tokens or fewer.'
    );

    await userEvent.tab();
    expect(lines).toHaveFocus();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'The format recommends 500 lines or fewer.'
    );
    expect(lines).toHaveAccessibleDescription(
      'The format recommends 500 lines or fewer.'
    );
  });
});
