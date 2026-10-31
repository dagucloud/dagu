// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The dialog of Run step and Execute step: Run with the previous steps, or
// Step only.
export default defineMessages({
  en: {
    'Step only': 'Step only',
    'Run the step {name} with its previous steps?':
      'Run the step {name} with its previous steps?',
    'Steps that run: {steps}': 'Steps that run: {steps}',
  },
  'zh-CN': {
    'Step only': '仅此步骤',
    'Run the step {name} with its previous steps?':
      '连同前面的步骤一起运行步骤 {name}？',
    'Steps that run: {steps}': '将运行的步骤：{steps}',
  },
  ja: {
    'Step only': 'このステップのみ',
    'Run the step {name} with its previous steps?':
      '前のステップと一緒にステップ {name} を実行しますか？',
    'Steps that run: {steps}': '実行するステップ：{steps}',
  },
});
