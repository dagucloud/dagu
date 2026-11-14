// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PanelMessage } from '../panel-message';

describe('PanelMessage', () => {
  it('shows an error as an alert in the destructive token', () => {
    render(
      <PanelMessage tone="error" align="start">
        Could not load the step
      </PanelMessage>
    );
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Could not load the step');
    expect(alert).toHaveClass('text-destructive', 'text-start');
  });

  it('shows a muted message without a role', () => {
    render(
      <PanelMessage tone="muted" align="center">
        No run yet
      </PanelMessage>
    );
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByText('No run yet')).toHaveClass(
      'text-muted-foreground',
      'text-center'
    );
  });
});
