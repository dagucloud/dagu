// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

// The static messages of the skills: the Skills tab of the AI page and the skill
// editor. i18n/staticMessages.ts merges them.
export default defineMessages({
  en: {
    'No skill selected': 'No skill selected',
    AI: 'AI',
    'Revert changes': 'Revert changes',
    '"{name}" is reserved. Choose another name.':
      '"{name}" is reserved. Choose another name.',
    'MCP server': 'MCP server',
    'A description is required.': 'A description is required.',
    'Input name': 'Input name',
    'Remove input {name}': 'Remove input {name}',
    'Copy the step': 'Copy the step',
    'Use it in a chat step': 'Use it in a chat step',
    Skills: 'Skills',
    'New skill': 'New skill',
    'Search skills': 'Search skills',
    'No skills yet': 'No skills yet',
    'No skill matches "{query}"': 'No skill matches "{query}"',
    "Couldn't load the skills": "Couldn't load the skills",
    "Couldn't load the skill": "Couldn't load the skill",
    'Skill not found': 'Skill not found',
    'No skill has the name "{name}".': 'No skill has the name "{name}".',
    'Back to the skills': 'Back to the skills',
    'The skill was not saved.': 'The skill was not saved.',
    'The skill was not deleted.': 'The skill was not deleted.',
    'Delete skill': 'Delete skill',
    'Delete skill?': 'Delete skill?',
    'Delete "{name}"? A step that names this skill fails until you create it again.':
      'Delete "{name}"? A step that names this skill fails until you create it again.',
    'A skill named "{name}" exists.': 'A skill named "{name}" exists.',
    'A skill is a set of instructions. The model reads it only when it needs it.':
      'A skill is a set of instructions. The model reads it only when it needs it.',
    'Skill name': 'Skill name',
    'The name of the folder. You cannot change it after you save.':
      'The name of the folder. You cannot change it after you save.',
    'What the skill is for': 'What the skill is for',
    'Skills ({count})': 'Skills ({count})',
    'A file with this path exists.': 'A file with this path exists.',
    'A hidden file is not allowed. Only .gitkeep is.':
      'A hidden file is not allowed. Only .gitkeep is.',
    'A skill keeps its files in references/, assets/ and scripts/.':
      'A skill keeps its files in references/, assets/ and scripts/.',
    'A step cannot use this skill until you fix its errors.':
      'A step cannot use this skill until you fix its errors.',
    'Add a file': 'Add a file',
    'An estimate: the bytes of the body divided by 3.7. The format recommends {limit} tokens or fewer.':
      'An estimate: the bytes of the body divided by 3.7. The format recommends {limit} tokens or fewer.',
    'Copy my changes': 'Copy my changes',
    'Delete "{name}" from the skill "{skill}"?':
      'Delete "{name}" from the skill "{skill}"?',
    'Delete file?': 'Delete file?',
    'Delete the file': 'Delete the file',
    'Discard my changes and reload': 'Discard my changes and reload',
    'Download skill': 'Download skill',
    'Edit the front matter': 'Edit the front matter',
    'Enter a path.': 'Enter a path.',
    'Files ({count})': 'Files ({count})',
    'Files of the skill': 'Files of the skill',
    'It has an error.': 'It has an error.',
    'Keep my changes': 'Keep my changes',
    'Keep your text in the editor. The next save replaces this file on the server.':
      'Keep your text in the editor. The next save replaces this file on the server.',
    'Path in the skill': 'Path in the skill',
    'Rename the file': 'Rename the file',
    'SKILL.md is the file of the skill. Edit it in the editor.':
      'SKILL.md is the file of the skill. Edit it in the editor.',
    'Select all': 'Select all',
    'Select all skills': 'Select all skills',
    'Select skill {name}': 'Select skill {name}',
    'The download failed.': 'The download failed.',
    'The editor does not open this file. Download it to read it.':
      'The editor does not open this file. Download it to read it.',
    'The file could not be downloaded.': 'The file could not be downloaded.',
    'The file could not be read.': 'The file could not be read.',
    'The file has {size}. The limit is {limit}.':
      'The file has {size}. The limit is {limit}.',
    'The file was not deleted.': 'The file was not deleted.',
    'The file was not saved.': 'The file was not saved.',
    'The format recommends {limit} lines or fewer.':
      'The format recommends {limit} lines or fewer.',
    'The new file {to} exists, but the old file {from} was not deleted: {message} Delete {from} by hand, or restore it.':
      'The new file {to} exists, but the old file {from} was not deleted: {message} Delete {from} by hand, or restore it.',
    'The path is too long.': 'The path is too long.',
    'The path must start with references/, assets/ or scripts/.':
      'The path must start with references/, assets/ or scripts/.',
    'The server refused the save': 'The server refused the save',
    'The server refuses this skill: {reason}':
      'The server refuses this skill: {reason}',
    'The server writes the new file, then deletes the old one.':
      'The server writes the new file, then deletes the old one.',
    'The skill changed on the server.': 'The skill changed on the server.',
    'The skill folder': 'The skill folder',
    'The skill no longer exists on the server.':
      'The skill no longer exists on the server.',
    Upload: 'Upload',
    'Upload a file': 'Upload a file',
    'Use / between folders. A part cannot be empty, . or .., and the path cannot start with /.':
      'Use / between folders. A part cannot be empty, . or .., and the path cannot start with /.',
    'Use at most {max} characters.': 'Use at most {max} characters.',
    'Use lowercase letters and digits. Join words with one -. Do not start or end with -.':
      'Use lowercase letters and digits. Join words with one -. Do not start or end with -.',
    Validation: 'Validation',
    'Your changes are still in the editor. Nothing was saved.':
      'Your changes are still in the editor. Nothing was saved.',
    'Your changes are still in the editor. Nothing was saved. Version on the server: {hash}.':
      'Your changes are still in the editor. Nothing was saved. Version on the server: {hash}.',
    'about {count} tokens': 'about {count} tokens',
    'and {count} more': 'and {count} more',
    '{count} selected': '{count} selected',
    '{count} lines': '{count} lines',
  },
  'zh-CN': {
    'No skill selected': '未选择技能',
    AI: 'AI',
    'Revert changes': '撤销更改',
    '"{name}" is reserved. Choose another name.':
      '“{name}”是保留名称。请选择其他名称。',
    'MCP server': 'MCP 服务器',
    'A description is required.': '必须填写说明。',
    'Input name': '输入名称',
    'Remove input {name}': '删除输入 {name}',
    'Copy the step': '复制步骤',
    'Use it in a chat step': '在聊天步骤中使用',
    Skills: '技能',
    'New skill': '新建技能',
    'Search skills': '搜索技能',
    'No skills yet': '还没有技能',
    'No skill matches "{query}"': '没有与“{query}”匹配的技能',
    "Couldn't load the skills": '无法加载技能',
    "Couldn't load the skill": '无法加载该技能',
    'Skill not found': '未找到技能',
    'No skill has the name "{name}".': '没有名为“{name}”的技能。',
    'Back to the skills': '返回技能列表',
    'The skill was not saved.': '技能未保存。',
    'The skill was not deleted.': '技能未删除。',
    'Delete skill': '删除技能',
    'Delete skill?': '删除技能？',
    'Delete "{name}"? A step that names this skill fails until you create it again.':
      '删除“{name}”？在重新创建之前，引用该技能的步骤会失败。',
    'A skill named "{name}" exists.': '已存在名为“{name}”的技能。',
    'A skill is a set of instructions. The model reads it only when it needs it.':
      '技能是一组指令。模型只在需要时才读取它。',
    'Skill name': '技能名称',
    'The name of the folder. You cannot change it after you save.':
      '文件夹的名称。保存后不能更改。',
    'What the skill is for': '技能的用途',
    'Skills ({count})': '技能（{count}）',
    'A file with this path exists.': '已存在相同路径的文件。',
    'A hidden file is not allowed. Only .gitkeep is.':
      '不允许隐藏文件。只允许 .gitkeep。',
    'A skill keeps its files in references/, assets/ and scripts/.':
      '技能的文件放在 references/、assets/ 和 scripts/ 中。',
    'A step cannot use this skill until you fix its errors.':
      '在修复错误之前，步骤无法使用此技能。',
    'Add a file': '添加文件',
    'An estimate: the bytes of the body divided by 3.7. The format recommends {limit} tokens or fewer.':
      '估算值：正文字节数除以 3.7。格式建议不超过 {limit} 个 token。',
    'Copy my changes': '复制我的更改',
    'Delete "{name}" from the skill "{skill}"?':
      '从技能“{skill}”中删除“{name}”？',
    'Delete file?': '删除文件？',
    'Delete the file': '删除文件',
    'Discard my changes and reload': '放弃我的更改并重新加载',
    'Download skill': '下载技能',
    'Edit the front matter': '编辑前置元数据',
    'Enter a path.': '请输入路径。',
    'Files ({count})': '文件（{count}）',
    'Files of the skill': '技能的文件',
    'It has an error.': '它有错误。',
    'Keep my changes': '保留我的更改',
    'Keep your text in the editor. The next save replaces this file on the server.':
      '将你的文本保留在编辑器中。下次保存会替换服务器上的此文件。',
    'Path in the skill': '技能内的路径',
    'Rename the file': '重命名文件',
    'SKILL.md is the file of the skill. Edit it in the editor.':
      'SKILL.md 是技能的主文件。请在编辑器中编辑它。',
    'Select all': '全选',
    'Select all skills': '选择所有技能',
    'Select skill {name}': '选择技能 {name}',
    'The download failed.': '下载失败。',
    'The editor does not open this file. Download it to read it.':
      '编辑器无法打开此文件。请下载后阅读。',
    'The file could not be downloaded.': '无法下载该文件。',
    'The file could not be read.': '无法读取该文件。',
    'The file has {size}. The limit is {limit}.':
      '文件大小为 {size}。上限为 {limit}。',
    'The file was not deleted.': '文件未删除。',
    'The file was not saved.': '文件未保存。',
    'The format recommends {limit} lines or fewer.':
      '格式建议不超过 {limit} 行。',
    'The new file {to} exists, but the old file {from} was not deleted: {message} Delete {from} by hand, or restore it.':
      '新文件 {to} 已存在，但旧文件 {from} 未删除：{message} 请手动删除 {from}，或将其恢复。',
    'The path is too long.': '路径太长。',
    'The path must start with references/, assets/ or scripts/.':
      '路径必须以 references/、assets/ 或 scripts/ 开头。',
    'The server refused the save': '服务器拒绝了此次保存',
    'The server refuses this skill: {reason}': '服务器拒绝此技能：{reason}',
    'The server writes the new file, then deletes the old one.':
      '服务器先写入新文件，再删除旧文件。',
    'The skill changed on the server.': '技能在服务器上已更改。',
    'The skill folder': '技能文件夹',
    'The skill no longer exists on the server.': '该技能在服务器上已不存在。',
    Upload: '上传',
    'Upload a file': '上传文件',
    'Use / between folders. A part cannot be empty, . or .., and the path cannot start with /.':
      '文件夹之间用 / 分隔。每一段不能为空、. 或 ..，路径不能以 / 开头。',
    'Use at most {max} characters.': '最多使用 {max} 个字符。',
    'Use lowercase letters and digits. Join words with one -. Do not start or end with -.':
      '使用小写字母和数字。单词之间用一个 - 连接。不要以 - 开头或结尾。',
    Validation: '校验',
    'Your changes are still in the editor. Nothing was saved.':
      '你的更改仍在编辑器中。未保存任何内容。',
    'Your changes are still in the editor. Nothing was saved. Version on the server: {hash}.':
      '你的更改仍在编辑器中。未保存任何内容。服务器上的版本：{hash}。',
    'about {count} tokens': '约 {count} 个 token',
    'and {count} more': '另有 {count} 项',
    '{count} selected': '已选择 {count} 项',
    '{count} lines': '{count} 行',
  },
  ja: {
    'No skill selected': 'スキルが選択されていません',
    AI: 'AI',
    'Revert changes': '変更を元に戻す',
    '"{name}" is reserved. Choose another name.':
      '「{name}」は予約されています。別の名前を選んでください。',
    'MCP server': 'MCP サーバー',
    'A description is required.': '説明は必須です。',
    'Input name': '入力名',
    'Remove input {name}': '入力 {name} を削除',
    'Copy the step': 'ステップをコピー',
    'Use it in a chat step': 'チャットステップで使う',
    Skills: 'スキル',
    'New skill': '新しいスキル',
    'Search skills': 'スキルを検索',
    'No skills yet': 'スキルはまだありません',
    'No skill matches "{query}"': '「{query}」に一致するスキルはありません',
    "Couldn't load the skills": 'スキルを読み込めませんでした',
    "Couldn't load the skill": 'スキルを読み込めませんでした',
    'Skill not found': 'スキルが見つかりません',
    'No skill has the name "{name}".':
      '「{name}」という名前のスキルはありません。',
    'Back to the skills': 'スキル一覧に戻る',
    'The skill was not saved.': 'スキルは保存されませんでした。',
    'The skill was not deleted.': 'スキルは削除されませんでした。',
    'Delete skill': 'スキルを削除',
    'Delete skill?': 'スキルを削除しますか？',
    'Delete "{name}"? A step that names this skill fails until you create it again.':
      '「{name}」を削除しますか？再作成するまで、このスキルを指定するステップは失敗します。',
    'A skill named "{name}" exists.':
      '「{name}」という名前のスキルが既にあります。',
    'A skill is a set of instructions. The model reads it only when it needs it.':
      'スキルは指示のまとまりです。モデルは必要なときだけ読み込みます。',
    'Skill name': 'スキル名',
    'The name of the folder. You cannot change it after you save.':
      'フォルダー名です。保存後は変更できません。',
    'What the skill is for': 'スキルの用途',
    'Skills ({count})': 'スキル（{count} 件）',
    'A file with this path exists.': 'このパスのファイルが既にあります。',
    'A hidden file is not allowed. Only .gitkeep is.':
      '隠しファイルは使えません。使えるのは .gitkeep だけです。',
    'A skill keeps its files in references/, assets/ and scripts/.':
      'スキルのファイルは references/、assets/、scripts/ に置きます。',
    'A step cannot use this skill until you fix its errors.':
      'エラーを直すまで、ステップはこのスキルを使えません。',
    'Add a file': 'ファイルを追加',
    'An estimate: the bytes of the body divided by 3.7. The format recommends {limit} tokens or fewer.':
      '推定値: 本文のバイト数を 3.7 で割った値です。形式は {limit} トークン以下を推奨しています。',
    'Copy my changes': '自分の変更をコピー',
    'Delete "{name}" from the skill "{skill}"?':
      'スキル「{skill}」から「{name}」を削除しますか？',
    'Delete file?': 'ファイルを削除しますか？',
    'Delete the file': 'ファイルを削除',
    'Discard my changes and reload': '自分の変更を破棄して再読み込み',
    'Download skill': 'スキルをダウンロード',
    'Edit the front matter': 'フロントマターを編集',
    'Enter a path.': 'パスを入力してください。',
    'Files ({count})': 'ファイル（{count} 件）',
    'Files of the skill': 'スキルのファイル',
    'It has an error.': 'エラーがあります。',
    'Keep my changes': '自分の変更を残す',
    'Keep your text in the editor. The next save replaces this file on the server.':
      'テキストをエディターに残します。次の保存でサーバー上のこのファイルが置き換わります。',
    'Path in the skill': 'スキル内のパス',
    'Rename the file': 'ファイル名を変更',
    'SKILL.md is the file of the skill. Edit it in the editor.':
      'SKILL.md はスキルの主ファイルです。エディターで編集してください。',
    'Select all': 'すべて選択',
    'Select all skills': 'すべてのスキルを選択',
    'Select skill {name}': 'スキル {name} を選択',
    'The download failed.': 'ダウンロードに失敗しました。',
    'The editor does not open this file. Download it to read it.':
      'エディターはこのファイルを開けません。ダウンロードして読んでください。',
    'The file could not be downloaded.':
      'ファイルをダウンロードできませんでした。',
    'The file could not be read.': 'ファイルを読み込めませんでした。',
    'The file has {size}. The limit is {limit}.':
      'ファイルは {size} です。上限は {limit} です。',
    'The file was not deleted.': 'ファイルは削除されませんでした。',
    'The file was not saved.': 'ファイルは保存されませんでした。',
    'The format recommends {limit} lines or fewer.':
      '形式は {limit} 行以下を推奨しています。',
    'The new file {to} exists, but the old file {from} was not deleted: {message} Delete {from} by hand, or restore it.':
      '新しいファイル {to} はありますが、古いファイル {from} は削除されませんでした: {message} {from} を手動で削除するか、元に戻してください。',
    'The path is too long.': 'パスが長すぎます。',
    'The path must start with references/, assets/ or scripts/.':
      'パスは references/、assets/、scripts/ のいずれかで始めてください。',
    'The server refused the save': 'サーバーは保存を拒否しました',
    'The server refuses this skill: {reason}':
      'サーバーはこのスキルを拒否します: {reason}',
    'The server writes the new file, then deletes the old one.':
      'サーバーは新しいファイルを書き込み、その後に古いファイルを削除します。',
    'The skill changed on the server.': 'スキルがサーバー上で変更されました。',
    'The skill folder': 'スキルのフォルダー',
    'The skill no longer exists on the server.':
      'このスキルはサーバー上にもうありません。',
    Upload: 'アップロード',
    'Upload a file': 'ファイルをアップロード',
    'Use / between folders. A part cannot be empty, . or .., and the path cannot start with /.':
      'フォルダーは / で区切ります。各部分は空、. 、.. にできず、パスは / で始められません。',
    'Use at most {max} characters.': '{max} 文字以内にしてください。',
    'Use lowercase letters and digits. Join words with one -. Do not start or end with -.':
      '小文字の英字と数字を使います。単語は - 1 つでつなぎます。先頭と末尾に - は使えません。',
    Validation: '検証',
    'Your changes are still in the editor. Nothing was saved.':
      '変更はエディターに残っています。何も保存されていません。',
    'Your changes are still in the editor. Nothing was saved. Version on the server: {hash}.':
      '変更はエディターに残っています。何も保存されていません。サーバー上のバージョン: {hash}。',
    'about {count} tokens': '約 {count} トークン',
    'and {count} more': 'ほか {count} 件',
    '{count} selected': '{count} 件を選択',
    '{count} lines': '{count} 行',
  },
});
