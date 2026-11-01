// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { LOG_CHUNK_LINES, LogChunks } from '../LogChunks';

describe('LogChunks', () => {
  it('keeps every line in order across chunks the browser may skip', () => {
    const count = LOG_CHUNK_LINES * 2 + 5;
    const { container } = render(
      <LogChunks
        count={count}
        renderLine={(index) => <span key={index}>{`line ${index}`}</span>}
      />
    );

    const chunks = container.querySelectorAll<HTMLElement>('[data-log-chunk]');
    expect(chunks).toHaveLength(3);
    expect(chunks[2]!.children).toHaveLength(5);
    expect(chunks[0]!.style.contentVisibility).toBe('auto');
    const lines = container.querySelectorAll('span');
    expect(lines).toHaveLength(count);
    expect(lines[LOG_CHUNK_LINES]!.textContent).toBe(`line ${LOG_CHUNK_LINES}`);
  });
});
