// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

// The static messages of the MCP tab of the AI page and of the named MCP servers
// of the tool picker. i18n/staticMessages.ts merges them.
export default defineMessages({
  en: {
    "Couldn't load the MCP servers": "Couldn't load the MCP servers",
    'A MCP server entry holds a URL and credentials. A step names it, and the coordinator keeps the credentials.':
      'A MCP server entry holds a URL and credentials. A step names it, and the coordinator keeps the credentials.',
    'A MCP server entry holds a URL and credentials. A step names it. The step never sees the credentials.':
      'A MCP server entry holds a URL and credentials. A step names it. The step never sees the credentials.',
    'A MCP server entry of the AI page. The tools of the server start with its name.':
      'A MCP server entry of the AI page. The tools of the server start with its name.',
    'A MCP server with this name exists in this scope.':
      'A MCP server with this name exists in this scope.',
    'A header name starts with a letter. Use letters, digits and -.':
      'A header name starts with a letter. Use letters, digits and -.',
    'A step of a DAG in this list can use the MCP server.':
      'A step of a DAG in this list can use the MCP server.',
    'A step writes this name in its mcp entry, with no url. You cannot change it after you save.':
      'A step writes this name in its mcp entry, with no url. You cannot change it after you save.',
    'A token is the header authorization. Remove one of them.':
      'A token is the header authorization. Remove one of them.',
    'Add MCP server': 'Add MCP server',
    'Add a MCP server': 'Add a MCP server',
    'Add at least one header, or choose another form.':
      'Add at least one header, or choose another form.',
    'An entry has at most 16 headers.': 'An entry has at most 16 headers.',
    'Bearer token': 'Bearer token',
    Credentials: 'Credentials',
    'Dagu sets this header. Use another name.':
      'Dagu sets this header. Use another name.',
    'Delete "{name}"? A step that names this MCP server fails until you create it again. The credentials are deleted too.':
      'Delete "{name}"? A step that names this MCP server fails until you create it again. The credentials are deleted too.',
    'Delete MCP server {name}': 'Delete MCP server {name}',
    'Delete MCP server?': 'Delete MCP server?',
    'Give the URL of the MCP server.': 'Give the URL of the MCP server.',
    'Give the token.': 'Give the token.',
    'Give the value of the header.': 'Give the value of the header.',
    'Gives the tools of a remote MCP server. Use a server of the AI page, or a URL.':
      'Gives the tools of a remote MCP server. Use a server of the AI page, or a URL.',
    'Header name {number}': 'Header name {number}',
    'Header value {number}': 'Header value {number}',
    'MCP server entry: {name}': 'MCP server entry: {name}',
    'New MCP server with a URL': 'New MCP server with a URL',
    'No MCP servers yet': 'No MCP servers yet',
    'No credential': 'No credential',
    'Only an administrator can manage the MCP servers.':
      'Only an administrator can manage the MCP servers.',
    'Open the MCP servers': 'Open the MCP servers',
    'Remove header {number}': 'Remove header {number}',
    'Rotate credentials': 'Rotate credentials',
    'Rotate the credentials of {name}': 'Rotate the credentials of {name}',
    'The MCP server does not exist. Reload the page.':
      'The MCP server does not exist. Reload the page.',
    'The MCP server was not deleted.': 'The MCP server was not deleted.',
    'The MCP server was not saved.': 'The MCP server was not saved.',
    'The URL must not have user information or a fragment.':
      'The URL must not have user information or a fragment.',
    'The URL must start with https or http.':
      'The URL must start with https or http.',
    'The coordinator calls this server with the credentials of the entry. The step never sees them.':
      'The coordinator calls this server with the credentials of the entry. The step never sees them.',
    'The coordinator sends it as authorization: Bearer. The server never shows it again. It shows the first characters and a fingerprint.':
      'The coordinator sends it as authorization: Bearer. The server never shows it again. It shows the first characters and a fingerprint.',
    'The coordinator sends these headers to the server. The server never shows a value again.':
      'The coordinator sends these headers to the server. The server never shows a value again.',
    'The credentials were not saved.': 'The credentials were not saved.',
    'The entry will have no credential.': 'The entry will have no credential.',
    'The name dagu is the name of the tool bridge. Use another name.':
      'The name dagu is the name of the tool bridge. Use another name.',
    'The name of a MCP server entry of the AI page. The tools of the server start with it.':
      'The name of a MCP server entry of the AI page. The tools of the server start with it.',
    'The new credentials replace all the old credentials. Give every value again. The server never shows a value after you save.':
      'The new credentials replace all the old credentials. Give every value again. The server never shows a value after you save.',
    'The server does not know the MCP server "{name}", or this DAG may not use it. The step fails when it starts.':
      'The server does not know the MCP server "{name}", or this DAG may not use it. The step fails when it starts.',
    'The server does not store MCP servers.':
      'The server does not store MCP servers.',
    'The streamable HTTP address of the MCP server. The coordinator calls it, not the worker.':
      'The streamable HTTP address of the MCP server. The coordinator calls it, not the worker.',
    'The value has a control character. Paste it again.':
      'The value has a control character. Paste it again.',
    'The value is empty.': 'The value is empty.',
    'The value is too long. The limit is 8192 bytes.':
      'The value is too long. The limit is 8192 bytes.',
    'This URL uses http. The credentials go over the network with no encryption.':
      'This URL uses http. The credentials go over the network with no encryption.',
    'This header appears twice.': 'This header appears twice.',
    'To change the credentials, use "Rotate credentials" in the list.':
      'To change the credentials, use "Rotate credentials" in the list.',
  },
  'zh-CN': {
    "Couldn't load the MCP servers": '无法加载 MCP 服务器',
    'A MCP server entry holds a URL and credentials. A step names it, and the coordinator keeps the credentials.':
      'MCP 服务器条目保存 URL 和凭据。步骤只写出它的名称，凭据由协调器保管。',
    'A MCP server entry holds a URL and credentials. A step names it. The step never sees the credentials.':
      'MCP 服务器条目保存 URL 和凭据。步骤只写出它的名称，永远看不到凭据。',
    'A MCP server entry of the AI page. The tools of the server start with its name.':
      'AI 页面的 MCP 服务器条目。该服务器的工具名以它的名称开头。',
    'A MCP server with this name exists in this scope.':
      '此范围内已有同名的 MCP 服务器。',
    'A header name starts with a letter. Use letters, digits and -.':
      '标头名称以字母开头。请使用字母、数字和 -。',
    'A step of a DAG in this list can use the MCP server.':
      '此列表中 DAG 的步骤可以使用该 MCP 服务器。',
    'A step writes this name in its mcp entry, with no url. You cannot change it after you save.':
      '步骤把这个名称写在没有 url 的 mcp 条目中。保存后不能更改。',
    'A token is the header authorization. Remove one of them.':
      '令牌就是标头 authorization。请删除其中一个。',
    'Add MCP server': '添加 MCP 服务器',
    'Add a MCP server': '添加 MCP 服务器',
    'Add at least one header, or choose another form.':
      '请至少添加一个标头，或选择其他形式。',
    'An entry has at most 16 headers.': '一个条目最多有 16 个标头。',
    'Bearer token': 'Bearer 令牌',
    Credentials: '凭据',
    'Dagu sets this header. Use another name.':
      '此标头由 Dagu 设置。请使用其他名称。',
    'Delete "{name}"? A step that names this MCP server fails until you create it again. The credentials are deleted too.':
      '删除“{name}”？在重新创建之前，引用此 MCP 服务器的步骤会失败。凭据也会一并删除。',
    'Delete MCP server {name}': '删除 MCP 服务器 {name}',
    'Delete MCP server?': '删除 MCP 服务器？',
    'Give the URL of the MCP server.': '请填写 MCP 服务器的 URL。',
    'Give the token.': '请填写令牌。',
    'Give the value of the header.': '请填写标头的值。',
    'Gives the tools of a remote MCP server. Use a server of the AI page, or a URL.':
      '提供远程 MCP 服务器的工具。使用 AI 页面的服务器，或使用 URL。',
    'Header name {number}': '标头名称 {number}',
    'Header value {number}': '标头值 {number}',
    'MCP server entry: {name}': 'MCP 服务器条目：{name}',
    'New MCP server with a URL': '使用 URL 的新 MCP 服务器',
    'No MCP servers yet': '还没有 MCP 服务器',
    'No credential': '无凭据',
    'Only an administrator can manage the MCP servers.':
      '只有管理员可以管理 MCP 服务器。',
    'Open the MCP servers': '打开 MCP 服务器',
    'Remove header {number}': '删除标头 {number}',
    'Rotate credentials': '轮换凭据',
    'Rotate the credentials of {name}': '轮换 {name} 的凭据',
    'The MCP server does not exist. Reload the page.':
      '该 MCP 服务器不存在。请重新加载页面。',
    'The MCP server was not deleted.': '未能删除 MCP 服务器。',
    'The MCP server was not saved.': '未能保存 MCP 服务器。',
    'The URL must not have user information or a fragment.':
      'URL 不能包含用户信息或片段。',
    'The URL must start with https or http.': 'URL 必须以 https 或 http 开头。',
    'The coordinator calls this server with the credentials of the entry. The step never sees them.':
      '协调器使用该条目的凭据调用此服务器。步骤永远看不到凭据。',
    'The coordinator sends it as authorization: Bearer. The server never shows it again. It shows the first characters and a fingerprint.':
      '协调器把它作为 authorization: Bearer 发送。服务器不会再次显示它，只显示前几个字符和指纹。',
    'The coordinator sends these headers to the server. The server never shows a value again.':
      '协调器把这些标头发送给服务器。服务器不会再次显示任何值。',
    'The credentials were not saved.': '未能保存凭据。',
    'The entry will have no credential.': '该条目将没有任何凭据。',
    'The name dagu is the name of the tool bridge. Use another name.':
      '名称 dagu 是工具桥的名称。请使用其他名称。',
    'The name of a MCP server entry of the AI page. The tools of the server start with it.':
      'AI 页面的 MCP 服务器条目名称。该服务器的工具名以它开头。',
    'The new credentials replace all the old credentials. Give every value again. The server never shows a value after you save.':
      '新凭据会替换所有旧凭据。请重新填写每个值。保存后服务器不会再显示任何值。',
    'The server does not know the MCP server "{name}", or this DAG may not use it. The step fails when it starts.':
      '服务器不认识 MCP 服务器“{name}”，或者此 DAG 不能使用它。步骤启动时会失败。',
    'The server does not store MCP servers.': '服务器不保存 MCP 服务器。',
    'The streamable HTTP address of the MCP server. The coordinator calls it, not the worker.':
      'MCP 服务器的 streamable HTTP 地址。由协调器调用它，而不是工作节点。',
    'The value has a control character. Paste it again.':
      '该值包含控制字符。请重新粘贴。',
    'The value is empty.': '该值为空。',
    'The value is too long. The limit is 8192 bytes.':
      '该值太长。上限为 8192 字节。',
    'This URL uses http. The credentials go over the network with no encryption.':
      '此 URL 使用 http。凭据将以未加密的方式经网络传输。',
    'This header appears twice.': '此标头出现了两次。',
    'To change the credentials, use "Rotate credentials" in the list.':
      '要更改凭据，请使用列表中的“轮换凭据”。',
  },
  ja: {
    "Couldn't load the MCP servers": 'MCP サーバーを読み込めませんでした',
    'A MCP server entry holds a URL and credentials. A step names it, and the coordinator keeps the credentials.':
      'MCP サーバーのエントリは URL と認証情報を保持します。ステップは名前だけを指定し、認証情報はコーディネーターが保管します。',
    'A MCP server entry holds a URL and credentials. A step names it. The step never sees the credentials.':
      'MCP サーバーのエントリは URL と認証情報を保持します。ステップは名前を指定するだけで、認証情報は見えません。',
    'A MCP server entry of the AI page. The tools of the server start with its name.':
      'AI ページの MCP サーバーのエントリです。サーバーのツール名はこの名前で始まります。',
    'A MCP server with this name exists in this scope.':
      'このスコープには同じ名前の MCP サーバーがあります。',
    'A header name starts with a letter. Use letters, digits and -.':
      'ヘッダー名は英字で始めます。英字、数字、- を使います。',
    'A step of a DAG in this list can use the MCP server.':
      'この一覧の DAG のステップはこの MCP サーバーを使えます。',
    'A step writes this name in its mcp entry, with no url. You cannot change it after you save.':
      'ステップは url のない mcp エントリにこの名前を書きます。保存後は変更できません。',
    'A token is the header authorization. Remove one of them.':
      'トークンはヘッダー authorization です。どちらかを削除してください。',
    'Add MCP server': 'MCP サーバーを追加',
    'Add a MCP server': 'MCP サーバーを追加する',
    'Add at least one header, or choose another form.':
      'ヘッダーを 1 つ以上追加するか、別の形式を選んでください。',
    'An entry has at most 16 headers.':
      '1 つのエントリのヘッダーは最大 16 個です。',
    'Bearer token': 'Bearer トークン',
    Credentials: '認証情報',
    'Dagu sets this header. Use another name.':
      'このヘッダーは Dagu が設定します。別の名前を使ってください。',
    'Delete "{name}"? A step that names this MCP server fails until you create it again. The credentials are deleted too.':
      '「{name}」を削除しますか？再作成するまで、この MCP サーバーを指定するステップは失敗します。認証情報も削除されます。',
    'Delete MCP server {name}': 'MCP サーバー {name} を削除',
    'Delete MCP server?': 'MCP サーバーを削除しますか？',
    'Give the URL of the MCP server.':
      'MCP サーバーの URL を入力してください。',
    'Give the token.': 'トークンを入力してください。',
    'Give the value of the header.': 'ヘッダーの値を入力してください。',
    'Gives the tools of a remote MCP server. Use a server of the AI page, or a URL.':
      'リモート MCP サーバーのツールを提供します。AI ページのサーバーか URL を使います。',
    'Header name {number}': 'ヘッダー名 {number}',
    'Header value {number}': 'ヘッダー値 {number}',
    'MCP server entry: {name}': 'MCP サーバーのエントリ: {name}',
    'New MCP server with a URL': 'URL を使う新しい MCP サーバー',
    'No MCP servers yet': 'MCP サーバーはまだありません',
    'No credential': '認証情報なし',
    'Only an administrator can manage the MCP servers.':
      'MCP サーバーを管理できるのは管理者だけです。',
    'Open the MCP servers': 'MCP サーバーを開く',
    'Remove header {number}': 'ヘッダー {number} を削除',
    'Rotate credentials': '認証情報をローテーション',
    'Rotate the credentials of {name}': '{name} の認証情報をローテーション',
    'The MCP server does not exist. Reload the page.':
      'この MCP サーバーは存在しません。ページを再読み込みしてください。',
    'The MCP server was not deleted.': 'MCP サーバーを削除できませんでした。',
    'The MCP server was not saved.': 'MCP サーバーを保存できませんでした。',
    'The URL must not have user information or a fragment.':
      'URL にユーザー情報やフラグメントを含めることはできません。',
    'The URL must start with https or http.':
      'URL は https または http で始める必要があります。',
    'The coordinator calls this server with the credentials of the entry. The step never sees them.':
      'コーディネーターはこのエントリの認証情報でこのサーバーを呼び出します。ステップには認証情報は見えません。',
    'The coordinator sends it as authorization: Bearer. The server never shows it again. It shows the first characters and a fingerprint.':
      'コーディネーターは authorization: Bearer として送信します。サーバーは再表示せず、先頭の文字とフィンガープリントだけを表示します。',
    'The coordinator sends these headers to the server. The server never shows a value again.':
      'コーディネーターはこれらのヘッダーをサーバーに送信します。値は再表示されません。',
    'The credentials were not saved.': '認証情報を保存できませんでした。',
    'The entry will have no credential.':
      'このエントリの認証情報はなくなります。',
    'The name dagu is the name of the tool bridge. Use another name.':
      'dagu という名前はツールブリッジの名前です。別の名前を使ってください。',
    'The name of a MCP server entry of the AI page. The tools of the server start with it.':
      'AI ページの MCP サーバーのエントリの名前です。サーバーのツール名はこの名前で始まります。',
    'The new credentials replace all the old credentials. Give every value again. The server never shows a value after you save.':
      '新しい認証情報は古い認証情報をすべて置き換えます。すべての値を再入力してください。保存後、値は表示されません。',
    'The server does not know the MCP server "{name}", or this DAG may not use it. The step fails when it starts.':
      'サーバーは MCP サーバー「{name}」を知らないか、この DAG は使えません。ステップは開始時に失敗します。',
    'The server does not store MCP servers.':
      'サーバーは MCP サーバーを保存しません。',
    'The streamable HTTP address of the MCP server. The coordinator calls it, not the worker.':
      'MCP サーバーの streamable HTTP アドレスです。呼び出すのはワーカーではなくコーディネーターです。',
    'The value has a control character. Paste it again.':
      '値に制御文字が含まれています。貼り付け直してください。',
    'The value is empty.': '値が空です。',
    'The value is too long. The limit is 8192 bytes.':
      '値が長すぎます。上限は 8192 バイトです。',
    'This URL uses http. The credentials go over the network with no encryption.':
      'この URL は http を使っています。認証情報は暗号化されずにネットワークを通ります。',
    'This header appears twice.': 'このヘッダーが 2 回出てきます。',
    'To change the credentials, use "Rotate credentials" in the list.':
      '認証情報を変更するには、一覧の「認証情報をローテーション」を使います。',
  },
});
