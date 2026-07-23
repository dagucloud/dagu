// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';

import { controllerDAGOption } from '../dagOptions';

describe('Controller DAG options', () => {
  it('keeps DAGs with stable identity and named parameters', () => {
    expect(
      controllerDAGOption({
        fileName: 'classify',
        dag: {
          name: 'classify',
          description: 'Classify an alert.',
          params: ['severity=warning'],
        },
      })
    ).toEqual({
      fileName: 'classify',
      name: 'classify',
      description: 'Classify an alert.',
    });
  });

  it('omits DAGs with unstable identity or positional parameters', () => {
    expect(
      controllerDAGOption({
        fileName: 'classify',
        dag: { name: 'renamed', params: ['severity=warning'] },
      })
    ).toBeNull();
    expect(
      controllerDAGOption({
        fileName: 'classify',
        dag: { name: 'classify', params: ['1=warning'] },
      })
    ).toBeNull();
  });
});
