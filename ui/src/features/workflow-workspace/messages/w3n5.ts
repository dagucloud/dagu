// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The repeat modes of a step, as RepeatBadge writes them.
export default defineMessages({
  en: {
    while: 'while',
    until: 'until',
  },
  'zh-CN': {
    while: '当满足时',
    until: '直到满足',
  },
  ja: {
    while: '条件が真の間',
    until: '条件が真になるまで',
  },
});
