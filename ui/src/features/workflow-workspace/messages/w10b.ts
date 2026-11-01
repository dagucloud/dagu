// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The step timeline: the duration of a step that ran under one second, and
// the tooltip line of a bar that is drawn after its dependency.
export default defineMessages({
  en: {
    '<1s': '<1s',
    'Shown after {step} (times are rounded to the second)':
      'Shown after {step} (times are rounded to the second)',
  },
  'zh-CN': {
    '<1s': '<1秒',
    'Shown after {step} (times are rounded to the second)':
      '显示在 {step} 之后（时间已取整到秒）',
  },
  ja: {
    '<1s': '<1秒',
    'Shown after {step} (times are rounded to the second)':
      '{step} の後に表示（時刻は秒単位に丸められています）',
  },
});
