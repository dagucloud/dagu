// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { isIgnorableQueryError } from '../QueryFeedback';

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
