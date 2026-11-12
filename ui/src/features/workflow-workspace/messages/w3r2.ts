// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The step run dialog: the run that feeds the step and the state of the
// previous steps.
export default defineMessages({
  en: {
    'Changed steps': 'Changed steps',
    'Checking earlier runs': 'Checking earlier runs',
    'Inputs from run {id}': 'Inputs from run {id}',
    'No earlier run holds the outputs that this step reads.':
      'No earlier run holds the outputs that this step reads.',
    'Run {id} used other parameters. The step runs with the defaults.':
      'Run {id} used other parameters. The step runs with the defaults.',
    'Waits for run {id} to finish': 'Waits for run {id} to finish',
    'changed since run {id}': 'changed since run {id}',
    'no output in run {id}': 'no output in run {id}',
    reused: 'reused',
  },
  'zh-CN': {
    'Changed steps': '已更改的步骤',
    'Checking earlier runs': '正在检查之前的运行',
    'Inputs from run {id}': '输入来自运行 {id}',
    'No earlier run holds the outputs that this step reads.':
      '没有之前的运行保存此步骤所读取的输出。',
    'Run {id} used other parameters. The step runs with the defaults.':
      '运行 {id} 使用了其他参数。此步骤将使用默认值运行。',
    'Waits for run {id} to finish': '等待运行 {id} 结束',
    'changed since run {id}': '自运行 {id} 起已更改',
    'no output in run {id}': '运行 {id} 中没有输出',
    reused: '已复用',
  },
  ja: {
    'Changed steps': '変更されたステップ',
    'Checking earlier runs': '以前の実行を確認しています',
    'Inputs from run {id}': '入力の元の実行: {id}',
    'No earlier run holds the outputs that this step reads.':
      'このステップが読む出力を持つ以前の実行はありません。',
    'Run {id} used other parameters. The step runs with the defaults.':
      '実行 {id} は別のパラメータを使いました。このステップは既定値で実行されます。',
    'Waits for run {id} to finish': '実行 {id} の終了を待っています',
    'changed since run {id}': '実行 {id} から変更あり',
    'no output in run {id}': '実行 {id} に出力なし',
    reused: '再利用',
  },
});
