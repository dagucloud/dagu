// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useCalendarPreview } from '../useCalendarPreview';

const { post, client } = vi.hoisted(() => {
  const post = vi.fn();
  return { post, client: { POST: post } };
});

// The app's client is a single instance, so the mock keeps one too.
vi.mock('@/hooks/api', () => ({
  useClient: () => client,
}));

const preview = {
  errors: [],
  warnings: [],
  year: 2027,
  today: '2027-01-04',
  days: [],
  changes: { weekdaysAdded: [], weekdaysRemoved: [], dates: [] },
};

function render(baseline: string) {
  return renderHook(
    (props: { baseline: string }) =>
      useCalendarPreview({
        name: 'ops',
        remoteNode: 'local',
        spec: 'holidays: [2027-01-11]\n',
        baseline: props.baseline,
        year: 2027,
      }),
    { initialProps: { baseline } }
  );
}

describe('useCalendarPreview', () => {
  beforeEach(() => {
    post.mockReset();
  });

  // Changes are marked against the saved content, so saving must bring a
  // fresh preview even though the edited content is the same.
  it('previews again when the saved content changes', async () => {
    post.mockResolvedValue({ data: preview });
    const { rerender } = render('holidays: []\n');
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

    rerender({ baseline: 'holidays: [2027-01-11]\n' });

    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
  });

  it('reports a failed request and tries again on retry', async () => {
    post.mockRejectedValueOnce(new Error('offline'));
    const { result } = render('holidays: []\n');
    await waitFor(() => expect(result.current.failed).toBe(true));

    post.mockResolvedValue({ data: preview });
    act(() => result.current.retry());

    await waitFor(() => expect(result.current.preview).toEqual(preview));
    expect(result.current.failed).toBe(false);
  });
});
