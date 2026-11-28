// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

/** The static messages of the Connections pages. */
export const connectionMessages = defineMessages({
  en: {
    'Accounts that DAGs in this workspace use by name. Dagu keeps and refreshes their tokens.':
      'Accounts that DAGs in this workspace use by name. Dagu keeps and refreshes their tokens.',
    Account: 'Account',
    'Loading connections…': 'Loading connections…',
    'No connections in this workspace.': 'No connections in this workspace.',
    'Loading tools…': 'Loading tools…',
    'This connection has no MCP services.':
      'This connection has no MCP services.',
    'Listed {time}': 'Listed {time}',
    'The service lists no tools.': 'The service lists no tools.',
    'Read only': 'Read only',
    'Connection not found': 'Connection not found',
    'No connection {name} in workspace {workspace}.':
      'No connection {name} in workspace {workspace}.',
    'Back to connections': 'Back to connections',
    "Couldn't load the connection.": "Couldn't load the connection.",
    'The test failed.': 'The test failed.',
    'Could not remove the connection.': 'Could not remove the connection.',
    Connect: 'Connect',
    Reconnect: 'Reconnect',
    'Test results': 'Test results',
    'Last refreshed': 'Last refreshed',
    'Sign-in ends': 'Sign-in ends',
    'Read and write': 'Read and write',
    Read: 'Read',
    'Not granted. Reconnect to ask for the missing scopes:':
      'Not granted. Reconnect to ask for the missing scopes:',
    'Granted scopes': 'Granted scopes',
    'Remove connection': 'Remove connection',
    'Remove {name}? Its tokens and tool lists are deleted, and DAGs that name it fail when they start.':
      'Remove {name}? Its tokens and tool lists are deleted, and DAGs that name it fail when they start.',
    'Dagu also revokes its sign-in at Google, so connections on other servers that use a client of the same Google Cloud project may lose access.':
      'Dagu also revokes its sign-in at Google, so connections on other servers that use a client of the same Google Cloud project may lose access.',
    'Dagu also revokes its sign-in at the provider, so connections on other servers that use the same client may lose access.':
      'Dagu also revokes its sign-in at the provider, so connections on other servers that use the same client may lose access.',
    'Copy {name}': 'Copy {name}',
    'Connections are kept on each server':
      'Connections are kept on each server',
    'Remote nodes do not serve their connections. Select the local server to see its connections.':
      'Remote nodes do not serve their connections. Select the local server to see its connections.',
    'Could not start signing in.': 'Could not start signing in.',
    'Could not sign in.': 'Could not sign in.',
    'Could not confirm.': 'Could not confirm.',
    'Connect {name}': 'Connect {name}',
    'Reconnect {name}': 'Reconnect {name}',
    'The live tokens stay in use until you confirm the account that signed in.':
      'The live tokens stay in use until you confirm the account that signed in.',
    '{name} is connected.': '{name} is connected.',
    'Signing in failed: {reason}': 'Signing in failed: {reason}',
    "This server has no public URL, so a web client can't receive the sign-in. Set server.public_url, or use an app with a desktop client.":
      "This server has no public URL, so a web client can't receive the sign-in. Set server.public_url, or use an app with a desktop client.",
    'After you consent, the popup ends on a page that fails to load. Copy that page’s address from the popup and paste it here.':
      'After you consent, the popup ends on a page that fails to load. Copy that page’s address from the popup and paste it here.',
    'Redirect URI to register in the client':
      'Redirect URI to register in the client',
    'Client ID, which a Workspace admin may have to trust':
      'Client ID, which a Workspace admin may have to trust',
    'Address the popup ended on': 'Address the popup ended on',
    'Confirm account': 'Confirm account',
    Continue: 'Continue',
    'Open sign-in': 'Open sign-in',
    'The browser blocked the popup. Open the sign-in page instead.':
      'The browser blocked the popup. Open the sign-in page instead.',
    'Waiting for consent in the popup…': 'Waiting for consent in the popup…',
    'Open sign-in page': 'Open sign-in page',
    'Copy sign-in link': 'Copy sign-in link',
    'The account’s owner may sign in from their own browser with the copied link. The session ends at {time}.':
      'The account’s owner may sign in from their own browser with the copied link. The session ends at {time}.',
    'The session ends at {time}.': 'The session ends at {time}.',
    'Signed in as': 'Signed in as',
    'This is not {account}, the account the connection holds now. Confirming replaces it.':
      'This is not {account}, the account the connection holds now. Confirming replaces it.',
    'Other connections in this workspace already hold this account through the same client: {names}.':
      'Other connections in this workspace already hold this account through the same client: {names}.',
    'The sign-in ends {date}.': 'The sign-in ends {date}.',
    'Service checks': 'Service checks',
    'Needs reconnect': 'Needs reconnect',
    Broken: 'Broken',
    'Could not create the connection.': 'Could not create the connection.',
    'Connect Google Workspace': 'Connect Google Workspace',
    'The connection belongs to workspace {workspace}.':
      'The connection belongs to workspace {workspace}.',
    'Create and sign in': 'Create and sign in',
    'Gmail, Calendar, Drive, Docs, and more.':
      'Gmail, Calendar, Drive, Docs, and more.',
    'Not available yet.': 'Not available yet.',
    'DAGs name the connection with this. Up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit.':
      'DAGs name the connection with this. Up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit.',
    'Access for {service}': 'Access for {service}',
    'Preview services work only for accounts in Google’s Workspace Developer Preview program.':
      'Preview services work only for accounts in Google’s Workspace Developer Preview program.',
    'Scopes to ask for': 'Scopes to ask for',
    'AI agents': 'AI agents',
    'Steps call Google’s MCP servers with mcp.call, and AI steps use their tools.':
      'Steps call Google’s MCP servers with mcp.call, and AI steps use their tools.',
    'Other automation and mail': 'Other automation and mail',
    'Steps call Google’s REST APIs with http.request, and mail accounts use the mailbox.':
      'Steps call Google’s REST APIs with http.request, and mail accounts use the mailbox.',
    'Who will use this connection?': 'Who will use this connection?',
    'Google requires separate OAuth clients for AI agents and for other automation, so each connection uses one kind. For both, add two connections.':
      'Google requires separate OAuth clients for AI agents and for other automation, so each connection uses one kind. For both, add two connections.',
    'OAuth app': 'OAuth app',
    'No Google app for AI agents fits this server yet.':
      'No Google app for AI agents fits this server yet.',
    'No Google app for other automation fits this server yet.':
      'No Google app for other automation fits this server yet.',
    'Register another app': 'Register another app',
    'DAGs that run on this server, rather than on a worker, run as Dagu’s user, so their authors can read this connection’s tokens.':
      'DAGs that run on this server, rather than on a worker, run as Dagu’s user, so their authors can read this connection’s tokens.',
    'Ask an admin to register a Google OAuth app with:':
      'Ask an admin to register a Google OAuth app with:',
    'Client type: Web application': 'Client type: Web application',
    'Client type: Desktop app': 'Client type: Desktop app',
    '“Use this client for an AI-powered agent” ticked':
      '“Use this client for an AI-powered agent” ticked',
    '“Use this client for an AI-powered agent” not ticked':
      '“Use this client for an AI-powered agent” not ticked',
    'Could not register the app.': 'Could not register the app.',
    'Enable the APIs': 'Enable the APIs',
    'Enable APIs in Google Cloud': 'Enable APIs in Google Cloud',
    'The chosen services need no API.': 'The chosen services need no API.',
    'Set up the consent screen': 'Set up the consent screen',
    'Open the consent screen': 'Open the consent screen',
    'An app in Testing status accepts only its test users, and their sign-ins end after seven days. Publish the app to Production, or make it Internal in a Workspace organization, to avoid both.':
      'An app in Testing status accepts only its test users, and their sign-ins end after seven days. Publish the app to Production, or make it Internal in a Workspace organization, to avoid both.',
    'An app that Google has not verified shows “Google hasn’t verified this app” at sign-in. That is expected for your own app.':
      'An app that Google has not verified shows “Google hasn’t verified this app” at sign-in. That is expected for your own app.',
    'Create the OAuth client': 'Create the OAuth client',
    'Open Google Cloud clients': 'Open Google Cloud clients',
    'Choose the application type Web application.':
      'Choose the application type Web application.',
    'Choose the application type Desktop app. This server has no public URL, so the sign-in ends on a page whose address you paste back.':
      'Choose the application type Desktop app. This server has no public URL, so the sign-in ends on a page whose address you paste back.',
    'Tick “Use this client for an AI-powered agent”.':
      'Tick “Use this client for an AI-powered agent”.',
    'Upload the client JSON': 'Upload the client JSON',
    'App name': 'App name',
    'Client JSON file': 'Client JSON file',
    'Register app': 'Register app',
    'Mail over IMAP and SMTP': 'Mail over IMAP and SMTP',
  },
  'zh-CN': {
    'Accounts that DAGs in this workspace use by name. Dagu keeps and refreshes their tokens.':
      '此工作区的 DAG 按名称使用的账号。Dagu 保存并刷新其令牌。',
    Account: '账号',
    'Loading connections…': '正在加载连接…',
    'No connections in this workspace.': '此工作区没有连接。',
    'Loading tools…': '正在加载工具…',
    'This connection has no MCP services.': '此连接没有 MCP 服务。',
    'Listed {time}': '列出于 {time}',
    'The service lists no tools.': '该服务未列出任何工具。',
    'Read only': '只读',
    'Connection not found': '未找到连接',
    'No connection {name} in workspace {workspace}.':
      '工作区 {workspace} 中没有连接 {name}。',
    'Back to connections': '返回连接',
    "Couldn't load the connection.": '无法加载连接。',
    'The test failed.': '测试失败。',
    'Could not remove the connection.': '无法移除连接。',
    Connect: '连接',
    Reconnect: '重新连接',
    'Test results': '测试结果',
    'Last refreshed': '上次刷新',
    'Sign-in ends': '登录到期',
    'Read and write': '读写',
    Read: '读取',
    'Not granted. Reconnect to ask for the missing scopes:':
      '未授予。重新连接以请求缺少的权限范围：',
    'Granted scopes': '已授予的权限范围',
    'Remove connection': '移除连接',
    'Remove {name}? Its tokens and tool lists are deleted, and DAGs that name it fail when they start.':
      '移除 {name}？其令牌和工具列表将被删除，引用它的 DAG 将在启动时失败。',
    'Dagu also revokes its sign-in at Google, so connections on other servers that use a client of the same Google Cloud project may lose access.':
      'Dagu 还会在 Google 撤销其登录，因此其他服务器上使用同一 Google Cloud 项目客户端的连接可能会失去访问权限。',
    'Dagu also revokes its sign-in at the provider, so connections on other servers that use the same client may lose access.':
      'Dagu 还会在提供方撤销其登录，因此其他服务器上使用同一客户端的连接可能会失去访问权限。',
    'Copy {name}': '复制{name}',
    'Connections are kept on each server': '连接保存在各自的服务器上',
    'Remote nodes do not serve their connections. Select the local server to see its connections.':
      '远程节点不提供其连接。选择本地服务器以查看其连接。',
    'Could not start signing in.': '无法开始登录。',
    'Could not sign in.': '无法登录。',
    'Could not confirm.': '无法确认。',
    'Connect {name}': '连接 {name}',
    'Reconnect {name}': '重新连接 {name}',
    'The live tokens stay in use until you confirm the account that signed in.':
      '在你确认登录的账号之前，当前令牌会继续使用。',
    '{name} is connected.': '{name} 已连接。',
    'Signing in failed: {reason}': '登录失败：{reason}',
    "This server has no public URL, so a web client can't receive the sign-in. Set server.public_url, or use an app with a desktop client.":
      '此服务器没有公开 URL，因此 Web 客户端无法接收登录结果。请设置 server.public_url，或使用桌面客户端的应用。',
    'After you consent, the popup ends on a page that fails to load. Copy that page’s address from the popup and paste it here.':
      '同意后，弹出窗口会停在一个无法加载的页面。从弹出窗口复制该页面的地址并粘贴到这里。',
    'Redirect URI to register in the client': '要在客户端中注册的重定向 URI',
    'Client ID, which a Workspace admin may have to trust':
      '客户端 ID（Workspace 管理员可能需要信任它）',
    'Address the popup ended on': '弹出窗口最终停留的地址',
    'Confirm account': '确认账号',
    Continue: '继续',
    'Open sign-in': '打开登录',
    'The browser blocked the popup. Open the sign-in page instead.':
      '浏览器拦截了弹出窗口。请改为打开登录页面。',
    'Waiting for consent in the popup…': '正在等待弹出窗口中的同意…',
    'Open sign-in page': '打开登录页面',
    'Copy sign-in link': '复制登录链接',
    'The account’s owner may sign in from their own browser with the copied link. The session ends at {time}.':
      '账号所有者可以用复制的链接在自己的浏览器中登录。会话于 {time} 结束。',
    'The session ends at {time}.': '会话于 {time} 结束。',
    'Signed in as': '登录账号',
    'This is not {account}, the account the connection holds now. Confirming replaces it.':
      '这不是该连接当前持有的账号 {account}。确认后将替换它。',
    'Other connections in this workspace already hold this account through the same client: {names}.':
      '此工作区中已有其他连接通过同一客户端持有此账号：{names}。',
    'The sign-in ends {date}.': '登录于 {date} 到期。',
    'Service checks': '服务检查',
    'Needs reconnect': '需要重新连接',
    Broken: '已损坏',
    'Could not create the connection.': '无法创建连接。',
    'Connect Google Workspace': '连接 Google Workspace',
    'The connection belongs to workspace {workspace}.':
      '该连接属于工作区 {workspace}。',
    'Create and sign in': '创建并登录',
    'Gmail, Calendar, Drive, Docs, and more.':
      'Gmail、Calendar、Drive、Docs 等。',
    'Not available yet.': '暂不可用。',
    'DAGs name the connection with this. Up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit.':
      'DAG 用此名称引用连接。最多 63 个小写字母、数字和连字符，以字母或数字开头。',
    'Access for {service}': '{service} 的访问权限',
    'Preview services work only for accounts in Google’s Workspace Developer Preview program.':
      '预览服务仅适用于加入 Google Workspace Developer Preview 计划的账号。',
    'Scopes to ask for': '要请求的权限范围',
    'AI agents': 'AI 智能体',
    'Steps call Google’s MCP servers with mcp.call, and AI steps use their tools.':
      '步骤通过 mcp.call 调用 Google 的 MCP 服务器，AI 步骤使用其工具。',
    'Other automation and mail': '其他自动化和邮件',
    'Steps call Google’s REST APIs with http.request, and mail accounts use the mailbox.':
      '步骤通过 http.request 调用 Google 的 REST API，邮件账号使用邮箱。',
    'Who will use this connection?': '谁将使用此连接？',
    'Google requires separate OAuth clients for AI agents and for other automation, so each connection uses one kind. For both, add two connections.':
      'Google 要求 AI 智能体和其他自动化使用不同的 OAuth 客户端，因此每个连接只使用一种。两者都需要时，请添加两个连接。',
    'OAuth app': 'OAuth 应用',
    'No Google app for AI agents fits this server yet.':
      '尚无适用于此服务器的 AI 智能体 Google 应用。',
    'No Google app for other automation fits this server yet.':
      '尚无适用于此服务器的其他自动化 Google 应用。',
    'Register another app': '注册其他应用',
    'DAGs that run on this server, rather than on a worker, run as Dagu’s user, so their authors can read this connection’s tokens.':
      '在此服务器上（而非 worker 上）运行的 DAG 以 Dagu 的用户身份运行，因此其作者可以读取此连接的令牌。',
    'Ask an admin to register a Google OAuth app with:':
      '请管理员注册具有以下设置的 Google OAuth 应用：',
    'Client type: Web application': '客户端类型：Web 应用',
    'Client type: Desktop app': '客户端类型：桌面应用',
    '“Use this client for an AI-powered agent” ticked':
      '勾选“Use this client for an AI-powered agent”',
    '“Use this client for an AI-powered agent” not ticked':
      '不勾选“Use this client for an AI-powered agent”',
    'Could not register the app.': '无法注册应用。',
    'Enable the APIs': '启用 API',
    'Enable APIs in Google Cloud': '在 Google Cloud 中启用 API',
    'The chosen services need no API.': '所选服务无需启用 API。',
    'Set up the consent screen': '设置同意屏幕',
    'Open the consent screen': '打开同意屏幕',
    'An app in Testing status accepts only its test users, and their sign-ins end after seven days. Publish the app to Production, or make it Internal in a Workspace organization, to avoid both.':
      '处于 Testing 状态的应用只接受其测试用户，且登录在七天后失效。将应用发布到 Production，或在 Workspace 组织中设为 Internal，即可避免这两点。',
    'An app that Google has not verified shows “Google hasn’t verified this app” at sign-in. That is expected for your own app.':
      '未经 Google 验证的应用在登录时会显示“Google hasn’t verified this app”。对于你自己的应用，这是正常的。',
    'Create the OAuth client': '创建 OAuth 客户端',
    'Open Google Cloud clients': '打开 Google Cloud 客户端',
    'Choose the application type Web application.': '选择应用类型“Web 应用”。',
    'Choose the application type Desktop app. This server has no public URL, so the sign-in ends on a page whose address you paste back.':
      '选择应用类型“桌面应用”。此服务器没有公开 URL，因此登录会停在一个页面上，你需要把该页面的地址粘贴回来。',
    'Tick “Use this client for an AI-powered agent”.':
      '勾选“Use this client for an AI-powered agent”。',
    'Upload the client JSON': '上传客户端 JSON',
    'App name': '应用名称',
    'Client JSON file': '客户端 JSON 文件',
    'Register app': '注册应用',
    'Mail over IMAP and SMTP': '通过 IMAP 和 SMTP 收发邮件',
  },
  ja: {
    'Accounts that DAGs in this workspace use by name. Dagu keeps and refreshes their tokens.':
      'このワークスペースの DAG が名前で使うアカウントです。トークンは Dagu が保管し、更新します。',
    Account: 'アカウント',
    'Loading connections…': '接続を読み込んでいます…',
    'No connections in this workspace.':
      'このワークスペースに接続はありません。',
    'Loading tools…': 'ツールを読み込んでいます…',
    'This connection has no MCP services.':
      'この接続には MCP サービスがありません。',
    'Listed {time}': '{time} に取得',
    'The service lists no tools.': 'このサービスにはツールがありません。',
    'Read only': '読み取り専用',
    'Connection not found': '接続が見つかりません',
    'No connection {name} in workspace {workspace}.':
      'ワークスペース {workspace} に接続 {name} はありません。',
    'Back to connections': '接続一覧に戻る',
    "Couldn't load the connection.": '接続を読み込めませんでした。',
    'The test failed.': 'テストに失敗しました。',
    'Could not remove the connection.': '接続を削除できませんでした。',
    Connect: '接続',
    Reconnect: '再接続',
    'Test results': 'テスト結果',
    'Last refreshed': '最終更新',
    'Sign-in ends': 'サインインの期限',
    'Read and write': '読み取りと書き込み',
    Read: '読み取り',
    'Not granted. Reconnect to ask for the missing scopes:':
      '許可されていません。再接続して不足しているスコープを要求してください:',
    'Granted scopes': '許可されたスコープ',
    'Remove connection': '接続を削除',
    'Remove {name}? Its tokens and tool lists are deleted, and DAGs that name it fail when they start.':
      '{name} を削除しますか？トークンとツール一覧が削除され、この接続を指定する DAG は開始時に失敗します。',
    'Dagu also revokes its sign-in at Google, so connections on other servers that use a client of the same Google Cloud project may lose access.':
      'Dagu は Google でのサインインも取り消すため、同じ Google Cloud プロジェクトのクライアントを使う他のサーバーの接続がアクセスを失うことがあります。',
    'Dagu also revokes its sign-in at the provider, so connections on other servers that use the same client may lose access.':
      'Dagu はプロバイダーでのサインインも取り消すため、同じクライアントを使う他のサーバーの接続がアクセスを失うことがあります。',
    'Copy {name}': '{name}をコピー',
    'Connections are kept on each server': '接続は各サーバーに保存されます',
    'Remote nodes do not serve their connections. Select the local server to see its connections.':
      'リモートノードは接続を提供しません。接続を見るにはローカルサーバーを選択してください。',
    'Could not start signing in.': 'サインインを開始できませんでした。',
    'Could not sign in.': 'サインインできませんでした。',
    'Could not confirm.': '確認できませんでした。',
    'Connect {name}': '{name} を接続',
    'Reconnect {name}': '{name} を再接続',
    'The live tokens stay in use until you confirm the account that signed in.':
      'サインインしたアカウントを確認するまで、現在のトークンが使われ続けます。',
    '{name} is connected.': '{name} を接続しました。',
    'Signing in failed: {reason}': 'サインインに失敗しました: {reason}',
    "This server has no public URL, so a web client can't receive the sign-in. Set server.public_url, or use an app with a desktop client.":
      'このサーバーには公開 URL がないため、ウェブクライアントはサインインを受け取れません。server.public_url を設定するか、デスクトップクライアントのアプリを使ってください。',
    'After you consent, the popup ends on a page that fails to load. Copy that page’s address from the popup and paste it here.':
      '同意すると、ポップアップは読み込めないページで止まります。そのページのアドレスをポップアップからコピーして、ここに貼り付けてください。',
    'Redirect URI to register in the client':
      'クライアントに登録するリダイレクト URI',
    'Client ID, which a Workspace admin may have to trust':
      'クライアント ID（Workspace 管理者による信頼の設定が必要な場合があります）',
    'Address the popup ended on': 'ポップアップが最後に表示したアドレス',
    'Confirm account': 'アカウントを確認',
    Continue: '続行',
    'Open sign-in': 'サインインを開く',
    'The browser blocked the popup. Open the sign-in page instead.':
      'ブラウザーがポップアップをブロックしました。代わりにサインインページを開いてください。',
    'Waiting for consent in the popup…': 'ポップアップでの同意を待っています…',
    'Open sign-in page': 'サインインページを開く',
    'Copy sign-in link': 'サインインリンクをコピー',
    'The account’s owner may sign in from their own browser with the copied link. The session ends at {time}.':
      'アカウントの所有者は、コピーしたリンクから自分のブラウザーでサインインできます。セッションは {time} に終了します。',
    'The session ends at {time}.': 'セッションは {time} に終了します。',
    'Signed in as': 'サインインしたアカウント',
    'This is not {account}, the account the connection holds now. Confirming replaces it.':
      'これは接続が現在使っているアカウント {account} ではありません。確認すると置き換わります。',
    'Other connections in this workspace already hold this account through the same client: {names}.':
      'このワークスペースの他の接続が、同じクライアントでこのアカウントを使っています: {names}。',
    'The sign-in ends {date}.': 'サインインは {date} に期限切れになります。',
    'Service checks': 'サービスのチェック',
    'Needs reconnect': '再接続が必要',
    Broken: '破損',
    'Could not create the connection.': '接続を作成できませんでした。',
    'Connect Google Workspace': 'Google Workspace を接続',
    'The connection belongs to workspace {workspace}.':
      'この接続はワークスペース {workspace} に属します。',
    'Create and sign in': '作成してサインイン',
    'Gmail, Calendar, Drive, Docs, and more.':
      'Gmail、Calendar、Drive、Docs など。',
    'Not available yet.': 'まだ利用できません。',
    'DAGs name the connection with this. Up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit.':
      'DAG はこの名前で接続を指定します。小文字・数字・ハイフンで 63 文字まで、先頭は英字か数字です。',
    'Access for {service}': '{service} のアクセス',
    'Preview services work only for accounts in Google’s Workspace Developer Preview program.':
      'プレビューのサービスは、Google の Workspace Developer Preview プログラムに参加しているアカウントでのみ使えます。',
    'Scopes to ask for': '要求するスコープ',
    'AI agents': 'AI エージェント',
    'Steps call Google’s MCP servers with mcp.call, and AI steps use their tools.':
      'ステップは mcp.call で Google の MCP サーバーを呼び出し、AI ステップはそのツールを使います。',
    'Other automation and mail': 'その他の自動化とメール',
    'Steps call Google’s REST APIs with http.request, and mail accounts use the mailbox.':
      'ステップは http.request で Google の REST API を呼び出し、メールアカウントはメールボックスを使います。',
    'Who will use this connection?': 'この接続を使うのは？',
    'Google requires separate OAuth clients for AI agents and for other automation, so each connection uses one kind. For both, add two connections.':
      'Google では AI エージェントとその他の自動化に別々の OAuth クライアントが必要なため、1 つの接続はどちらか一方を使います。両方使う場合は接続を 2 つ追加してください。',
    'OAuth app': 'OAuth アプリ',
    'No Google app for AI agents fits this server yet.':
      'このサーバーに合う AI エージェント用の Google アプリはまだありません。',
    'No Google app for other automation fits this server yet.':
      'このサーバーに合うその他の自動化用の Google アプリはまだありません。',
    'Register another app': '別のアプリを登録',
    'DAGs that run on this server, rather than on a worker, run as Dagu’s user, so their authors can read this connection’s tokens.':
      'ワーカーではなくこのサーバーで動く DAG は Dagu のユーザーとして実行されるため、その作成者はこの接続のトークンを読めます。',
    'Ask an admin to register a Google OAuth app with:':
      '管理者に、次の設定の Google OAuth アプリの登録を依頼してください:',
    'Client type: Web application':
      'クライアントの種類: ウェブ アプリケーション',
    'Client type: Desktop app': 'クライアントの種類: デスクトップ アプリ',
    '“Use this client for an AI-powered agent” ticked':
      '「Use this client for an AI-powered agent」をオン',
    '“Use this client for an AI-powered agent” not ticked':
      '「Use this client for an AI-powered agent」をオフ',
    'Could not register the app.': 'アプリを登録できませんでした。',
    'Enable the APIs': 'API を有効にする',
    'Enable APIs in Google Cloud': 'Google Cloud で API を有効にする',
    'The chosen services need no API.':
      '選んだサービスに有効にする API はありません。',
    'Set up the consent screen': '同意画面を設定する',
    'Open the consent screen': '同意画面を開く',
    'An app in Testing status accepts only its test users, and their sign-ins end after seven days. Publish the app to Production, or make it Internal in a Workspace organization, to avoid both.':
      'Testing 状態のアプリはテストユーザーしか受け付けず、サインインは 7 日で期限切れになります。アプリを Production に公開するか、Workspace 組織で Internal にすると、どちらも避けられます。',
    'An app that Google has not verified shows “Google hasn’t verified this app” at sign-in. That is expected for your own app.':
      'Google が確認していないアプリでは、サインイン時に「Google hasn’t verified this app」と表示されます。自分のアプリでは想定どおりです。',
    'Create the OAuth client': 'OAuth クライアントを作成する',
    'Open Google Cloud clients': 'Google Cloud のクライアントを開く',
    'Choose the application type Web application.':
      'アプリケーションの種類で「ウェブ アプリケーション」を選びます。',
    'Choose the application type Desktop app. This server has no public URL, so the sign-in ends on a page whose address you paste back.':
      'アプリケーションの種類で「デスクトップ アプリ」を選びます。このサーバーには公開 URL がないため、サインインはあるページで止まり、そのアドレスを貼り付けて戻します。',
    'Tick “Use this client for an AI-powered agent”.':
      '「Use this client for an AI-powered agent」をオンにします。',
    'Upload the client JSON': 'クライアント JSON をアップロードする',
    'App name': 'アプリ名',
    'Client JSON file': 'クライアント JSON ファイル',
    'Register app': 'アプリを登録',
    'Mail over IMAP and SMTP': 'IMAP と SMTP のメール',
  },
  de: {
    'Accounts that DAGs in this workspace use by name. Dagu keeps and refreshes their tokens.':
      'Konten, die DAGs in diesem Arbeitsbereich per Namen nutzen. Dagu speichert und erneuert ihre Tokens.',
    Account: 'Konto',
    'Loading connections…': 'Verbindungen werden geladen…',
    'No connections in this workspace.':
      'Keine Verbindungen in diesem Arbeitsbereich.',
    'Loading tools…': 'Tools werden geladen…',
    'This connection has no MCP services.':
      'Diese Verbindung hat keine MCP-Dienste.',
    'Listed {time}': 'Abgerufen {time}',
    'The service lists no tools.': 'Der Dienst bietet keine Tools an.',
    'Read only': 'Nur lesen',
    'Connection not found': 'Verbindung nicht gefunden',
    'No connection {name} in workspace {workspace}.':
      'Keine Verbindung {name} im Arbeitsbereich {workspace}.',
    'Back to connections': 'Zurück zu den Verbindungen',
    "Couldn't load the connection.":
      'Die Verbindung konnte nicht geladen werden.',
    'The test failed.': 'Der Test ist fehlgeschlagen.',
    'Could not remove the connection.':
      'Die Verbindung konnte nicht entfernt werden.',
    Connect: 'Verbinden',
    Reconnect: 'Neu verbinden',
    'Test results': 'Testergebnisse',
    'Last refreshed': 'Zuletzt erneuert',
    'Sign-in ends': 'Anmeldung endet',
    'Read and write': 'Lesen und schreiben',
    Read: 'Lesen',
    'Not granted. Reconnect to ask for the missing scopes:':
      'Nicht gewährt. Neu verbinden, um die fehlenden Scopes anzufordern:',
    'Granted scopes': 'Gewährte Scopes',
    'Remove connection': 'Verbindung entfernen',
    'Remove {name}? Its tokens and tool lists are deleted, and DAGs that name it fail when they start.':
      '{name} entfernen? Seine Tokens und Tool-Listen werden gelöscht, und DAGs, die es nennen, schlagen beim Start fehl.',
    'Dagu also revokes its sign-in at Google, so connections on other servers that use a client of the same Google Cloud project may lose access.':
      'Dagu widerruft auch die Anmeldung bei Google. Verbindungen auf anderen Servern, die einen Client desselben Google-Cloud-Projekts nutzen, können den Zugriff verlieren.',
    'Dagu also revokes its sign-in at the provider, so connections on other servers that use the same client may lose access.':
      'Dagu widerruft auch die Anmeldung beim Anbieter. Verbindungen auf anderen Servern, die denselben Client nutzen, können den Zugriff verlieren.',
    'Copy {name}': '{name} kopieren',
    'Connections are kept on each server':
      'Verbindungen liegen auf jedem Server',
    'Remote nodes do not serve their connections. Select the local server to see its connections.':
      'Remote-Knoten stellen ihre Verbindungen nicht bereit. Wählen Sie den lokalen Server, um seine Verbindungen zu sehen.',
    'Could not start signing in.': 'Die Anmeldung konnte nicht starten.',
    'Could not sign in.': 'Die Anmeldung ist fehlgeschlagen.',
    'Could not confirm.': 'Bestätigen fehlgeschlagen.',
    'Connect {name}': '{name} verbinden',
    'Reconnect {name}': '{name} neu verbinden',
    'The live tokens stay in use until you confirm the account that signed in.':
      'Die aktuellen Tokens bleiben in Gebrauch, bis Sie das angemeldete Konto bestätigen.',
    '{name} is connected.': '{name} ist verbunden.',
    'Signing in failed: {reason}': 'Anmeldung fehlgeschlagen: {reason}',
    "This server has no public URL, so a web client can't receive the sign-in. Set server.public_url, or use an app with a desktop client.":
      'Dieser Server hat keine öffentliche URL, daher kann ein Web-Client die Anmeldung nicht empfangen. Setzen Sie server.public_url oder nutzen Sie eine App mit Desktop-Client.',
    'After you consent, the popup ends on a page that fails to load. Copy that page’s address from the popup and paste it here.':
      'Nach der Zustimmung endet das Popup auf einer Seite, die nicht lädt. Kopieren Sie deren Adresse aus dem Popup und fügen Sie sie hier ein.',
    'Redirect URI to register in the client':
      'Im Client zu registrierende Weiterleitungs-URI',
    'Client ID, which a Workspace admin may have to trust':
      'Client-ID, der ein Workspace-Admin eventuell vertrauen muss',
    'Address the popup ended on': 'Adresse, auf der das Popup endete',
    'Confirm account': 'Konto bestätigen',
    Continue: 'Weiter',
    'Open sign-in': 'Anmeldung öffnen',
    'The browser blocked the popup. Open the sign-in page instead.':
      'Der Browser hat das Popup blockiert. Öffnen Sie stattdessen die Anmeldeseite.',
    'Waiting for consent in the popup…': 'Warte auf die Zustimmung im Popup…',
    'Open sign-in page': 'Anmeldeseite öffnen',
    'Copy sign-in link': 'Anmeldelink kopieren',
    'The account’s owner may sign in from their own browser with the copied link. The session ends at {time}.':
      'Der Inhaber des Kontos kann sich mit dem kopierten Link in seinem eigenen Browser anmelden. Die Sitzung endet um {time}.',
    'The session ends at {time}.': 'Die Sitzung endet um {time}.',
    'Signed in as': 'Angemeldet als',
    'This is not {account}, the account the connection holds now. Confirming replaces it.':
      'Dies ist nicht {account}, das Konto der Verbindung. Bestätigen ersetzt es.',
    'Other connections in this workspace already hold this account through the same client: {names}.':
      'Andere Verbindungen in diesem Arbeitsbereich nutzen dieses Konto bereits über denselben Client: {names}.',
    'The sign-in ends {date}.': 'Die Anmeldung endet am {date}.',
    'Service checks': 'Dienstprüfungen',
    'Needs reconnect': 'Neu verbinden nötig',
    Broken: 'Defekt',
    'Could not create the connection.':
      'Die Verbindung konnte nicht erstellt werden.',
    'Connect Google Workspace': 'Google Workspace verbinden',
    'The connection belongs to workspace {workspace}.':
      'Die Verbindung gehört zum Arbeitsbereich {workspace}.',
    'Create and sign in': 'Erstellen und anmelden',
    'Gmail, Calendar, Drive, Docs, and more.':
      'Gmail, Calendar, Drive, Docs und mehr.',
    'Not available yet.': 'Noch nicht verfügbar.',
    'DAGs name the connection with this. Up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit.':
      'DAGs nennen die Verbindung mit diesem Namen. Bis zu 63 Kleinbuchstaben, Ziffern und Bindestriche, beginnend mit einem Buchstaben oder einer Ziffer.',
    'Access for {service}': 'Zugriff für {service}',
    'Preview services work only for accounts in Google’s Workspace Developer Preview program.':
      'Vorschau-Dienste funktionieren nur für Konten im Workspace Developer Preview-Programm von Google.',
    'Scopes to ask for': 'Anzufordernde Scopes',
    'AI agents': 'KI-Agenten',
    'Steps call Google’s MCP servers with mcp.call, and AI steps use their tools.':
      'Schritte rufen die MCP-Server von Google mit mcp.call auf, und KI-Schritte nutzen ihre Tools.',
    'Other automation and mail': 'Andere Automatisierung und E-Mail',
    'Steps call Google’s REST APIs with http.request, and mail accounts use the mailbox.':
      'Schritte rufen die REST-APIs von Google mit http.request auf, und E-Mail-Konten nutzen das Postfach.',
    'Who will use this connection?': 'Wer nutzt diese Verbindung?',
    'Google requires separate OAuth clients for AI agents and for other automation, so each connection uses one kind. For both, add two connections.':
      'Google verlangt getrennte OAuth-Clients für KI-Agenten und für andere Automatisierung, daher nutzt jede Verbindung eine Art. Für beides fügen Sie zwei Verbindungen hinzu.',
    'OAuth app': 'OAuth-App',
    'No Google app for AI agents fits this server yet.':
      'Noch keine Google-App für KI-Agenten passt zu diesem Server.',
    'No Google app for other automation fits this server yet.':
      'Noch keine Google-App für andere Automatisierung passt zu diesem Server.',
    'Register another app': 'Weitere App registrieren',
    'DAGs that run on this server, rather than on a worker, run as Dagu’s user, so their authors can read this connection’s tokens.':
      'DAGs, die auf diesem Server statt auf einem Worker laufen, laufen als Dagus Benutzer, daher können ihre Autoren die Tokens dieser Verbindung lesen.',
    'Ask an admin to register a Google OAuth app with:':
      'Bitten Sie einen Admin, eine Google-OAuth-App zu registrieren mit:',
    'Client type: Web application': 'Client-Typ: Webanwendung',
    'Client type: Desktop app': 'Client-Typ: Desktop-App',
    '“Use this client for an AI-powered agent” ticked':
      '„Use this client for an AI-powered agent“ angehakt',
    '“Use this client for an AI-powered agent” not ticked':
      '„Use this client for an AI-powered agent“ nicht angehakt',
    'Could not register the app.': 'Die App konnte nicht registriert werden.',
    'Enable the APIs': 'APIs aktivieren',
    'Enable APIs in Google Cloud': 'APIs in Google Cloud aktivieren',
    'The chosen services need no API.':
      'Die gewählten Dienste brauchen keine API.',
    'Set up the consent screen': 'Zustimmungsbildschirm einrichten',
    'Open the consent screen': 'Zustimmungsbildschirm öffnen',
    'An app in Testing status accepts only its test users, and their sign-ins end after seven days. Publish the app to Production, or make it Internal in a Workspace organization, to avoid both.':
      'Eine App im Status Testing akzeptiert nur ihre Testnutzer, und deren Anmeldungen enden nach sieben Tagen. Veröffentlichen Sie die App in Production oder machen Sie sie in einer Workspace-Organisation Internal, um beides zu vermeiden.',
    'An app that Google has not verified shows “Google hasn’t verified this app” at sign-in. That is expected for your own app.':
      'Eine von Google nicht geprüfte App zeigt bei der Anmeldung „Google hasn’t verified this app“. Für Ihre eigene App ist das zu erwarten.',
    'Create the OAuth client': 'OAuth-Client erstellen',
    'Open Google Cloud clients': 'Google-Cloud-Clients öffnen',
    'Choose the application type Web application.':
      'Wählen Sie den Anwendungstyp Webanwendung.',
    'Choose the application type Desktop app. This server has no public URL, so the sign-in ends on a page whose address you paste back.':
      'Wählen Sie den Anwendungstyp Desktop-App. Dieser Server hat keine öffentliche URL, daher endet die Anmeldung auf einer Seite, deren Adresse Sie zurück einfügen.',
    'Tick “Use this client for an AI-powered agent”.':
      'Haken Sie „Use this client for an AI-powered agent“ an.',
    'Upload the client JSON': 'Client-JSON hochladen',
    'App name': 'App-Name',
    'Client JSON file': 'Client-JSON-Datei',
    'Register app': 'App registrieren',
    'Mail over IMAP and SMTP': 'E-Mail über IMAP und SMTP',
  },
  fr: {
    'Accounts that DAGs in this workspace use by name. Dagu keeps and refreshes their tokens.':
      'Comptes que les DAG de cet espace de travail utilisent par leur nom. Dagu conserve et renouvelle leurs jetons.',
    Account: 'Compte',
    'Loading connections…': 'Chargement des connexions…',
    'No connections in this workspace.':
      'Aucune connexion dans cet espace de travail.',
    'Loading tools…': 'Chargement des outils…',
    'This connection has no MCP services.':
      'Cette connexion n’a aucun service MCP.',
    'Listed {time}': 'Listé le {time}',
    'The service lists no tools.': 'Le service ne liste aucun outil.',
    'Read only': 'Lecture seule',
    'Connection not found': 'Connexion introuvable',
    'No connection {name} in workspace {workspace}.':
      'Aucune connexion {name} dans l’espace de travail {workspace}.',
    'Back to connections': 'Retour aux connexions',
    "Couldn't load the connection.": 'Impossible de charger la connexion.',
    'The test failed.': 'Le test a échoué.',
    'Could not remove the connection.': 'Impossible de supprimer la connexion.',
    Connect: 'Connecter',
    Reconnect: 'Reconnecter',
    'Test results': 'Résultats du test',
    'Last refreshed': 'Dernier renouvellement',
    'Sign-in ends': 'Fin de la connexion',
    'Read and write': 'Lecture et écriture',
    Read: 'Lecture',
    'Not granted. Reconnect to ask for the missing scopes:':
      'Non accordé. Reconnectez pour demander les portées manquantes :',
    'Granted scopes': 'Portées accordées',
    'Remove connection': 'Supprimer la connexion',
    'Remove {name}? Its tokens and tool lists are deleted, and DAGs that name it fail when they start.':
      'Supprimer {name} ? Ses jetons et ses listes d’outils sont supprimés, et les DAG qui la nomment échouent au démarrage.',
    'Dagu also revokes its sign-in at Google, so connections on other servers that use a client of the same Google Cloud project may lose access.':
      'Dagu révoque aussi sa connexion chez Google : les connexions d’autres serveurs qui utilisent un client du même projet Google Cloud peuvent perdre l’accès.',
    'Dagu also revokes its sign-in at the provider, so connections on other servers that use the same client may lose access.':
      'Dagu révoque aussi sa connexion chez le fournisseur : les connexions d’autres serveurs qui utilisent le même client peuvent perdre l’accès.',
    'Copy {name}': 'Copier : {name}',
    'Connections are kept on each server':
      'Les connexions sont conservées sur chaque serveur',
    'Remote nodes do not serve their connections. Select the local server to see its connections.':
      'Les nœuds distants ne servent pas leurs connexions. Sélectionnez le serveur local pour voir ses connexions.',
    'Could not start signing in.': 'Impossible de démarrer la connexion.',
    'Could not sign in.': 'Impossible de se connecter.',
    'Could not confirm.': 'Impossible de confirmer.',
    'Connect {name}': 'Connecter {name}',
    'Reconnect {name}': 'Reconnecter {name}',
    'The live tokens stay in use until you confirm the account that signed in.':
      'Les jetons actuels restent utilisés jusqu’à ce que vous confirmiez le compte connecté.',
    '{name} is connected.': '{name} est connectée.',
    'Signing in failed: {reason}': 'La connexion a échoué : {reason}',
    "This server has no public URL, so a web client can't receive the sign-in. Set server.public_url, or use an app with a desktop client.":
      'Ce serveur n’a pas d’URL publique : un client web ne peut pas recevoir la connexion. Définissez server.public_url ou utilisez une application avec un client de bureau.',
    'After you consent, the popup ends on a page that fails to load. Copy that page’s address from the popup and paste it here.':
      'Après votre consentement, la fenêtre s’arrête sur une page qui ne se charge pas. Copiez l’adresse de cette page depuis la fenêtre et collez-la ici.',
    'Redirect URI to register in the client':
      'URI de redirection à enregistrer dans le client',
    'Client ID, which a Workspace admin may have to trust':
      'ID client, qu’un administrateur Workspace doit parfois approuver',
    'Address the popup ended on': 'Adresse finale de la fenêtre',
    'Confirm account': 'Confirmer le compte',
    Continue: 'Continuer',
    'Open sign-in': 'Ouvrir la connexion',
    'The browser blocked the popup. Open the sign-in page instead.':
      'Le navigateur a bloqué la fenêtre. Ouvrez plutôt la page de connexion.',
    'Waiting for consent in the popup…':
      'En attente du consentement dans la fenêtre…',
    'Open sign-in page': 'Ouvrir la page de connexion',
    'Copy sign-in link': 'Copier le lien de connexion',
    'The account’s owner may sign in from their own browser with the copied link. The session ends at {time}.':
      'Le titulaire du compte peut se connecter depuis son propre navigateur avec le lien copié. La session se termine à {time}.',
    'The session ends at {time}.': 'La session se termine à {time}.',
    'Signed in as': 'Connecté en tant que',
    'This is not {account}, the account the connection holds now. Confirming replaces it.':
      'Ce n’est pas {account}, le compte actuel de la connexion. Confirmer le remplace.',
    'Other connections in this workspace already hold this account through the same client: {names}.':
      'D’autres connexions de cet espace de travail utilisent déjà ce compte avec le même client : {names}.',
    'The sign-in ends {date}.': 'La connexion prend fin le {date}.',
    'Service checks': 'Vérifications des services',
    'Needs reconnect': 'À reconnecter',
    Broken: 'Endommagée',
    'Could not create the connection.': 'Impossible de créer la connexion.',
    'Connect Google Workspace': 'Connecter Google Workspace',
    'The connection belongs to workspace {workspace}.':
      'La connexion appartient à l’espace de travail {workspace}.',
    'Create and sign in': 'Créer et se connecter',
    'Gmail, Calendar, Drive, Docs, and more.':
      'Gmail, Calendar, Drive, Docs et plus encore.',
    'Not available yet.': 'Pas encore disponible.',
    'DAGs name the connection with this. Up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit.':
      'Les DAG nomment la connexion ainsi. Jusqu’à 63 lettres minuscules, chiffres et tirets, commençant par une lettre ou un chiffre.',
    'Access for {service}': 'Accès pour {service}',
    'Preview services work only for accounts in Google’s Workspace Developer Preview program.':
      'Les services en préversion ne fonctionnent que pour les comptes inscrits au programme Workspace Developer Preview de Google.',
    'Scopes to ask for': 'Portées à demander',
    'AI agents': 'Agents IA',
    'Steps call Google’s MCP servers with mcp.call, and AI steps use their tools.':
      'Les étapes appellent les serveurs MCP de Google avec mcp.call, et les étapes IA utilisent leurs outils.',
    'Other automation and mail': 'Autres automatisations et e-mail',
    'Steps call Google’s REST APIs with http.request, and mail accounts use the mailbox.':
      'Les étapes appellent les API REST de Google avec http.request, et les comptes e-mail utilisent la boîte aux lettres.',
    'Who will use this connection?': 'Qui utilisera cette connexion ?',
    'Google requires separate OAuth clients for AI agents and for other automation, so each connection uses one kind. For both, add two connections.':
      'Google exige des clients OAuth distincts pour les agents IA et pour les autres automatisations : chaque connexion en utilise un seul type. Pour les deux, ajoutez deux connexions.',
    'OAuth app': 'Application OAuth',
    'No Google app for AI agents fits this server yet.':
      'Aucune application Google pour agents IA ne convient encore à ce serveur.',
    'No Google app for other automation fits this server yet.':
      'Aucune application Google pour les autres automatisations ne convient encore à ce serveur.',
    'Register another app': 'Enregistrer une autre application',
    'DAGs that run on this server, rather than on a worker, run as Dagu’s user, so their authors can read this connection’s tokens.':
      'Les DAG exécutés sur ce serveur, et non sur un worker, s’exécutent sous l’utilisateur de Dagu : leurs auteurs peuvent donc lire les jetons de cette connexion.',
    'Ask an admin to register a Google OAuth app with:':
      'Demandez à un administrateur d’enregistrer une application OAuth Google avec :',
    'Client type: Web application': 'Type de client : application Web',
    'Client type: Desktop app': 'Type de client : application de bureau',
    '“Use this client for an AI-powered agent” ticked':
      '« Use this client for an AI-powered agent » coché',
    '“Use this client for an AI-powered agent” not ticked':
      '« Use this client for an AI-powered agent » non coché',
    'Could not register the app.': 'Impossible d’enregistrer l’application.',
    'Enable the APIs': 'Activer les API',
    'Enable APIs in Google Cloud': 'Activer les API dans Google Cloud',
    'The chosen services need no API.':
      'Les services choisis ne nécessitent aucune API.',
    'Set up the consent screen': 'Configurer l’écran de consentement',
    'Open the consent screen': 'Ouvrir l’écran de consentement',
    'An app in Testing status accepts only its test users, and their sign-ins end after seven days. Publish the app to Production, or make it Internal in a Workspace organization, to avoid both.':
      'Une application en statut Testing n’accepte que ses utilisateurs de test, et leurs connexions expirent après sept jours. Publiez l’application en Production, ou rendez-la Internal dans une organisation Workspace, pour éviter les deux.',
    'An app that Google has not verified shows “Google hasn’t verified this app” at sign-in. That is expected for your own app.':
      'Une application que Google n’a pas vérifiée affiche « Google hasn’t verified this app » à la connexion. C’est normal pour votre propre application.',
    'Create the OAuth client': 'Créer le client OAuth',
    'Open Google Cloud clients': 'Ouvrir les clients Google Cloud',
    'Choose the application type Web application.':
      'Choisissez le type d’application Application Web.',
    'Choose the application type Desktop app. This server has no public URL, so the sign-in ends on a page whose address you paste back.':
      'Choisissez le type d’application Application de bureau. Ce serveur n’a pas d’URL publique : la connexion se termine sur une page dont vous recollez l’adresse.',
    'Tick “Use this client for an AI-powered agent”.':
      'Cochez « Use this client for an AI-powered agent ».',
    'Upload the client JSON': 'Téléverser le JSON du client',
    'App name': 'Nom de l’application',
    'Client JSON file': 'Fichier JSON du client',
    'Register app': 'Enregistrer l’application',
    'Mail over IMAP and SMTP': 'E-mail via IMAP et SMTP',
  },
});
