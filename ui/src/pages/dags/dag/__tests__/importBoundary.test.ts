// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  findForbiddenStaticImports,
  MERMAID_FORBIDDEN,
} from '@/testing/importGraph';

// The chunk of the workflow page carries no Mermaid.
describe('workflow page import boundary', () => {
  // The scan parses the whole reachable tree; under a parallel run it can
  // take longer than the default 5 s.
  it('reaches no Mermaid module statically', { timeout: 60_000 }, () => {
    expect(
      findForbiddenStaticImports(
        ['pages/dags/dag/index.tsx'],
        MERMAID_FORBIDDEN
      )
    ).toEqual([]);
  });
});
