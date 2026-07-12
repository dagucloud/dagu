// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useSWRConfig } from 'swr';
import { isIgnorableQueryError, QueryFeedback } from '../QueryFeedback';

function FeedbackTriggers({ messages }: { messages: string[] }) {
  const { onError } = useSWRConfig();
  const reportError = onError as unknown as (error: unknown) => void;

  return messages.map((message, index) => (
    <button key={message} onClick={() => reportError(new Error(message))}>
      Trigger {index + 1}
    </button>
  ));
}

function renderFeedback(messages: string[]) {
  render(
    <QueryFeedback>
      <FeedbackTriggers messages={messages} />
    </QueryFeedback>
  );
}

describe('isIgnorableQueryError', () => {
  it('ignores aborted requests', () => {
    expect(isIgnorableQueryError({ name: 'AbortError' })).toBe(true);
    expect(isIgnorableQueryError({ name: 'RequestAbortError' })).toBe(true);
  });

  it('ignores 401 and 404 responses from fetch errors', () => {
    expect(isIgnorableQueryError({ response: { status: 401 } })).toBe(true);
    expect(isIgnorableQueryError({ status: 404 })).toBe(true);
  });

  it('ignores expected API error codes from parsed error bodies', () => {
    expect(
      isIgnorableQueryError({ code: 'not_found', message: 'DAG x not found' })
    ).toBe(true);
    expect(
      isIgnorableQueryError({ code: 'unauthorized', message: 'nope' })
    ).toBe(true);
    expect(
      isIgnorableQueryError({ code: 'auth.unauthorized', message: 'nope' })
    ).toBe(true);
  });

  it('reports other failures', () => {
    expect(isIgnorableQueryError({ status: 500, message: 'boom' })).toBe(false);
    expect(
      isIgnorableQueryError({ code: 'internal_error', message: 'boom' })
    ).toBe(false);
    expect(isIgnorableQueryError(new Error('network down'))).toBe(false);
  });
});

describe('QueryFeedback', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('suppresses duplicate notices during cooldown and expires notices', () => {
    renderFeedback(['network down']);
    const trigger = screen.getByRole('button', { name: 'Trigger 1' });

    fireEvent.click(trigger);
    fireEvent.click(trigger);
    expect(screen.getAllByText('network down')).toHaveLength(1);

    act(() => vi.advanceTimersByTime(6_000));
    expect(screen.queryByText('network down')).not.toBeInTheDocument();

    fireEvent.click(trigger);
    expect(screen.queryByText('network down')).not.toBeInTheDocument();

    act(() => vi.advanceTimersByTime(24_000));
    fireEvent.click(trigger);
    expect(screen.getByText('network down')).toBeInTheDocument();
  });

  it('keeps only the three newest notices', () => {
    renderFeedback(['first', 'second', 'third', 'fourth']);

    for (let index = 1; index <= 4; index++) {
      fireEvent.click(screen.getByRole('button', { name: `Trigger ${index}` }));
    }

    expect(screen.queryByText('first')).not.toBeInTheDocument();
    expect(screen.getByText('second')).toBeInTheDocument();
    expect(screen.getByText('third')).toBeInTheDocument();
    expect(screen.getByText('fourth')).toBeInTheDocument();
  });

  it('tracks browser connectivity changes', () => {
    renderFeedback([]);
    expect(
      screen.queryByText('You are offline — data may be stale')
    ).not.toBeInTheDocument();

    act(() => window.dispatchEvent(new Event('offline')));
    expect(
      screen.getByText('You are offline — data may be stale')
    ).toBeInTheDocument();

    act(() => window.dispatchEvent(new Event('online')));
    expect(
      screen.queryByText('You are offline — data may be stale')
    ).not.toBeInTheDocument();
  });
});
