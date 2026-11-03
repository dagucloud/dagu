// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

export default defineMessages({
  en: {
    'This step ran {name} as a sub-run.': 'This step ran {name} as a sub-run.',
    'This step ran {name} as {count} sub-runs.':
      'This step ran {name} as {count} sub-runs.',
    'Choose sub-run': 'Choose sub-run',
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
    'Step {step} of the selected run ran this workflow.':
      '選択した実行のステップ {step} がこのワークフローを実行しました。',
    'Step {step} of the selected run ran this workflow {count} times.':
      '選択した実行のステップ {step} がこのワークフローを {count} 回実行しました。',
  },
});
