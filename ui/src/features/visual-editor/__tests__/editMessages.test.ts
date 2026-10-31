// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { EditErrorCode } from '../contracts';
import { translateStatic } from '@/i18n/staticMessages';
import { canConvertForEdit, editErrorMessage } from '../editMessages';

const codes: EditErrorCode[] = [
  'parseError',
  'unknownStep',
  'unsupportedForm',
  'chainType',
  'agentType',
  'duplicateName',
  'invalidName',
  'cycle',
  'invalidYaml',
];

describe('editErrorMessage', () => {
  it('gives a translated message for each failure', () => {
    for (const code of codes) {
      const english = editErrorMessage(code, (s) => s);
      expect(english).toBeTruthy();
      const japanese = editErrorMessage(code, (s) => translateStatic('ja', s));
      expect(japanese).not.toBe(english);
    }
  });

  it('gives no message for an edit that changed nothing', () => {
    expect(editErrorMessage('noop', (s) => s)).toBeNull();
  });
});

describe('canConvertForEdit', () => {
  it('offers the conversion only for step form failures', () => {
    expect(codes.filter(canConvertForEdit)).toEqual([
      'unsupportedForm',
      'chainType',
    ]);
  });
});
