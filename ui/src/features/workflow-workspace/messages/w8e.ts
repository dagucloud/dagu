// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The repeat policy of a step: the summary line and the iteration, as
// RepeatBadge and the step facts write them.
export default defineMessages({
  en: {
    'repeat while "{condition}"': 'repeat while "{condition}"',
    'repeat until "{condition}"': 'repeat until "{condition}"',
    'repeat while exit {codes}': 'repeat while exit {codes}',
    'repeat until exit {codes}': 'repeat until exit {codes}',
    'every {interval}': 'every {interval}',
    'max {limit}': 'max {limit}',
    'no limit': 'no limit',
    'iteration {n} of {limit}': 'iteration {n} of {limit}',
    'iteration {n}': 'iteration {n}',
  },
  'zh-CN': {
    'repeat while "{condition}"': '当 "{condition}" 成立时重复',
    'repeat until "{condition}"': '重复直到 "{condition}"',
    'repeat while exit {codes}': '当退出码为 {codes} 时重复',
    'repeat until exit {codes}': '重复直到退出码为 {codes}',
    'every {interval}': '每 {interval}',
    'max {limit}': '最多 {limit} 次',
    'no limit': '不限次数',
    'iteration {n} of {limit}': '第 {n} 次，共 {limit} 次',
    'iteration {n}': '第 {n} 次',
  },
  ja: {
    'repeat while "{condition}"': '"{condition}" の間繰り返す',
    'repeat until "{condition}"': '"{condition}" になるまで繰り返す',
    'repeat while exit {codes}': '終了コードが {codes} の間繰り返す',
    'repeat until exit {codes}': '終了コードが {codes} になるまで繰り返す',
    'every {interval}': '{interval}ごと',
    'max {limit}': '最大 {limit} 回',
    'no limit': '回数制限なし',
    'iteration {n} of {limit}': '{limit} 回中 {n} 回目',
    'iteration {n}': '{n} 回目',
  },
});
