// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { CompactCount } from '../compact-count';
import { RetryCount } from '../retry-count';

// A name on a plain span is not exposed reliably, so the counts are images
// with their whole meaning as the name.
describe('count labels', () => {
  it('names the retry count for assistive technology', () => {
    render(<RetryCount count={3} />);
    expect(screen.getByRole('img', { name: 'Retried 3×' })).toHaveTextContent(
      '3'
    );
  });

  it('names the whole count of a compact count', () => {
    render(<CompactCount value={1234567} />);
    expect(screen.getByRole('img', { name: '1,234,567' })).toHaveTextContent(
      '1.2M'
    );
  });
});
