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
    Tools: 'Tools',
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
    Tools: '工具',
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
    Tools: 'ツール',
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
    Tools: 'Tools',
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
    Tools: 'Outils',
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
  },
});
