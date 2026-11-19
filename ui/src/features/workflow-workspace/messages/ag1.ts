// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The agent step in the step editor and on the canvas: its own sections
// (Model, Tools with the built-in tools, Limits) and the rows of the canvas.
export default defineMessages({
  en: {
    '(choose a provider)': '(choose a provider)',
    'Required. The CLI that runs the step.':
      'Required. The CLI that runs the step.',
    'Built-in tools': 'Built-in tools',
    'Read files': 'Read files',
    'Write files': 'Write files',
    'Edit files': 'Edit files',
    'Search files': 'Search files',
    'Shell commands': 'Shell commands',
    'Allowed commands': 'Allowed commands',
    'Denied commands': 'Denied commands',
    'A pattern is a command prefix. A final * means any arguments. A deny pattern wins over an allow pattern.':
      'A pattern is a command prefix. A final * means any arguments. A deny pattern wins over an allow pattern.',
    Limits: 'Limits',
    'Maximum number of tool iterations': 'Maximum number of tool iterations',
    'The most model requests of one attempt. An attempt that reaches the limit before an answer fails.':
      'The most model requests of one attempt. An attempt that reaches the limit before an answer fails.',
    'Maximum number of tool calls': 'Maximum number of tool calls',
    'The most calls of DAG tools in one attempt. A call over the limit gets an error result. The attempt does not fail.':
      'The most calls of DAG tools in one attempt. A call over the limit gets an error result. The attempt does not fail.',
    'Dagu stops the step at this limit.': 'Dagu stops the step at this limit.',
    'Enter a whole number, 0 or more': 'Enter a whole number, 0 or more',
    '{count} allowed': '{count} allowed',
    Allow: 'Allow',
    Deny: 'Deny',
  },
  'zh-CN': {
    '(choose a provider)': '（选择提供方）',
    'Required. The CLI that runs the step.': '必填。运行该步骤的 CLI。',
    'Built-in tools': '内置工具',
    'Read files': '读取文件',
    'Write files': '写入文件',
    'Edit files': '编辑文件',
    'Search files': '搜索文件',
    'Shell commands': 'Shell 命令',
    'Allowed commands': '允许的命令',
    'Denied commands': '禁止的命令',
    'A pattern is a command prefix. A final * means any arguments. A deny pattern wins over an allow pattern.':
      '模式是命令前缀。结尾的 * 表示任意参数。禁止模式优先于允许模式。',
    Limits: '限制',
    'Maximum number of tool iterations': '工具迭代最大次数',
    'The most model requests of one attempt. An attempt that reaches the limit before an answer fails.':
      '一次尝试的最多模型请求数。在得出答案前达到限制的尝试会失败。',
    'Maximum number of tool calls': '最大工具调用数',
    'The most calls of DAG tools in one attempt. A call over the limit gets an error result. The attempt does not fail.':
      '一次尝试中 DAG 工具的最多调用数。超过限制的调用得到错误结果。尝试不会失败。',
    'Dagu stops the step at this limit.': 'Dagu 在此限制处停止步骤。',
    'Enter a whole number, 0 or more': '请输入不小于 0 的整数',
    '{count} allowed': '允许 {count} 项',
    Allow: '允许',
    Deny: '禁止',
  },
  ja: {
    '(choose a provider)': '（プロバイダーを選択）',
    'Required. The CLI that runs the step.':
      '必須です。ステップを実行する CLI です。',
    'Built-in tools': '組み込みツール',
    'Read files': 'ファイルの読み取り',
    'Write files': 'ファイルの書き込み',
    'Edit files': 'ファイルの編集',
    'Search files': 'ファイルの検索',
    'Shell commands': 'シェルコマンド',
    'Allowed commands': '許可するコマンド',
    'Denied commands': '拒否するコマンド',
    'A pattern is a command prefix. A final * means any arguments. A deny pattern wins over an allow pattern.':
      'パターンはコマンドの先頭部分です。末尾の * は任意の引数を表します。拒否パターンは許可パターンより優先されます。',
    Limits: '制限',
    'Maximum number of tool iterations': 'ツール反復の最大回数',
    'The most model requests of one attempt. An attempt that reaches the limit before an answer fails.':
      '1 回の試行でのモデルへのリクエストの上限です。回答の前に上限に達した試行は失敗します。',
    'Maximum number of tool calls': 'ツール呼び出しの最大数',
    'The most calls of DAG tools in one attempt. A call over the limit gets an error result. The attempt does not fail.':
      '1 回の試行での DAG ツールの呼び出しの上限です。上限を超えた呼び出しはエラー結果になります。試行は失敗しません。',
    'Dagu stops the step at this limit.':
      'この制限で Dagu がステップを停止します。',
    'Enter a whole number, 0 or more': '0 以上の整数を入力してください',
    '{count} allowed': '{count} 件を許可',
    Allow: '許可',
    Deny: '拒否',
  },
});
