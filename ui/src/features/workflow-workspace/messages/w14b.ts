// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The run timeline: the cached time, the time range, and the arrows between
// the runs that the timeline shows.
export default defineMessages({
  en: {
    'Show cached time': 'Show cached time',
    'Hide cached time': 'Hide cached time',
    'Total {total} = own {own} + cached {cached}':
      'Total {total} = own {own} + cached {cached}',
    'Newest runs': 'Newest runs',
    'Last hour': 'Last hour',
    'Last 6 hours': 'Last 6 hours',
    'Last 24 hours': 'Last 24 hours',
    'Edit custom range': 'Edit custom range',
    'The start must not be after the end.':
      'The start must not be after the end.',
    'Only the last {n} runs are loaded': 'Only the last {n} runs are loaded',
    'No runs in this time range': 'No runs in this time range',
    'Choose a longer range to see more runs.':
      'Choose a longer range to see more runs.',
    'No older run in this view': 'No older run in this view',
    'No newer run in this view': 'No newer run in this view',
  },
  'zh-CN': {
    'Show cached time': '显示缓存时间',
    'Hide cached time': '隐藏缓存时间',
    'Total {total} = own {own} + cached {cached}':
      '总计 {total} = 自身 {own} + 缓存 {cached}',
    'Newest runs': '最新运行',
    'Last hour': '最近 1 小时',
    'Last 6 hours': '最近 6 小时',
    'Last 24 hours': '最近 24 小时',
    'Edit custom range': '编辑自定义范围',
    'The start must not be after the end.': '开始时间不能晚于结束时间。',
    'Only the last {n} runs are loaded': '仅加载了最近 {n} 次运行',
    'No runs in this time range': '此时间范围内没有运行',
    'Choose a longer range to see more runs.': '选择更长的范围以查看更多运行。',
    'No older run in this view': '此视图中没有更早的运行',
    'No newer run in this view': '此视图中没有更新的运行',
  },
  ja: {
    'Show cached time': 'キャッシュ時間を表示',
    'Hide cached time': 'キャッシュ時間を非表示',
    'Total {total} = own {own} + cached {cached}':
      '合計 {total} = 自身 {own} + キャッシュ {cached}',
    'Newest runs': '最新の実行',
    'Last hour': '過去 1 時間',
    'Last 6 hours': '過去 6 時間',
    'Last 24 hours': '過去 24 時間',
    'Edit custom range': 'カスタム範囲を編集',
    'The start must not be after the end.': '開始は終了より後にできません。',
    'Only the last {n} runs are loaded': '直近 {n} 件の実行のみ読み込み済み',
    'No runs in this time range': 'この期間に実行はありません',
    'Choose a longer range to see more runs.':
      'より長い範囲を選ぶと、さらに多くの実行を表示できます。',
    'No older run in this view': 'このビューに古い実行はありません',
    'No newer run in this view': 'このビューに新しい実行はありません',
  },
});
