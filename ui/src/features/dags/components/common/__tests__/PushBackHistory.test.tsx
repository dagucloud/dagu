// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import dayjs from '@/lib/dayjs';
import PushBackHistory from '../PushBackHistory';

describe('PushBackHistory', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows when each push-back happened relative to now', () => {
    // Only Date is faked so the label is stable without stalling timers.
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-07-22T01:05:00Z'));
    const at = '2026-07-22T01:00:00Z';

    render(<PushBackHistory history={[{ iteration: 1, by: 'bob', at }]} />);

    expect(screen.getByText('5 minutes ago')).toHaveAttribute(
      'title',
      dayjs(at).format('YYYY-MM-DD HH:mm:ss')
    );
  });

  it('shows the subject ID for a push-back entry', async () => {
    const user = userEvent.setup();

    render(
      <PushBackHistory
        history={[
          {
            iteration: 1,
            by: 'bob',
            byId: 'user-2',
            at: '2026-07-22T01:00:00Z',
          },
        ]}
      />
    );

    const subject = screen.getByText('bob');
    await user.tab();
    expect(subject).toHaveFocus();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Subject ID: user-2'
    );
  });
});
