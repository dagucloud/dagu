// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The outputs cache of the step run: the source line, the marks of the
// previous steps, and the test run.
export default defineMessages({
  en: {
    'Inputs from your cache ({time})': 'Inputs from your cache ({time})',
    'Inputs from run {id} and your cache':
      'Inputs from run {id} and your cache',
    cached: 'cached',
    'Test run, not kept': 'Test run, not kept',
  },
  'zh-CN': {
    'Inputs from your cache ({time})': '输入来自你的缓存（{time}）',
    'Inputs from run {id} and your cache': '输入来自运行 {id} 和你的缓存',
    cached: '已缓存',
    'Test run, not kept': '测试运行，不保留',
  },
  ja: {
    'Inputs from your cache ({time})': '入力の元: あなたのキャッシュ（{time}）',
    'Inputs from run {id} and your cache':
      '入力の元: 実行 {id} とあなたのキャッシュ',
    cached: 'キャッシュ済み',
    'Test run, not kept': 'テスト実行（保存されません）',
  },
});
