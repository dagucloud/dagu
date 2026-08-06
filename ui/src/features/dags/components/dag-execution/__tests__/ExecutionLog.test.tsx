// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';
import { ActivityLine } from '../ActivityLine';

describe('ActivityLine', () => {
  it('exposes its source line for jump navigation and wraps messages', () => {
    const { container, rerender } = render(
      <ActivityLine
        line={{
          timestamp: '2026-08-06T12:00:00Z',
          level: 'INFO',
          message: 'structured-message',
          structured: true,
        }}
        lineNumber={42}
      />
    );

    expect(container.firstChild).toHaveAttribute('data-line-number', '42');
    expect(screen.getByText('structured-message').parentElement).toHaveClass(
      'whitespace-normal',
      'break-words'
    );

    rerender(
      <ActivityLine
        line={{ message: 'plain-message', structured: false }}
        lineNumber={43}
      />
    );

    expect(container.firstChild).toHaveAttribute('data-line-number', '43');
    expect(container.firstChild).toHaveClass(
      'whitespace-normal',
      'break-words'
    );
  });
});
