// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Issues button that takes the place of Save, and the line of an issue.
export default defineMessages({
  en: {
    'Fix the issues before you save ({count})':
      'Fix the issues before you save ({count})',
    'line {line}': 'line {line}',
  },
  'zh-CN': {
    'Fix the issues before you save ({count})': '保存前请先修复问题（{count}）',
    'line {line}': '第 {line} 行',
  },
  ja: {
    'Fix the issues before you save ({count})':
      '保存する前に問題を修正してください（{count}）',
    'line {line}': '{line} 行目',
  },
});
