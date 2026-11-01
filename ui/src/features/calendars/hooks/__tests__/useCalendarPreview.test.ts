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

  // Opening another calendar clears the content until it loads; the answer
  // for the previous content must not land under the new calendar.
  it('drops an answer for content that is gone', async () => {
    let answer: (value: unknown) => void = () => undefined;
    post.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const { result, rerender } = renderHook(
      (props: { spec: string | null }) =>
        useCalendarPreview({
          name: 'ops',
          remoteNode: 'local',
          spec: props.spec,
          baseline: null,
          year: 2027,
        }),
      { initialProps: { spec: 'holidays: []\n' as string | null } }
    );
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

    rerender({ spec: null });
    await act(async () => answer({ data: preview }));

    expect(result.current.preview).toBeNull();
    expect(result.current.isPreviewing).toBe(false);
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

  it('waits for a pause in typing while the first preview loads', async () => {
    post.mockReturnValue(new Promise(() => undefined));
    const { rerender } = renderHook(
      (props: { spec: string }) =>
        useCalendarPreview({
          name: 'ops',
          remoteNode: 'local',
          spec: props.spec,
          baseline: null,
          year: 2027,
        }),
      { initialProps: { spec: 'holidays: []\n' } }
    );
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

    rerender({ spec: 'holidays: [2027-01-1' });
    await act(() => new Promise((resolve) => setTimeout(resolve, 50)));

    expect(post).toHaveBeenCalledTimes(1);
  });

  it('clears a failure while it tries again', async () => {
    post.mockRejectedValueOnce(new Error('offline'));
    const { result } = render('holidays: []\n');
    await waitFor(() => expect(result.current.failed).toBe(true));

    post.mockReturnValue(new Promise(() => undefined));
    act(() => result.current.retry());

    expect(result.current.failed).toBe(false);
    expect(result.current.isPreviewing).toBe(true);
  });
});
