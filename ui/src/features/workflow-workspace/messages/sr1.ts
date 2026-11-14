// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

export default defineMessages({
  en: {
    'This step ran {name} as a sub-run.': 'This step ran {name} as a sub-run.',
    'This step ran {name} as {count} sub-runs.':
      'This step ran {name} as {count} sub-runs.',
    'Choose sub-run': 'Choose sub-run',
    'Open sub-run: {name}': 'Open sub-run: {name}',
    'Choose one of {count} sub-runs of {name}':
      'Choose one of {count} sub-runs of {name}',
    'Step {step} of the selected run ran this workflow.':
      'Step {step} of the selected run ran this workflow.',
    'Step {step} of the selected run ran this workflow {count} times.':
      'Step {step} of the selected run ran this workflow {count} times.',
  },
  'zh-CN': {
    'This step ran {name} as a sub-run.': '此步骤以子运行的方式运行了 {name}。',
    'This step ran {name} as {count} sub-runs.':
      '此步骤以 {count} 个子运行的方式运行了 {name}。',
    'Choose sub-run': '选择子运行',
    'Open sub-run: {name}': '打开子运行：{name}',
    'Choose one of {count} sub-runs of {name}':
      '从 {name} 的 {count} 个子运行中选择一个',
    'Step {step} of the selected run ran this workflow.':
      '所选运行的步骤 {step} 运行了此工作流。',
    'Step {step} of the selected run ran this workflow {count} times.':
      '所选运行的步骤 {step} 运行了此工作流 {count} 次。',
  },
  ja: {
    'This step ran {name} as a sub-run.':
      'このステップは {name} をサブ実行として実行しました。',
    'This step ran {name} as {count} sub-runs.':
      'このステップは {name} を {count} 件のサブ実行として実行しました。',
    'Choose sub-run': 'サブ実行を選択',
    'Open sub-run: {name}': 'サブ実行を開く: {name}',
    'Choose one of {count} sub-runs of {name}':
      '{name} の {count} 件のサブ実行から選択',
    'Step {step} of the selected run ran this workflow.':
      '選択した実行のステップ {step} がこのワークフローを実行しました。',
    'Step {step} of the selected run ran this workflow {count} times.':
      '選択した実行のステップ {step} がこのワークフローを {count} 回実行しました。',
  },
});
