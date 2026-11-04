// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

export default defineMessages({
  en: {
    Calls: 'Calls',
    'Open the sub-run (Cmd/Ctrl+Click opens a new tab)':
      'Open the sub-run (Cmd/Ctrl+Click opens a new tab)',
    'Multiple executions: {count} sub-runs':
      'Multiple executions: {count} sub-runs',
    'Multiple executions: {visible} of {total} sub-runs':
      'Multiple executions: {visible} of {total} sub-runs',
    '{visible} of {total} sub-runs': '{visible} of {total} sub-runs',
    'No sub-runs match the selected filter':
      'No sub-runs match the selected filter',
    'Pack files into an archive.': 'Pack files into an archive.',
    'Unpack an archive into a folder.': 'Unpack an archive into a folder.',
    'List the files in an archive.': 'List the files in an archive.',
    'List the artifacts of the run.': 'List the artifacts of the run.',
    'Read an artifact of the run.': 'Read an artifact of the run.',
    'Save a file as an artifact of the run.':
      'Save a file as an artifact of the run.',
    'Copy a file or folder.': 'Copy a file or folder.',
    'Delete a file or folder.': 'Delete a file or folder.',
    'List the files in a folder.': 'List the files in a folder.',
    'Create a folder.': 'Create a folder.',
    'Move or rename a file or folder.': 'Move or rename a file or folder.',
    'Read the content of a file.': 'Read the content of a file.',
    'Read the size, type and times of a file.':
      'Read the size, type and times of a file.',
    'Write content to a file.': 'Write content to a file.',
    'Delete a key of the workflow state.':
      'Delete a key of the workflow state.',
    'Tell whether a value changed since the last run.':
      'Tell whether a value changed since the last run.',
    'Read a key of the workflow state.': 'Read a key of the workflow state.',
    'List the keys of the workflow state.':
      'List the keys of the workflow state.',
    'Store a value in the workflow state.':
      'Store a value in the workflow state.',
    'Wait for a length of time.': 'Wait for a length of time.',
    'Wait until a file exists, or is gone.':
      'Wait until a file exists, or is gone.',
    'Wait until a URL answers with the expected status.':
      'Wait until a URL answers with the expected status.',
    'Wait until a time of day or a date.':
      'Wait until a time of day or a date.',
    'Run a SQL query on PostgreSQL.': 'Run a SQL query on PostgreSQL.',
    'Import a data file into a PostgreSQL table.':
      'Import a data file into a PostgreSQL table.',
    'Run a SQL query on SQLite.': 'Run a SQL query on SQLite.',
    'Import a data file into a SQLite table.':
      'Import a data file into a SQLite table.',
    'Run a command in a container.': 'Run a command in a container.',
    'Run a command in a Docker container.':
      'Run a command in a Docker container.',
    'Run a command as a Kubernetes job.': 'Run a command as a Kubernetes job.',
    'Read a key from Redis.': 'Read a key from Redis.',
    'Store a value in Redis.': 'Store a value in Redis.',
    'Delete objects from S3.': 'Delete objects from S3.',
    'Download objects from S3.': 'Download objects from S3.',
    'List the objects in an S3 bucket.': 'List the objects in an S3 bucket.',
    'Upload files to S3.': 'Upload files to S3.',
    'Download files over SFTP.': 'Download files over SFTP.',
    'Upload files over SFTP.': 'Upload files over SFTP.',
    'Send an email.': 'Send an email.',
    'Ask a person to review or fill in a form.':
      'Ask a person to review or fill in a form.',
    'Publish values as outputs of the run.':
      'Publish values as outputs of the run.',
  },
  'zh-CN': {
    Calls: '调用',
    'Open the sub-run (Cmd/Ctrl+Click opens a new tab)':
      '打开子运行（Cmd/Ctrl+点击在新标签页中打开）',
    'Multiple executions: {count} sub-runs': '多次执行：{count} 个子运行',
    'Multiple executions: {visible} of {total} sub-runs':
      '多次执行：{total} 个子运行中的 {visible} 个',
    '{visible} of {total} sub-runs': '{total} 个子运行中的 {visible} 个',
    'No sub-runs match the selected filter': '没有符合所选筛选条件的子运行',
    'Pack files into an archive.': '将文件打包为归档。',
    'Unpack an archive into a folder.': '将归档解压到文件夹。',
    'List the files in an archive.': '列出归档中的文件。',
    'List the artifacts of the run.': '列出此次运行的工件。',
    'Read an artifact of the run.': '读取此次运行的工件。',
    'Save a file as an artifact of the run.': '将文件保存为此次运行的工件。',
    'Copy a file or folder.': '复制文件或文件夹。',
    'Delete a file or folder.': '删除文件或文件夹。',
    'List the files in a folder.': '列出文件夹中的文件。',
    'Create a folder.': '创建文件夹。',
    'Move or rename a file or folder.': '移动或重命名文件或文件夹。',
    'Read the content of a file.': '读取文件内容。',
    'Read the size, type and times of a file.': '读取文件的大小、类型和时间。',
    'Write content to a file.': '将内容写入文件。',
    'Delete a key of the workflow state.': '删除工作流状态中的键。',
    'Tell whether a value changed since the last run.':
      '判断某个值自上次运行以来是否变化。',
    'Read a key of the workflow state.': '读取工作流状态中的键。',
    'List the keys of the workflow state.': '列出工作流状态中的键。',
    'Store a value in the workflow state.': '在工作流状态中保存一个值。',
    'Wait for a length of time.': '等待一段时间。',
    'Wait until a file exists, or is gone.': '等待文件出现或消失。',
    'Wait until a URL answers with the expected status.':
      '等待 URL 返回预期的状态码。',
    'Wait until a time of day or a date.': '等待到某个时刻或日期。',
    'Run a SQL query on PostgreSQL.': '在 PostgreSQL 上执行 SQL 查询。',
    'Import a data file into a PostgreSQL table.':
      '将数据文件导入 PostgreSQL 表。',
    'Run a SQL query on SQLite.': '在 SQLite 上执行 SQL 查询。',
    'Import a data file into a SQLite table.': '将数据文件导入 SQLite 表。',
    'Run a command in a container.': '在容器中运行命令。',
    'Run a command in a Docker container.': '在 Docker 容器中运行命令。',
    'Run a command as a Kubernetes job.': '以 Kubernetes Job 运行命令。',
    'Read a key from Redis.': '从 Redis 读取键。',
    'Store a value in Redis.': '在 Redis 中保存一个值。',
    'Delete objects from S3.': '从 S3 删除对象。',
    'Download objects from S3.': '从 S3 下载对象。',
    'List the objects in an S3 bucket.': '列出 S3 存储桶中的对象。',
    'Upload files to S3.': '将文件上传到 S3。',
    'Download files over SFTP.': '通过 SFTP 下载文件。',
    'Upload files over SFTP.': '通过 SFTP 上传文件。',
    'Send an email.': '发送电子邮件。',
    'Ask a person to review or fill in a form.': '请人员审阅或填写表单。',
    'Publish values as outputs of the run.': '将值发布为此次运行的输出。',
  },
  ja: {
    Calls: '呼び出し先',
    'Open the sub-run (Cmd/Ctrl+Click opens a new tab)':
      'サブ実行を開く（Cmd/Ctrl+クリックで新しいタブ）',
    'Multiple executions: {count} sub-runs': '複数実行: {count} 件のサブ実行',
    'Multiple executions: {visible} of {total} sub-runs':
      '複数実行: {total} 件中 {visible} 件のサブ実行',
    '{visible} of {total} sub-runs': '{total} 件中 {visible} 件のサブ実行',
    'No sub-runs match the selected filter':
      '選択したフィルターに一致するサブ実行はありません',
    'Pack files into an archive.': 'ファイルをアーカイブにまとめます。',
    'Unpack an archive into a folder.': 'アーカイブをフォルダーに展開します。',
    'List the files in an archive.': 'アーカイブ内のファイルを一覧表示します。',
    'List the artifacts of the run.':
      '実行のアーティファクトを一覧表示します。',
    'Read an artifact of the run.': '実行のアーティファクトを読み取ります。',
    'Save a file as an artifact of the run.':
      'ファイルを実行のアーティファクトとして保存します。',
    'Copy a file or folder.': 'ファイルまたはフォルダーをコピーします。',
    'Delete a file or folder.': 'ファイルまたはフォルダーを削除します。',
    'List the files in a folder.': 'フォルダー内のファイルを一覧表示します。',
    'Create a folder.': 'フォルダーを作成します。',
    'Move or rename a file or folder.':
      'ファイルまたはフォルダーを移動、または名前を変更します。',
    'Read the content of a file.': 'ファイルの内容を読み取ります。',
    'Read the size, type and times of a file.':
      'ファイルのサイズ、種類、日時を読み取ります。',
    'Write content to a file.': 'ファイルに内容を書き込みます。',
    'Delete a key of the workflow state.':
      'ワークフローの状態のキーを削除します。',
    'Tell whether a value changed since the last run.':
      '前回の実行から値が変わったかどうかを判定します。',
    'Read a key of the workflow state.':
      'ワークフローの状態のキーを読み取ります。',
    'List the keys of the workflow state.':
      'ワークフローの状態のキーを一覧表示します。',
    'Store a value in the workflow state.':
      'ワークフローの状態に値を保存します。',
    'Wait for a length of time.': '一定の時間待機します。',
    'Wait until a file exists, or is gone.':
      'ファイルが現れるか、なくなるまで待機します。',
    'Wait until a URL answers with the expected status.':
      'URL が期待するステータスを返すまで待機します。',
    'Wait until a time of day or a date.':
      '指定した時刻または日付まで待機します。',
    'Run a SQL query on PostgreSQL.': 'PostgreSQL で SQL クエリを実行します。',
    'Import a data file into a PostgreSQL table.':
      'データファイルを PostgreSQL のテーブルに取り込みます。',
    'Run a SQL query on SQLite.': 'SQLite で SQL クエリを実行します。',
    'Import a data file into a SQLite table.':
      'データファイルを SQLite のテーブルに取り込みます。',
    'Run a command in a container.': 'コンテナーでコマンドを実行します。',
    'Run a command in a Docker container.':
      'Docker コンテナーでコマンドを実行します。',
    'Run a command as a Kubernetes job.':
      'Kubernetes の Job としてコマンドを実行します。',
    'Read a key from Redis.': 'Redis からキーを読み取ります。',
    'Store a value in Redis.': 'Redis に値を保存します。',
    'Delete objects from S3.': 'S3 からオブジェクトを削除します。',
    'Download objects from S3.': 'S3 からオブジェクトをダウンロードします。',
    'List the objects in an S3 bucket.':
      'S3 バケット内のオブジェクトを一覧表示します。',
    'Upload files to S3.': 'ファイルを S3 にアップロードします。',
    'Download files over SFTP.': 'SFTP でファイルをダウンロードします。',
    'Upload files over SFTP.': 'SFTP でファイルをアップロードします。',
    'Send an email.': 'メールを送信します。',
    'Ask a person to review or fill in a form.':
      '担当者にレビューやフォームの入力を依頼します。',
    'Publish values as outputs of the run.': '値を実行の出力として公開します。',
  },
});
