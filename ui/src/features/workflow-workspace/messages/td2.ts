// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Tools dock and the Skills dock: the search, the list with its sections,
// the detail area and the limits.
export default defineMessages({
  en: {
    'A catalog with more tools fails the step when it starts.':
      'A catalog with more tools fails the step when it starts.',
    'A longer result is cut before the model gets it.':
      'A longer result is cut before the model gets it.',
    'A server entry of the AI page. The coordinator keeps its URL and its credentials. You give the allowed tools after the add.':
      'A server entry of the AI page. The coordinator keeps its URL and its credentials. You give the allowed tools after the add.',
    'A skill that the list does not show.':
      'A skill that the list does not show.',
    'A tool is something that the model can call during the step.':
      'A tool is something that the model can call during the step.',
    'Add "{name}"': 'Add "{name}"',
    'Allow: {names}': 'Allow: {names}',
    'Available to add': 'Available to add',
    'Change a part of a file.': 'Change a part of a file.',
    'Choose a skill': 'Choose a skill',
    'Choose a tool': 'Choose a tool',
    'Connect a remote MCP server by its URL.':
      'Connect a remote MCP server by its URL.',
    'Connect a remote MCP server by its URL. You give the label, the URL and the allowed tools after the add.':
      'Connect a remote MCP server by its URL. You give the label, the URL and the allowed tools after the add.',
    'Create files and replace their content.':
      'Create files and replace their content.',
    'Deny: {names}': 'Deny: {names}',
    'Details of the skill': 'Details of the skill',
    'Details of the tool': 'Details of the tool',
    'Edit the limits': 'Edit the limits',
    'Every MCP server entry is in this step already.':
      'Every MCP server entry is in this step already.',
    'Every other DAG is a tool of this step already.':
      'Every other DAG is a tool of this step already.',
    'Every skill of the skills folder is in this step already.':
      'Every skill of the skills folder is in this step already.',
    Fixed: 'Fixed',
    'From the model': 'From the model',
    'Headers: {names}': 'Headers: {names}',
    'In this step': 'In this step',
    'Limits keep the catalog and the results small. An empty field uses the default.':
      'Limits keep the catalog and the results small. An empty field uses the default.',
    'Limits stop a run that loops or calls too much. An empty field uses the default.':
      'Limits stop a run that loops or calls too much. An empty field uses the default.',
    'No MCP server entry for this DAG. Add one on the AI page, or use a URL.':
      'No MCP server entry for this DAG. Add one on the AI page, or use a URL.',
    'No URL yet': 'No URL yet',
    'No allowed commands': 'No allowed commands',
    'No allowed tools': 'No allowed tools',
    'No description': 'No description',
    'No other DAG found. Create a DAG in the DAGs directory to use it as a tool.':
      'No other DAG found. Create a DAG in the DAGs directory to use it as a tool.',
    'No skill matches the search.': 'No skill matches the search.',
    'No skills yet. Pick a skill below.': 'No skills yet. Pick a skill below.',
    'No tools yet. The model has nothing to call. Pick a tool below.':
      'No tools yet. The model has nothing to call. Pick a tool below.',
    'Not declared by the DAG': 'Not declared by the DAG',
    'Not in the DAGs directory': 'Not in the DAGs directory',
    'Not in the skills folder': 'Not in the skills folder',
    'Not set up: give it a name and a URL.':
      'Not set up: give it a name and a URL.',
    'Open to the model': 'Open to the model',
    'Read the content of files in the working directory.':
      'Read the content of files in the working directory.',
    'Result size': 'Result size',
    'Run shell commands. Only the allowed commands run.':
      'Run shell commands. Only the allowed commands run.',
    'Search file names and file content.':
      'Search file names and file content.',
    'Search skills, or type a skill name':
      'Search skills, or type a skill name',
    'Search tools and servers': 'Search tools and servers',
    'Select a skill in the list to see its details here.':
      'Select a skill in the list to see its details here.',
    'Select a tool in the list to see its details here.':
      'Select a tool in the list to see its details here.',
    'Server entry of the AI page': 'Server entry of the AI page',
    'Show all tools': 'Show all tools',
    'Show the tools of the step {name}': 'Show the tools of the step {name}',
    Skill: 'Skill',
    'The label of the server starts the tool names that the model sees.':
      'The label of the server starts the tool names that the model sees.',
    'The list is empty. No tool of this server joins the tools of the model.':
      'The list is empty. No tool of this server joins the tools of the model.',
    'The model calls it {name}': 'The model calls it {name}',
    'The model does not see a fixed input. An input that the entry does not name stays open for the model.':
      'The model does not see a fixed input. An input that the entry does not name stays open for the model.',
    'The model reads: {text}': 'The model reads: {text}',
    'The server does not know the DAG "{name}". The step fails when it starts.':
      'The server does not know the DAG "{name}". The step fails when it starts.',
    'The server entry {name} of the AI page.':
      'The server entry {name} of the AI page.',
    'The shell tool needs a command pattern. You add it after the add.':
      'The shell tool needs a command pattern. You add it after the add.',
    'This DAG has no description.': 'This DAG has no description.',
    'This skill has no description.': 'This skill has no description.',
    'This tool takes no input.': 'This tool takes no input.',
    'Turn off {name}': 'Turn off {name}',
    Turns: 'Turns',
    'Up and Down to move, Enter to add, Delete to remove, / to search':
      'Up and Down to move, Enter to add, Delete to remove, / to search',
    'Up and Down to move, Enter to edit or add, Shift+Enter to add and keep the list, Delete to remove, / to search':
      'Up and Down to move, Enter to edit or add, Shift+Enter to add and keep the list, Delete to remove, / to search',
    'Up and Down to move, Enter to edit, / to search':
      'Up and Down to move, Enter to edit, / to search',
    'What the add writes': 'What the add writes',
    'YAML of the built-in tools': 'YAML of the built-in tools',
    'YAML of the entry': 'YAML of the entry',
    none: 'none',
    '{count} (default)': '{count} (default)',
    '{count} fixed': '{count} fixed',
    '{count} from the model': '{count} from the model',
    '{names} and {count} more': '{names} and {count} more',
  },
  'zh-CN': {
    'A catalog with more tools fails the step when it starts.':
      '工具数量超过上限时，步骤在启动时失败。',
    'A longer result is cut before the model gets it.':
      '过长的结果会在交给模型之前被截断。',
    'A server entry of the AI page. The coordinator keeps its URL and its credentials. You give the allowed tools after the add.':
      'AI 页面的服务器条目。协调器保存它的 URL 和凭据。添加后再填写允许的工具。',
    'A skill that the list does not show.': '列表中没有显示的技能。',
    'A tool is something that the model can call during the step.':
      '工具是模型在步骤运行期间可以调用的东西。',
    'Add "{name}"': '添加“{name}”',
    'Allow: {names}': '允许：{names}',
    'Available to add': '可添加',
    'Change a part of a file.': '修改文件的一部分。',
    'Choose a skill': '选择技能',
    'Choose a tool': '选择工具',
    'Connect a remote MCP server by its URL.': '通过 URL 连接远程 MCP 服务器。',
    'Connect a remote MCP server by its URL. You give the label, the URL and the allowed tools after the add.':
      '通过 URL 连接远程 MCP 服务器。添加后再填写标签、URL 和允许的工具。',
    'Create files and replace their content.': '创建文件并替换其内容。',
    'Deny: {names}': '拒绝：{names}',
    'Details of the skill': '技能详情',
    'Details of the tool': '工具详情',
    'Edit the limits': '修改限制',
    'Every MCP server entry is in this step already.':
      '所有 MCP 服务器条目都已在此步骤中。',
    'Every other DAG is a tool of this step already.':
      '其他所有 DAG 都已是此步骤的工具。',
    'Every skill of the skills folder is in this step already.':
      '技能文件夹中的所有技能都已在此步骤中。',
    Fixed: '固定',
    'From the model': '由模型决定',
    'Headers: {names}': '请求头：{names}',
    'In this step': '此步骤中',
    'Limits keep the catalog and the results small. An empty field uses the default.':
      '限制让工具目录和结果保持精简。留空则使用默认值。',
    'Limits stop a run that loops or calls too much. An empty field uses the default.':
      '限制会阻止循环或调用过多的运行。留空则使用默认值。',
    'No MCP server entry for this DAG. Add one on the AI page, or use a URL.':
      '此 DAG 没有可用的 MCP 服务器条目。请在 AI 页面添加，或使用 URL。',
    'No URL yet': '还没有 URL',
    'No allowed commands': '没有允许的命令',
    'No allowed tools': '没有允许的工具',
    'No description': '没有描述',
    'No other DAG found. Create a DAG in the DAGs directory to use it as a tool.':
      '没有找到其他 DAG。在 DAG 目录中创建一个 DAG，即可把它当作工具使用。',
    'No skill matches the search.': '没有匹配的技能。',
    'No skills yet. Pick a skill below.': '还没有技能。请在下方选择一个技能。',
    'No tools yet. The model has nothing to call. Pick a tool below.':
      '还没有工具。模型没有可调用的东西。请在下方选择一个工具。',
    'Not declared by the DAG': 'DAG 未声明',
    'Not in the DAGs directory': 'DAG 目录中没有',
    'Not in the skills folder': '不在技能文件夹中',
    'Not set up: give it a name and a URL.': '尚未设置：请填写名称和 URL。',
    'Open to the model': '交给模型',
    'Read the content of files in the working directory.':
      '读取工作目录中文件的内容。',
    'Result size': '结果大小',
    'Run shell commands. Only the allowed commands run.':
      '运行 shell 命令。只运行允许的命令。',
    'Search file names and file content.': '搜索文件名和文件内容。',
    'Search skills, or type a skill name': '搜索技能，或输入技能名称',
    'Search tools and servers': '搜索工具和服务器',
    'Select a skill in the list to see its details here.':
      '在列表中选择一个技能，即可在此查看详情。',
    'Select a tool in the list to see its details here.':
      '在列表中选择一个工具，即可在此查看详情。',
    'Server entry of the AI page': 'AI 页面的服务器条目',
    'Show all tools': '显示所有工具',
    'Show the tools of the step {name}': '显示步骤 {name} 的工具',
    Skill: '技能',
    'The label of the server starts the tool names that the model sees.':
      '服务器的标签是模型所见工具名称的开头。',
    'The list is empty. No tool of this server joins the tools of the model.':
      '列表为空。此服务器的任何工具都不会加入模型的工具。',
    'The model calls it {name}': '模型用 {name} 调用它',
    'The model does not see a fixed input. An input that the entry does not name stays open for the model.':
      '模型看不到固定输入。条目中没有列出的输入由模型决定。',
    'The model reads: {text}': '模型看到：{text}',
    'The server does not know the DAG "{name}". The step fails when it starts.':
      '服务器不认识 DAG“{name}”。步骤在启动时会失败。',
    'The server entry {name} of the AI page.': 'AI 页面的服务器条目 {name}。',
    'The shell tool needs a command pattern. You add it after the add.':
      'shell 工具需要命令模式。添加后再填写。',
    'This DAG has no description.': '此 DAG 没有描述。',
    'This skill has no description.': '此技能没有描述。',
    'This tool takes no input.': '此工具没有输入。',
    'Turn off {name}': '关闭 {name}',
    Turns: '轮数',
    'Up and Down to move, Enter to add, Delete to remove, / to search':
      '上下键移动，Enter 添加，Delete 删除，/ 搜索',
    'Up and Down to move, Enter to edit or add, Shift+Enter to add and keep the list, Delete to remove, / to search':
      '上下键移动，Enter 编辑或添加，Shift+Enter 添加并保留列表，Delete 删除，/ 搜索',
    'Up and Down to move, Enter to edit, / to search':
      '上下键移动，Enter 编辑，/ 搜索',
    'What the add writes': '添加会写入的内容',
    'YAML of the built-in tools': '内置工具的 YAML',
    'YAML of the entry': '条目的 YAML',
    none: '无',
    '{count} (default)': '{count}（默认）',
    '{count} fixed': '{count} 个固定',
    '{count} from the model': '{count} 个由模型决定',
    '{names} and {count} more': '{names} 等另外 {count} 个',
  },
  ja: {
    'A catalog with more tools fails the step when it starts.':
      'ツールがこれより多いと、ステップは開始時に失敗します。',
    'A longer result is cut before the model gets it.':
      'これより長い結果は、モデルに渡す前に切り詰められます。',
    'A server entry of the AI page. The coordinator keeps its URL and its credentials. You give the allowed tools after the add.':
      'AI ページのサーバーエントリです。URL と認証情報はコーディネーターが保持します。許可するツールは追加後に指定します。',
    'A skill that the list does not show.': '一覧にないスキルです。',
    'A tool is something that the model can call during the step.':
      'ツールは、ステップの実行中にモデルが呼び出せるものです。',
    'Add "{name}"': '「{name}」を追加',
    'Allow: {names}': '許可: {names}',
    'Available to add': '追加できるもの',
    'Change a part of a file.': 'ファイルの一部を変更します。',
    'Choose a skill': 'スキルを選択',
    'Choose a tool': 'ツールを選択',
    'Connect a remote MCP server by its URL.':
      'URL でリモートの MCP サーバーに接続します。',
    'Connect a remote MCP server by its URL. You give the label, the URL and the allowed tools after the add.':
      'URL でリモートの MCP サーバーに接続します。ラベル、URL、許可するツールは追加後に指定します。',
    'Create files and replace their content.':
      'ファイルを作成し、内容を置き換えます。',
    'Deny: {names}': '拒否: {names}',
    'Details of the skill': 'スキルの詳細',
    'Details of the tool': 'ツールの詳細',
    'Edit the limits': '制限を編集',
    'Every MCP server entry is in this step already.':
      'すべての MCP サーバーエントリがこのステップに入っています。',
    'Every other DAG is a tool of this step already.':
      '他のすべての DAG がこのステップのツールになっています。',
    'Every skill of the skills folder is in this step already.':
      'スキルフォルダのすべてのスキルがこのステップに入っています。',
    Fixed: '固定',
    'From the model': 'モデルが決める',
    'Headers: {names}': 'ヘッダー: {names}',
    'In this step': 'このステップ',
    'Limits keep the catalog and the results small. An empty field uses the default.':
      '制限により、ツールの一覧と結果を小さく保ちます。空欄の場合は既定値を使います。',
    'Limits stop a run that loops or calls too much. An empty field uses the default.':
      '制限は、ループや呼び出しが多すぎる実行を止めます。空欄の場合は既定値を使います。',
    'No MCP server entry for this DAG. Add one on the AI page, or use a URL.':
      'この DAG で使える MCP サーバーエントリはありません。AI ページで追加するか、URL を使ってください。',
    'No URL yet': 'URL がありません',
    'No allowed commands': '許可するコマンドがありません',
    'No allowed tools': '許可するツールがありません',
    'No description': '説明なし',
    'No other DAG found. Create a DAG in the DAGs directory to use it as a tool.':
      '他の DAG が見つかりません。DAG ディレクトリに DAG を作成すると、ツールとして使えます。',
    'No skill matches the search.': '一致するスキルがありません。',
    'No skills yet. Pick a skill below.':
      'スキルはまだありません。下からスキルを選んでください。',
    'No tools yet. The model has nothing to call. Pick a tool below.':
      'ツールはまだありません。モデルが呼び出せるものがありません。下からツールを選んでください。',
    'Not declared by the DAG': 'DAG で宣言されていません',
    'Not in the DAGs directory': 'DAG ディレクトリにありません',
    'Not in the skills folder': 'スキルフォルダにありません',
    'Not set up: give it a name and a URL.':
      '未設定です。名前と URL を指定してください。',
    'Open to the model': 'モデルに任せる',
    'Read the content of files in the working directory.':
      '作業ディレクトリのファイルの内容を読みます。',
    'Result size': '結果のサイズ',
    'Run shell commands. Only the allowed commands run.':
      'シェルコマンドを実行します。許可したコマンドだけが実行されます。',
    'Search file names and file content.':
      'ファイル名とファイルの内容を検索します。',
    'Search skills, or type a skill name': 'スキルを検索、またはスキル名を入力',
    'Search tools and servers': 'ツールとサーバーを検索',
    'Select a skill in the list to see its details here.':
      '一覧でスキルを選ぶと、ここに詳細が表示されます。',
    'Select a tool in the list to see its details here.':
      '一覧でツールを選ぶと、ここに詳細が表示されます。',
    'Server entry of the AI page': 'AI ページのサーバーエントリ',
    'Show all tools': 'すべてのツールを表示',
    'Show the tools of the step {name}': 'ステップ {name} のツールを表示',
    Skill: 'スキル',
    'The label of the server starts the tool names that the model sees.':
      'サーバーのラベルが、モデルに見えるツール名の先頭になります。',
    'The list is empty. No tool of this server joins the tools of the model.':
      '一覧が空です。このサーバーのツールはモデルのツールに加わりません。',
    'The model calls it {name}': 'モデルは {name} として呼び出します',
    'The model does not see a fixed input. An input that the entry does not name stays open for the model.':
      'モデルには固定入力が見えません。エントリに書かれていない入力は、モデルが決めます。',
    'The model reads: {text}': 'モデルが読む説明: {text}',
    'The server does not know the DAG "{name}". The step fails when it starts.':
      'サーバーは DAG「{name}」を知りません。ステップは開始時に失敗します。',
    'The server entry {name} of the AI page.':
      'AI ページのサーバーエントリ {name}。',
    'The shell tool needs a command pattern. You add it after the add.':
      'シェルツールにはコマンドパターンが必要です。追加後に指定します。',
    'This DAG has no description.': 'この DAG には説明がありません。',
    'This skill has no description.': 'このスキルには説明がありません。',
    'This tool takes no input.': 'このツールに入力はありません。',
    'Turn off {name}': '{name} をオフにする',
    Turns: 'ターン数',
    'Up and Down to move, Enter to add, Delete to remove, / to search':
      '上下キーで移動、Enter で追加、Delete で削除、/ で検索',
    'Up and Down to move, Enter to edit or add, Shift+Enter to add and keep the list, Delete to remove, / to search':
      '上下キーで移動、Enter で編集または追加、Shift+Enter で追加して一覧を保持、Delete で削除、/ で検索',
    'Up and Down to move, Enter to edit, / to search':
      '上下キーで移動、Enter で編集、/ で検索',
    'What the add writes': '追加で書き込まれる内容',
    'YAML of the built-in tools': '組み込みツールの YAML',
    'YAML of the entry': 'エントリの YAML',
    none: 'なし',
    '{count} (default)': '{count}（既定）',
    '{count} fixed': '固定 {count}',
    '{count} from the model': 'モデル決定 {count}',
    '{names} and {count} more': '{names} ほか {count} 件',
  },
});
