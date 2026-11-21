// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { SkillUsage } from '../SkillUsage';

describe('SkillUsage', () => {
  it('names the with block that the copy button copies', async () => {
    const user = userEvent.setup();
    render(<SkillUsage name="triage" />);

    await user.click(
      screen.getByRole('button', { name: 'Copy the with block' })
    );
    await waitFor(async () =>
      expect(await navigator.clipboard.readText()).toBe(
        'with:\n  skills: [triage]\n'
      )
    );
  });
});
