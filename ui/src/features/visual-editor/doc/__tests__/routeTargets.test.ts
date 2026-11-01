// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { applyEdit } from '..';

const ROUTER = `steps:
  - name: pick
    action: router.route
    with:
      value: "404"
      routes:
        ok: [done]
  - name: done
    run: echo done
  - name: client_error
    id: e4xx
    run: echo 4xx
`;

describe('setRouteTargets', () => {
  // The server resolves a route target by the step name only.
  it('writes the name of a step that the target gives by its id', () => {
    const result = applyEdit(ROUTER, {
      kind: 'setRouteTargets',
      step: 'steps[0]',
      pattern: 'missing',
      targets: ['e4xx'],
    });
    if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
    expect(result.text).toContain('missing: [client_error]');
  });
});
