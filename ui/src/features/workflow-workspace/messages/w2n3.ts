// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The INPUT and OUTPUT rows of the step view: one load error line.
export default defineMessages({
  en: {
    'Could not load {thing}': 'Could not load {thing}',
    'the outputs of this step': 'the outputs of this step',
    'the run {runId}': 'the run {runId}',
  },
  'zh-CN': {
    'Could not load {thing}': '无法加载{thing}',
    'the outputs of this step': '此步骤的输出',
    'the run {runId}': '运行 {runId}',
  },
  ja: {
    'Could not load {thing}': '{thing}を読み込めません',
    'the outputs of this step': 'このステップの出力',
    'the run {runId}': '実行 {runId}',
  },
});
