// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

export default defineMessages({
  en: {
    'More run options': 'More run options',
    'Stop signal sent to all running instances':
      'Stop signal sent to all running instances',
    'Failed to stop all DAG instances': 'Failed to stop all DAG instances',
    'Some instances may have already completed or the worker is unavailable.':
      'Some instances may have already completed or the worker is unavailable.',
  },
  'zh-CN': {
    'More run options': '更多运行选项',
    'Stop signal sent to all running instances':
      '已向所有运行中的实例发送停止信号',
    'Failed to stop all DAG instances': '停止所有 DAG 实例失败',
    'Some instances may have already completed or the worker is unavailable.':
      '部分实例可能已完成，或工作节点不可用。',
  },
  ja: {
    'More run options': 'その他の実行オプション',
    'Stop signal sent to all running instances':
      '実行中のすべてのインスタンスに停止シグナルを送信しました',
    'Failed to stop all DAG instances':
      'すべての DAG インスタンスを停止できませんでした',
    'Some instances may have already completed or the worker is unavailable.':
      '一部のインスタンスはすでに完了しているか、ワーカーが利用できない可能性があります。',
  },
});
