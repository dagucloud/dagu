// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { SessionSidebar } from '../SessionSidebar';
import type { SessionWithState } from '../../types';
import { formatDate } from '../../utils/formatDate';

function makeSession(
  id: string,
  createdAt: string,
  updatedAt: string,
  title = `Session ${id}`
): SessionWithState {
  return {
    session: {
      id,
      user_id: 'user-1',
      title,
      created_at: createdAt,
      updated_at: updatedAt,
    },
    working: false,
    has_pending_prompt: false,
    model: 'gpt-test',
    total_cost: 0,
  };
}

describe('SessionSidebar', () => {
  it('shows loaded sessions newest-first by last update time', () => {
    const olderUpdate = makeSession(
      'older-update',
      '2026-05-08T10:02:00+09:00',
      '2026-05-08T10:03:00+09:00'
    );
    const newerUpdate = makeSession(
      'newer-update',
      '2026-05-08T09:49:00+09:00',
      '2026-05-08T10:05:00+09:00',
      'why does the briefing workflow miss Tokyo events?'
    );

    render(
      <SessionSidebar
        isOpen={true}
        sessions={[olderUpdate, newerUpdate]}
        activeSessionId={null}
        onSelectSession={vi.fn()}
        onClose={vi.fn()}
        onLoadMore={vi.fn()}
        hasMore={false}
      />
    );

    const labels = screen
      .getAllByRole('button')
      .map((button) => button.textContent?.trim());

    expect(labels[0]).toContain(
      'why does the briefing workflow miss Tokyo events?'
    );
    expect(labels[0]).toContain(formatDate(newerUpdate.session.updated_at));
    expect(labels[1]).toContain(formatDate(olderUpdate.session.updated_at));
    expect(labels).not.toContain(formatDate(newerUpdate.session.created_at));
  });
});
