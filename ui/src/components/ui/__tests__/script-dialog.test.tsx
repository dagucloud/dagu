// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ScriptBadge } from '../script-dialog';

describe('ScriptBadge', () => {
  it('previews the first line and opens the full script', () => {
    render(
      <ScriptBadge
        script={'\n  echo "start"\n  make build\n  make test\n'}
        stepName="build"
      />
    );

    expect(screen.getByText('echo "start"')).toBeVisible();
    expect(screen.getByText('+2 lines')).toBeVisible();

    fireEvent.click(screen.getByRole('button'));

    expect(screen.getByRole('dialog')).toHaveTextContent('make test');
  });
});
