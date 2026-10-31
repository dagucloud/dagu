// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { translateStatic } from '@/i18n/staticMessages';
import { DOCK_TAB_LABELS } from '../dock/DockTabStrip';

describe('Japanese wording of the dock and the run list', () => {
  // The dock toggle (Logs) and its Log tab were both ログ.
  it('names the dock and the Log tab differently', () => {
    expect(translateStatic('ja', 'Logs')).toBe('ログ');
    expect(translateStatic('ja', DOCK_TAB_LABELS.log)).not.toBe(
      translateStatic('ja', 'Logs')
    );
  });

  // The rest of the UI says 実行 for a run.
  it('says 実行 for the runs of the list footer', () => {
    expect(translateStatic('ja', 'All runs')).toBe('すべての実行');
  });
});
