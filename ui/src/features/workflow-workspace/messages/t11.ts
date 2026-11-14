// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

export default defineMessages({
  en: {
    'Choose a sub DAG run': 'Choose a sub DAG run',
    'Failed to update status': 'Failed to update status',
    'Run upstream steps': 'Run upstream steps',
    'Fix the file to run the upstream steps':
      'Fix the file to run the upstream steps',
    'Run the {count} upstream steps of {name} now? Their commands run for real.':
      'Run the {count} upstream steps of {name} now? Their commands run for real.',
  },
  'zh-CN': {
    'Choose a sub DAG run': '选择一个子 DAG 运行',
    'Failed to update status': '更新状态失败',
    'Run upstream steps': '运行上游步骤',
    'Fix the file to run the upstream steps': '请先修复文件，再运行上游步骤',
    'Run the {count} upstream steps of {name} now? Their commands run for real.':
      '现在运行 {name} 的 {count} 个上游步骤吗？它们的命令会真实执行。',
  },
  ja: {
    'Choose a sub DAG run': 'サブ DAG 実行を選択',
    'Failed to update status': 'ステータスの更新に失敗しました',
    'Run upstream steps': '上流ステップを実行',
    'Fix the file to run the upstream steps':
      'ファイルを修正すると上流ステップを実行できます',
    'Run the {count} upstream steps of {name} now? Their commands run for real.':
      '{name} の上流の {count} 個のステップを今すぐ実行しますか？コマンドは実際に実行されます。',
  },
});
