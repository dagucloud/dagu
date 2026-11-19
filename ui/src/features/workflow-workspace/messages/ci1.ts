// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The facts of a step on the canvas: the limits of the Tools card of an agent
// step, and the conditions that change the flow (continue_on, preconditions).
export default defineMessages({
  en: {
    'max {count} turn': 'max {count} turn',
    'max {count} turns': 'max {count} turns',
    'max {count} call': 'max {count} call',
    'max {count} calls': 'max {count} calls',
    '{used} of {count} calls': '{used} of {count} calls',
    'The most model turns of one attempt.':
      'The most model turns of one attempt.',
    'The most calls of DAG tools in one attempt.':
      'The most calls of DAG tools in one attempt.',
    'The most calls of DAG tools in one attempt. This is the default.':
      'The most calls of DAG tools in one attempt. This is the default.',
    'if {step} failed': 'if {step} failed',
    'if {step} succeeded': 'if {step} succeeded',
    'if {step} exit code is {code}': 'if {step} exit code is {code}',
    'if {step} exit code is not {code}': 'if {step} exit code is not {code}',
    'if {step}: {test}': 'if {step}: {test}',
    'Precondition: {text}': 'Precondition: {text}',
    'continues on failure': 'continues on failure',
    'continues if skipped': 'continues if skipped',
    'continues on exit code {codes}': 'continues on exit code {codes}',
    'continues on matching output': 'continues on matching output',
    'The run goes on with the steps after this one.':
      'The run goes on with the steps after this one.',
    'The step then counts as a success.': 'The step then counts as a success.',
    'Runs only if:': 'Runs only if:',
    Conditional: 'Conditional',
    Empty: 'Empty',
    'No prompt': 'No prompt',
    'Add a prompt': 'Add a prompt',
    'Edit the prompt and the system message':
      'Edit the prompt and the system message',
    Trusted: 'Trusted',
    'Untrusted: the key untrusted is true.':
      'Untrusted: the key untrusted is true.',
    'retry {used}/{limit}': 'retry {used}/{limit}',
    'retry ×{limit}': 'retry ×{limit}',
    'exit {codes}': 'exit {codes}',
    'Retries the step up to {limit} times.':
      'Retries the step up to {limit} times.',
    'Retries the step.': 'Retries the step.',
    'Waits {interval} between the attempts.':
      'Waits {interval} between the attempts.',
    'Retries only after the exit code {codes}.':
      'Retries only after the exit code {codes}.',
    'timeout {time}': 'timeout {time}',
    '{count} secret': '{count} secret',
    '{count} secrets': '{count} secrets',
    'output schema': 'output schema',
    'Output schema': 'Output schema',
    'Output schema: {names}': 'Output schema: {names}',
    'Secrets: {names}': 'Secrets: {names}',
    'System: {line}': 'System: {line}',
    'Prompt: {line}': 'Prompt: {line}',
    '{count} tool call': '{count} tool call',
    '{count} tool calls': '{count} tool calls',
  },
  'zh-CN': {
    'max {count} turn': '最多 {count} 轮',
    'max {count} turns': '最多 {count} 轮',
    'max {count} call': '最多 {count} 次调用',
    'max {count} calls': '最多 {count} 次调用',
    '{used} of {count} calls': '已调用 {used} / {count} 次',
    'The most model turns of one attempt.': '一次尝试中模型的最大轮数。',
    'The most calls of DAG tools in one attempt.':
      '一次尝试中 DAG 工具的最大调用次数。',
    'The most calls of DAG tools in one attempt. This is the default.':
      '一次尝试中 DAG 工具的最大调用次数。这是默认值。',
    'if {step} failed': '如果 {step} 失败',
    'if {step} succeeded': '如果 {step} 成功',
    'if {step} exit code is {code}': '如果 {step} 的退出码是 {code}',
    'if {step} exit code is not {code}': '如果 {step} 的退出码不是 {code}',
    'if {step}: {test}': '如果 {step}：{test}',
    'Precondition: {text}': '前置条件：{text}',
    'continues on failure': '失败后继续',
    'continues if skipped': '跳过后继续',
    'continues on exit code {codes}': '退出码为 {codes} 时继续',
    'continues on matching output': '输出匹配时继续',
    'The run goes on with the steps after this one.':
      '运行会继续执行此步骤之后的步骤。',
    'The step then counts as a success.': '此步骤随后视为成功。',
    'Runs only if:': '仅在以下条件成立时运行：',
    Conditional: '有条件',
    Empty: '空',
    'No prompt': '没有提示词',
    'Add a prompt': '添加提示词',
    'Edit the prompt and the system message': '编辑提示词和系统消息',
    Trusted: '受信任',
    'Untrusted: the key untrusted is true.': '不受信任：键 untrusted 为 true。',
    'retry {used}/{limit}': '重试 {used}/{limit}',
    'retry ×{limit}': '重试 ×{limit}',
    'exit {codes}': '退出码 {codes}',
    'Retries the step up to {limit} times.': '此步骤最多重试 {limit} 次。',
    'Retries the step.': '此步骤会重试。',
    'Waits {interval} between the attempts.': '两次尝试之间等待 {interval}。',
    'Retries only after the exit code {codes}.':
      '仅在退出码为 {codes} 时重试。',
    'timeout {time}': '超时 {time}',
    '{count} secret': '{count} 个密钥',
    '{count} secrets': '{count} 个密钥',
    'output schema': '输出模式',
    'Output schema': '输出模式',
    'Output schema: {names}': '输出模式：{names}',
    'Secrets: {names}': '密钥：{names}',
    'System: {line}': '系统消息：{line}',
    'Prompt: {line}': '提示词：{line}',
    '{count} tool call': '{count} 次工具调用',
    '{count} tool calls': '{count} 次工具调用',
  },
  ja: {
    'max {count} turn': '最大 {count} ターン',
    'max {count} turns': '最大 {count} ターン',
    'max {count} call': '最大 {count} 回の呼び出し',
    'max {count} calls': '最大 {count} 回の呼び出し',
    '{used} of {count} calls': '{used} / {count} 回呼び出し',
    'The most model turns of one attempt.':
      '1 回の試行でのモデルの最大ターン数。',
    'The most calls of DAG tools in one attempt.':
      '1 回の試行での DAG ツールの最大呼び出し回数。',
    'The most calls of DAG tools in one attempt. This is the default.':
      '1 回の試行での DAG ツールの最大呼び出し回数。これは既定値です。',
    'if {step} failed': '{step} が失敗した場合',
    'if {step} succeeded': '{step} が成功した場合',
    'if {step} exit code is {code}': '{step} の終了コードが {code} の場合',
    'if {step} exit code is not {code}':
      '{step} の終了コードが {code} 以外の場合',
    'if {step}: {test}': '{step}: {test} の場合',
    'Precondition: {text}': '事前条件: {text}',
    'continues on failure': '失敗しても続行',
    'continues if skipped': 'スキップ時も続行',
    'continues on exit code {codes}': '終了コード {codes} で続行',
    'continues on matching output': '出力が一致したら続行',
    'The run goes on with the steps after this one.':
      '実行はこのステップの後のステップへ進みます。',
    'The step then counts as a success.':
      'その場合、ステップは成功と見なされます。',
    'Runs only if:': '次の条件のときだけ実行:',
    Conditional: '条件付き',
    Empty: '空',
    'No prompt': 'プロンプトなし',
    'Add a prompt': 'プロンプトを追加',
    'Edit the prompt and the system message':
      'プロンプトとシステムメッセージを編集',
    Trusted: '信頼済み',
    'Untrusted: the key untrusted is true.':
      '信頼されていません: キー untrusted が true です。',
    'retry {used}/{limit}': '再試行 {used}/{limit}',
    'retry ×{limit}': '再試行 ×{limit}',
    'exit {codes}': '終了コード {codes}',
    'Retries the step up to {limit} times.':
      'このステップを最大 {limit} 回再試行します。',
    'Retries the step.': 'このステップを再試行します。',
    'Waits {interval} between the attempts.':
      '試行の間に {interval} 待ちます。',
    'Retries only after the exit code {codes}.':
      '終了コードが {codes} のときだけ再試行します。',
    'timeout {time}': 'タイムアウト {time}',
    '{count} secret': 'シークレット {count} 件',
    '{count} secrets': 'シークレット {count} 件',
    'output schema': '出力スキーマ',
    'Output schema': '出力スキーマ',
    'Output schema: {names}': '出力スキーマ: {names}',
    'Secrets: {names}': 'シークレット: {names}',
    'System: {line}': 'システムメッセージ: {line}',
    'Prompt: {line}': 'プロンプト: {line}',
    '{count} tool call': 'ツール呼び出し {count} 回',
    '{count} tool calls': 'ツール呼び出し {count} 回',
  },
});
