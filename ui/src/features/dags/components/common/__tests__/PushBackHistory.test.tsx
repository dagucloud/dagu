// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import PushBackHistory from '../PushBackHistory';

describe('PushBackHistory', () => {
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
    await user.hover(subject);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Subject ID: user-2'
    );
  });
});
