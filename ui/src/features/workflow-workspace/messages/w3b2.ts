// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The run identity of the bar: the dot of a different definition and the
// run identity of a narrow bar.
export default defineMessages({
  en: {
    'This run used a different definition.':
      'This run used a different definition.',
    'This run used a different definition. {count} step of the run is not on the canvas.':
      'This run used a different definition. {count} step of the run is not on the canvas.',
    'This run used a different definition. {count} steps of the run are not on the canvas.':
      'This run used a different definition. {count} steps of the run are not on the canvas.',
    'Show the definition of this run': 'Show the definition of this run',
    'Back to the current definition': 'Back to the current definition',
    'Pinned run': 'Pinned run',
    'Open the run timeline': 'Open the run timeline',
  },
  'zh-CN': {
    'This run used a different definition.': '此次运行使用了不同的定义。',
    'This run used a different definition. {count} step of the run is not on the canvas.':
      '此次运行使用了不同的定义。运行中有 {count} 个步骤不在画布上。',
    'This run used a different definition. {count} steps of the run are not on the canvas.':
      '此次运行使用了不同的定义。运行中有 {count} 个步骤不在画布上。',
    'Show the definition of this run': '显示此次运行的定义',
    'Back to the current definition': '返回当前定义',
    'Pinned run': '已固定的运行',
    'Open the run timeline': '打开运行时间线',
  },
  ja: {
    'This run used a different definition.': 'この実行は別の定義を使いました。',
    'This run used a different definition. {count} step of the run is not on the canvas.':
      'この実行は別の定義を使いました。実行の {count} 個のステップがキャンバスにありません。',
    'This run used a different definition. {count} steps of the run are not on the canvas.':
      'この実行は別の定義を使いました。実行の {count} 個のステップがキャンバスにありません。',
    'Show the definition of this run': 'この実行の定義を表示',
    'Back to the current definition': '現在の定義に戻る',
    'Pinned run': '固定された実行',
    'Open the run timeline': '実行のタイムラインを開く',
  },
});
