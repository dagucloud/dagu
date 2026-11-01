// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { editDocument } from '../patch';

describe('editDocument', () => {
  // The text parses, but its document cannot be written back.
  it('returns an error for an alias before its anchor', () => {
    const text =
      'steps:\n  - name: a\n    run: *cmd\n  - name: b\n    run: &cmd echo hi\n';
    const result = editDocument(text, (doc) => {
      doc.set('name', 'demo');
    });
    expect(result).toMatchObject({ ok: false, code: 'invalidYaml' });
  });
});
