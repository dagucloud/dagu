// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The History tab of the Run panel: the steps of the workflow by run, and
// the runs of one step with its success rate and usual time.
export default defineMessages({
  en: {
    'History of {step}': 'History of {step}',
    'Runs of {step}': 'Runs of {step}',
    'Steps by run': 'Steps by run',
    '{percent}% succeeded': '{percent}% succeeded',
    '{succeeded} of {count} finished run succeeded':
      '{succeeded} of {count} finished run succeeded',
    '{succeeded} of {count} finished runs succeeded':
      '{succeeded} of {count} finished runs succeeded',
    'usually {duration}': 'usually {duration}',
    'Failing since {time}': 'Failing since {time}',
    'No finished runs of this step': 'No finished runs of this step',
    Longest: 'Longest',
    'Open the log of run {id}': 'Open the log of run {id}',
    'This step repeats without a limit: one run holds every repeat.':
      'This step repeats without a limit: one run holds every repeat.',
    'Open Continuous': 'Open Continuous',
    'The status is the one of this step, not of its sub-runs.':
      'The status is the one of this step, not of its sub-runs.',
    Show: 'Show',
    'Failures only': 'Failures only',
    'No failures in these runs': 'No failures in these runs',
  },
  'zh-CN': {
    'History of {step}': '{step} 的历史',
    'Runs of {step}': '{step} 的运行记录',
    'Steps by run': '各次运行的步骤',
    '{percent}% succeeded': '成功率 {percent}%',
    '{succeeded} of {count} finished run succeeded':
      '{count} 次已结束的运行中有 {succeeded} 次成功',
    '{succeeded} of {count} finished runs succeeded':
      '{count} 次已结束的运行中有 {succeeded} 次成功',
    'usually {duration}': '通常 {duration}',
    'Failing since {time}': '自{time}起一直失败',
    'No finished runs of this step': '此步骤还没有已结束的运行',
    Longest: '最长',
    'Open the log of run {id}': '打开运行 {id} 的日志',
    'This step repeats without a limit: one run holds every repeat.':
      '此步骤无限重复：一次运行包含其所有重复。',
    'Open Continuous': '打开持续运行',
    'The status is the one of this step, not of its sub-runs.':
      '状态是此步骤本身的状态，而不是其子运行的状态。',
    Show: '显示',
    'Failures only': '仅失败',
    'No failures in these runs': '这些运行中没有失败',
  },
  ja: {
    'History of {step}': '{step} の履歴',
    'Runs of {step}': '{step} の実行履歴',
    'Steps by run': '実行ごとのステップ',
    '{percent}% succeeded': '成功率 {percent}%',
    '{succeeded} of {count} finished run succeeded':
      '終了した {count} 回の実行のうち {succeeded} 回成功',
    '{succeeded} of {count} finished runs succeeded':
      '終了した {count} 回の実行のうち {succeeded} 回成功',
    'usually {duration}': '通常 {duration}',
    'Failing since {time}': '{time}から失敗中',
    'No finished runs of this step':
      'このステップの終了した実行はまだありません',
    Longest: '最長',
    'Open the log of run {id}': '実行 {id} のログを開く',
    'This step repeats without a limit: one run holds every repeat.':
      'このステップは上限なく繰り返します。1 回の実行にすべての繰り返しが含まれます。',
    'Open Continuous': '継続実行を開く',
    'The status is the one of this step, not of its sub-runs.':
      'ステータスはこのステップ自身のもので、サブ実行のものではありません。',
    Show: '表示',
    'Failures only': '失敗のみ',
    'No failures in these runs': 'これらの実行に失敗はありません',
  },
});
