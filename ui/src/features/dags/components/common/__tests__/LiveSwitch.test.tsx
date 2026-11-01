// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import LiveSwitch from '../LiveSwitch';

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: true } }),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: vi.fn() }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));

describe('LiveSwitch', () => {
  it('names the switch after the workflow schedule', () => {
    render(
      <LiveSwitch
        dag={
          {
            fileName: 'nightly',
            suspended: false,
            dag: { name: 'nightly' },
          } as never
        }
      />
    );

    expect(
      screen.getByRole('switch', { name: 'Schedule for nightly' })
    ).toBeChecked();
  });
});
