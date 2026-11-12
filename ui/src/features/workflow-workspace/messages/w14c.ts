// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The step timeline with the reused steps before the run, the hatched dots of
// the steps list, the order of the lines of a log, and the cached logs of the
// run summary.
export default defineMessages({
  en: {
    'Run starts {time}': 'Run starts {time}',
    'Reused, succeeded': 'Reused, succeeded',
    'Reused, failed': 'Reused, failed',
    'Old to new': 'Old to new',
    'New to old': 'New to old',
    'Reverse the order of the lines': 'Reverse the order of the lines',
    '1 cached step': '1 cached step',
    '{count} cached steps': '{count} cached steps',
    'Cached from run {id}: {step}': 'Cached from run {id}: {step}',
    'Log of run {id} is no longer available':
      'Log of run {id} is no longer available',
  },
  'zh-CN': {
    'Run starts {time}': '运行开始于 {time}',
    'Reused, succeeded': '已复用,成功',
    'Reused, failed': '已复用,失败',
    'Old to new': '从旧到新',
    'New to old': '从新到旧',
    'Reverse the order of the lines': '反转行的顺序',
    '1 cached step': '1 个缓存步骤',
    '{count} cached steps': '{count} 个缓存步骤',
    'Cached from run {id}: {step}': '缓存自运行 {id}:{step}',
    'Log of run {id} is no longer available': '运行 {id} 的日志已不可用',
  },
  ja: {
    'Run starts {time}': '実行開始 {time}',
    'Reused, succeeded': '再利用、成功',
    'Reused, failed': '再利用、失敗',
    'Old to new': '古い順',
    'New to old': '新しい順',
    'Reverse the order of the lines': '行の順序を反転',
    '1 cached step': 'キャッシュされたステップ 1 件',
    '{count} cached steps': 'キャッシュされたステップ {count} 件',
    'Cached from run {id}: {step}': '実行 {id} からキャッシュ: {step}',
    'Log of run {id} is no longer available':
      '実行 {id} のログは利用できなくなりました',
  },
});
