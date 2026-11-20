// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The kinds of tools and the tool panel: the badges, the groups, the tooltips
// and the editor of one tool entry in the right panel.
export default defineMessages({
  en: {
    'Allowed tools': 'Allowed tools',
    'Back to step {name}': 'Back to step {name}',
    'Built-in': 'Built-in',
    'Built-in tool': 'Built-in tool',
    'DAG tool': 'DAG tool',
    'DAG tools': 'DAG tools',
    'DAG: {name}': 'DAG: {name}',
    'All built-in tools are enabled.': 'All built-in tools are enabled.',
    'Loading DAGs...': 'Loading DAGs...',
    'No other DAG found.': 'No other DAG found.',
    'No tool matches the search.': 'No tool matches the search.',
    'No tool to add.': 'No tool to add.',
    'Valid on {steps} steps only.': 'Valid on {steps} steps only.',
    'MCP servers': 'MCP servers',
    'Only the tools in this list join the tools of the model. The list cannot be empty.':
      'Only the tools in this list join the tools of the model. The list cannot be empty.',
    'Server: {host}': 'Server: {host}',
    'Show the step {name}': 'Show the step {name}',
    'The DAG that the tool runs.': 'The DAG that the tool runs.',
    'The label of the server. It starts the tool names that the model sees.':
      'The label of the server. It starts the tool names that the model sees.',
    'The model decides every input of this tool.':
      'The model decides every input of this tool.',
    'The tool entry must be a YAML mapping.':
      'The tool entry must be a YAML mapping.',
    'This tool no longer exists.': 'This tool no longer exists.',
    Tool: 'Tool',
    'Tool YAML': 'Tool YAML',
    'All kinds': 'All kinds',
    'Gives a tool of the CLI of the agent.':
      'Gives a tool of the CLI of the agent.',
    'Kind of tool': 'Kind of tool',
    'Runs another workflow. The model gives it the inputs.':
      'Runs another workflow. The model gives it the inputs.',
  },
  'zh-CN': {
    'Allowed tools': '允许的工具',
    'Back to step {name}': '返回步骤 {name}',
    'Built-in': '内置',
    'Built-in tool': '内置工具',
    'DAG tool': 'DAG 工具',
    'DAG tools': 'DAG 工具',
    'DAG: {name}': 'DAG：{name}',
    'All built-in tools are enabled.': '所有内置工具都已启用。',
    'Loading DAGs...': '正在加载 DAG...',
    'No other DAG found.': '没有找到其他 DAG。',
    'No tool matches the search.': '没有与搜索匹配的工具。',
    'No tool to add.': '没有可添加的工具。',
    'Valid on {steps} steps only.': '仅适用于 {steps} 步骤。',
    'MCP servers': 'MCP 服务器',
    'Only the tools in this list join the tools of the model. The list cannot be empty.':
      '只有此列表中的工具会提供给模型。列表不能为空。',
    'Server: {host}': '服务器：{host}',
    'Show the step {name}': '显示步骤 {name}',
    'The DAG that the tool runs.': '此工具运行的 DAG。',
    'The label of the server. It starts the tool names that the model sees.':
      '服务器的标签。它是模型所见工具名称的开头。',
    'The model decides every input of this tool.':
      '此工具的每个输入都由模型决定。',
    'The tool entry must be a YAML mapping.': '工具条目必须是 YAML 映射。',
    'This tool no longer exists.': '此工具已不存在。',
    Tool: '工具',
    'Tool YAML': '工具 YAML',
    'All kinds': '所有类型',
    'Gives a tool of the CLI of the agent.': '提供智能体 CLI 的一个工具。',
    'Kind of tool': '工具类型',
    'Runs another workflow. The model gives it the inputs.':
      '运行另一个工作流。输入由模型提供。',
  },
  ja: {
    'Allowed tools': '許可するツール',
    'Back to step {name}': 'ステップ {name} に戻る',
    'Built-in': '組み込み',
    'Built-in tool': '組み込みツール',
    'DAG tool': 'DAG ツール',
    'DAG tools': 'DAG ツール',
    'DAG: {name}': 'DAG 名: {name}',
    'All built-in tools are enabled.': 'すべての組み込みツールが有効です。',
    'Loading DAGs...': 'DAG を読み込み中...',
    'No other DAG found.': '他の DAG は見つかりません。',
    'No tool matches the search.': '検索に一致するツールはありません。',
    'No tool to add.': '追加できるツールはありません。',
    'Valid on {steps} steps only.': '{steps} ステップでのみ有効です。',
    'MCP servers': 'MCP サーバー',
    'Only the tools in this list join the tools of the model. The list cannot be empty.':
      'このリストのツールだけがモデルに渡されます。リストは空にできません。',
    'Server: {host}': 'サーバー: {host}',
    'Show the step {name}': 'ステップ {name} を表示',
    'The DAG that the tool runs.': 'このツールが実行する DAG。',
    'The label of the server. It starts the tool names that the model sees.':
      'サーバーのラベル。モデルに見えるツール名の先頭になります。',
    'The model decides every input of this tool.':
      'このツールのすべての入力をモデルが決めます。',
    'The tool entry must be a YAML mapping.':
      'ツールエントリは YAML のマッピングである必要があります。',
    'This tool no longer exists.': 'このツールはもう存在しません。',
    Tool: 'ツール',
    'Tool YAML': 'ツールの YAML',
    'All kinds': 'すべての種類',
    'Gives a tool of the CLI of the agent.':
      'エージェントの CLI のツールを提供します。',
    'Kind of tool': 'ツールの種類',
    'Runs another workflow. The model gives it the inputs.':
      '別のワークフローを実行します。入力はモデルが渡します。',
  },
});
