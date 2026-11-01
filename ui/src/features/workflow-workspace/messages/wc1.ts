// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Run panel: the timeline of the latest runs, and the run spec dialog.
// Also the delete error of the DAG edit actions.
export default defineMessages({
  en: {
    Run: 'Run',
    Runs: 'Runs',
    'Run timeline': 'Run timeline',
    'Run {id}': 'Run {id}',
    latest: 'latest',
    Latest: 'Latest',
    'Start the workflow to see its runs here.':
      'Start the workflow to see its runs here.',
    'Failed to delete DAG': 'Failed to delete DAG',
  },
  'zh-CN': {
    Run: '运行',
    Runs: '运行记录',
    'Run timeline': '运行时间线',
    'Run {id}': '运行 {id}',
    latest: '最新',
    Latest: '最新',
    'Start the workflow to see its runs here.':
      '启动工作流后，其运行会显示在这里。',
    'Failed to delete DAG': '删除 DAG 失败',
  },
  ja: {
    Run: '実行',
    Runs: '実行履歴',
    'Run timeline': '実行タイムライン',
    'Run {id}': '実行 {id}',
    latest: '最新',
    Latest: '最新',
    'Start the workflow to see its runs here.':
      'ワークフローを開始すると、ここに実行が表示されます。',
    'Failed to delete DAG': 'DAG の削除に失敗しました',
  },
});
