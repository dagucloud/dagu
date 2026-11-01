// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Refresh tooltip of the bar, the .env blocks of the Overview tab and
// the star of a required key in the Add a step panel.
export default defineMessages({
  en: {
    'Reload the workflow, its runs and the latest run from the server':
      'Reload the workflow, its runs and the latest run from the server',
    'Show less': 'Show less',
    'Required key': 'Required key',
  },
  'zh-CN': {
    'Reload the workflow, its runs and the latest run from the server':
      '从服务器重新加载工作流、其运行记录和最新一次运行',
    'Show less': '收起',
    'Required key': '必填键',
  },
  ja: {
    'Reload the workflow, its runs and the latest run from the server':
      'サーバーからワークフロー、その実行履歴、最新の実行を再読み込みします',
    'Show less': '折りたたむ',
    'Required key': '必須キー',
  },
});
