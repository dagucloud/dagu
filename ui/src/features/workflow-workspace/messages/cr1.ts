// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Continuous tab: the availability, the chart of the samples and the
// events of a step that repeats without a limit (spec 083).
export default defineMessages({
  en: {
    Continuous: 'Continuous',
    'Availability {window}': 'Availability {window}',
    '{failures} failed of {count} executions':
      '{failures} failed of {count} executions',
    'Current streak': 'Current streak',
    'since {time}': 'since {time}',
    'Last failure': 'Last failure',
    Samples: 'Samples',
    'No samples yet': 'No samples yet',
    'No events yet': 'No events yet',
    'Loading samples...': 'Loading samples...',
    'Loading events...': 'Loading events...',
    'Loading availability...': 'Loading availability...',
    'the samples of this step': 'the samples of this step',
    'the events of this step': 'the events of this step',
    'the availability of this step': 'the availability of this step',
    '{count} buckets of {resolution}, from {from} to {to}':
      '{count} buckets of {resolution}, from {from} to {to}',
    Resolution: 'Resolution',
    Min: 'Min',
    Max: 'Max',
    'Median (p50)': 'Median (p50)',
    'Slow tail (p95)': 'Slow tail (p95)',
    Stopped: 'Stopped',
    Recovered: 'Recovered',
    'exit {code}': 'exit {code}',
    'outage {length}': 'outage {length}',
    '{from} to {to}': '{from} to {to}',
    'Show all events': 'Show all events',
    'The samples of a step of a sub-run are not available here.':
      'The samples of a step of a sub-run are not available here.',
  },
  'zh-CN': {
    Continuous: '持续运行',
    'Availability {window}': '{window} 可用率',
    '{failures} failed of {count} executions':
      '{count} 次执行中失败 {failures} 次',
    'Current streak': '当前连续成功',
    'since {time}': '自 {time}',
    'Last failure': '最近失败',
    Samples: '采样',
    'No samples yet': '还没有采样',
    'No events yet': '还没有事件',
    'Loading samples...': '正在加载采样...',
    'Loading events...': '正在加载事件...',
    'Loading availability...': '正在加载可用率...',
    'the samples of this step': '此步骤的采样',
    'the events of this step': '此步骤的事件',
    'the availability of this step': '此步骤的可用率',
    '{count} buckets of {resolution}, from {from} to {to}':
      '从 {from} 到 {to}，{count} 个 {resolution} 区间',
    Resolution: '粒度',
    Min: '最小',
    Max: '最大',
    'Median (p50)': '中位数 (p50)',
    'Slow tail (p95)': '慢速尾部 (p95)',
    Stopped: '已停止',
    Recovered: '已恢复',
    'exit {code}': '退出码 {code}',
    'outage {length}': '中断 {length}',
    '{from} to {to}': '{from} 至 {to}',
    'Show all events': '显示全部事件',
    'The samples of a step of a sub-run are not available here.':
      '此处无法显示子运行中步骤的采样。',
  },
  ja: {
    Continuous: '継続実行',
    'Availability {window}': '{window} の稼働率',
    '{failures} failed of {count} executions':
      '{count} 回の実行のうち {failures} 回失敗',
    'Current streak': '現在の連続成功',
    'since {time}': '{time} から',
    'Last failure': '直近の失敗',
    Samples: 'サンプル',
    'No samples yet': 'サンプルはまだありません',
    'No events yet': 'イベントはまだありません',
    'Loading samples...': 'サンプルを読み込み中...',
    'Loading events...': 'イベントを読み込み中...',
    'Loading availability...': '稼働率を読み込み中...',
    'the samples of this step': 'このステップのサンプル',
    'the events of this step': 'このステップのイベント',
    'the availability of this step': 'このステップの稼働率',
    '{count} buckets of {resolution}, from {from} to {to}':
      '{from} から {to} まで、{resolution} 単位 {count} 区間',
    Resolution: '粒度',
    Min: '最小',
    Max: '最大',
    'Median (p50)': '中央値 (p50)',
    'Slow tail (p95)': '遅い側 (p95)',
    Stopped: '停止',
    Recovered: '回復',
    'exit {code}': '終了コード {code}',
    'outage {length}': '停止 {length}',
    '{from} to {to}': '{from} ～ {to}',
    'Show all events': 'すべてのイベントを表示',
    'The samples of a step of a sub-run are not available here.':
      'サブ実行のステップのサンプルはここでは表示できません。',
  },
});
