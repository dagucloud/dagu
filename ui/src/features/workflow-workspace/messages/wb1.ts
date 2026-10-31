// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The run of a step with its upstream steps, and the runs that the step view follows.
export default defineMessages({
  en: {
    'Run the step {name} with its {count} upstream steps?':
      'Run the step {name} with its {count} upstream steps?',
    'Not run': 'Not run',
    'Show more': 'Show more',
    'Waiting for the step to run': 'Waiting for the step to run',
  },
  'zh-CN': {
    'Run the step {name} with its {count} upstream steps?':
      '运行步骤 {name} 及其 {count} 个上游步骤吗？',
    'Not run': '未运行',
    'Show more': '显示更多',
    'Waiting for the step to run': '等待步骤运行',
  },
  ja: {
    'Run the step {name} with its {count} upstream steps?':
      'ステップ {name} と上流の {count} 個のステップを実行しますか？',
    'Not run': '未実行',
    'Show more': 'もっと見る',
    'Waiting for the step to run': 'ステップの実行を待っています',
  },
});
