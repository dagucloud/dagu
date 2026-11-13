// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The run history of a service DAG in the Activity panel (spec 097): the
// time ranges from 30 days to 3 years, the gap event, and the retention note.
// Also the row of a service DAG in the Runs list: its state and its failures.
export default defineMessages({
  en: {
    'Last year': 'Last year',
    'Last 3 years': 'Last 3 years',
    'Run history': 'Run history',
    'the run history': 'the run history',
    'the availability of the runs': 'the availability of the runs',
    'the events of the run history': 'the events of the run history',
    'No runs in this window': 'No runs in this window',
    '{failures} failed of {count} runs': '{failures} failed of {count} runs',
    Gap: 'Gap',
    'no run for {length}': 'no run for {length}',
    'Open run {id}': 'Open run {id}',
    'The runs before {time} are not kept.':
      'The runs before {time} are not kept.',
    'Runs in the last {window}': 'Runs in the last {window}',
    'Longest gap': 'Longest gap',
    'Wait {seconds} s': 'Wait {seconds} s',
    '{count} failed in a row': '{count} failed in a row',
  },
  'zh-CN': {
    'Last year': '最近 1 年',
    'Last 3 years': '最近 3 年',
    'Run history': '运行历史',
    'the run history': '运行历史',
    'the availability of the runs': '运行的可用率',
    'the events of the run history': '运行历史的事件',
    'No runs in this window': '此时间段内没有运行',
    '{failures} failed of {count} runs': '{count} 次运行中失败 {failures} 次',
    Gap: '空档',
    'no run for {length}': '{length} 内没有运行',
    'Open run {id}': '打开运行 {id}',
    'The runs before {time} are not kept.': '{time} 之前的运行未保留。',
    'Runs in the last {window}': '过去 {window}内的运行',
    'Longest gap': '最长空档',
    'Wait {seconds} s': '等待 {seconds} 秒',
    '{count} failed in a row': '连续失败 {count} 次',
  },
  ja: {
    'Last year': '過去 1 年',
    'Last 3 years': '過去 3 年',
    'Run history': '実行履歴',
    'the run history': '実行履歴',
    'the availability of the runs': '実行の可用性',
    'the events of the run history': '実行履歴のイベント',
    'No runs in this window': 'この期間に実行はありません',
    '{failures} failed of {count} runs': '{count} 件中 {failures} 件が失敗',
    Gap: '空白',
    'no run for {length}': '{length} の間、実行なし',
    'Open run {id}': '実行 {id} を開く',
    'The runs before {time} are not kept.':
      '{time} より前の実行は保持されていません。',
    'Runs in the last {window}': '過去 {window}の実行',
    'Longest gap': '最長の空白',
    'Wait {seconds} s': '{seconds} 秒待機',
    '{count} failed in a row': '{count} 回連続で失敗',
  },
});
