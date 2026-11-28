// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

/** The static messages of the worker invites on System status. */
export const workerAdminMessages = defineMessages({
  en: {
    'Only admins can manage workers and invites.':
      'Only admins can manage workers and invites.',
    'Sign in with an admin account, or ask an admin.':
      'Sign in with an admin account, or ask an admin.',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.',
    'No coordinator is running or reachable.':
      'No coordinator is running or reachable.',
    'Start the coordinator, or check that this server can reach it, then try again.':
      'Start the coordinator, or check that this server can reach it, then try again.',
    'The request failed.': 'The request failed.',
    Used: 'Used',
    'Single use': 'Single use',
    Reusable: 'Reusable',
    'Loading invites...': 'Loading invites...',
    'No invites': 'No invites',
    'Created by': 'Created by',
    Expiry: 'Expiry',
    'No expiry': 'No expiry',
    Uses: 'Uses',
    'Last use': 'Last use',
    State: 'State',
    Invites: 'Invites',
    'Invite {id}': 'Invite {id}',
    'Delete invite': 'Delete invite',
    'Delete invite {id} ({kind})': 'Delete invite {id} ({kind})',
    'Invite {id}: {kind}, created {time}':
      'Invite {id}: {kind}, created {time}',
    By: 'By',
    use: 'use',
    uses: 'uses',
    'This invite no longer exists.': 'This invite no longer exists.',
    'It was deleted, or it ended more than 7 days ago.':
      'It was deleted, or it ended more than 7 days ago.',
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'No new worker can join with this invite. Workers that joined with it keep their certificates.',
    'Delete works on the local server only. Switch to it to delete an invite.':
      'Delete works on the local server only. Switch to it to delete an invite.',
    'The coordinator uses its own certificates, so it has no built-in CA for invites.':
      'The coordinator uses its own certificates, so it has no built-in CA for invites.',
    'The coordinator could not create or read its CA.':
      'The coordinator could not create or read its CA.',
    'See the coordinator start warning, fix its CA, then restart it.':
      'See the coordinator start warning, fix its CA, then restart it.',
    'This server has no peer token for the coordinator.':
      'This server has no peer token for the coordinator.',
    'Set peer.token on this server to the peer token of the coordinator.':
      'Set peer.token on this server to the peer token of the coordinator.',
    'This server and the coordinator have different peer tokens.':
      'This server and the coordinator have different peer tokens.',
    'Give the server the same peer.token as the coordinator.':
      'Give the server the same peer.token as the coordinator.',
    'The coordinator requires TLS, and this server called it over plaintext.':
      'The coordinator requires TLS, and this server called it over plaintext.',
    'Remove {setting} from the server config, then restart it.':
      'Remove {setting} from the server config, then restart it.',
    'The coordinator refused the server.':
      'The coordinator refused the server.',
    'Make the change the message names, then try again.':
      'Make the change the message names, then try again.',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'The coordinator runs an earlier Dagu release without worker invites.',
    'Upgrade the coordinator.': 'Upgrade the coordinator.',
    'This invite is already gone.': 'This invite is already gone.',
    'The list is read again. Close this dialog.':
      'The list is read again. Close this dialog.',
  },
  'zh-CN': {
    'Only admins can manage workers and invites.':
      '只有管理员可以管理 Worker 和邀请。',
    'Sign in with an admin account, or ask an admin.':
      '请使用管理员账号登录，或请管理员操作。',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      '要使用邀请，请从 Coordinator 配置中删除 peer.cert_file 和 peer.key_file，然后重启。',
    'No coordinator is running or reachable.':
      '没有正在运行或可访问的 Coordinator。',
    'Start the coordinator, or check that this server can reach it, then try again.':
      '请启动 Coordinator，或检查此服务器能否访问它，然后重试。',
    'The request failed.': '请求失败。',
    Used: '已使用',
    'Single use': '一次性',
    Reusable: '可重复使用',
    'Loading invites...': '正在加载邀请...',
    'No invites': '没有邀请',
    'Created by': '创建者',
    Expiry: '过期时间',
    'No expiry': '不过期',
    Uses: '使用次数',
    'Last use': '最近使用',
    State: '状态',
    Invites: '邀请',
    'Invite {id}': '邀请 {id}',
    'Delete invite': '删除邀请',
    'Delete invite {id} ({kind})': '删除邀请 {id}（{kind}）',
    'Invite {id}: {kind}, created {time}': '邀请 {id}：{kind}，创建于 {time}',
    By: '创建者',
    use: '次使用',
    uses: '次使用',
    'This invite no longer exists.': '此邀请已不存在。',
    'It was deleted, or it ended more than 7 days ago.':
      '它已被删除，或已结束超过 7 天。',
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      '新的 Worker 将无法通过此邀请加入。已通过它加入的 Worker 保留其证书。',
    'Delete works on the local server only. Switch to it to delete an invite.':
      '删除仅适用于本地服务器。请切换到本地服务器后删除邀请。',
    'The coordinator uses its own certificates, so it has no built-in CA for invites.':
      '该 Coordinator 使用自己的证书，因此没有用于邀请的内置 CA。',
    'The coordinator could not create or read its CA.':
      'Coordinator 无法创建或读取其 CA。',
    'See the coordinator start warning, fix its CA, then restart it.':
      '请查看 Coordinator 启动时的警告，修复其 CA，然后重启。',
    'This server has no peer token for the coordinator.':
      '此服务器没有用于 Coordinator 的 peer token。',
    'Set peer.token on this server to the peer token of the coordinator.':
      '请在此服务器上将 peer.token 设置为 Coordinator 的 peer token。',
    'This server and the coordinator have different peer tokens.':
      '此服务器与 Coordinator 的 peer token 不同。',
    'Give the server the same peer.token as the coordinator.':
      '请为服务器设置与 Coordinator 相同的 peer.token。',
    'The coordinator requires TLS, and this server called it over plaintext.':
      'Coordinator 要求 TLS，而此服务器以明文调用了它。',
    'Remove {setting} from the server config, then restart it.':
      '请从服务器配置中删除 {setting}，然后重启。',
    'The coordinator refused the server.': 'Coordinator 拒绝了此服务器。',
    'Make the change the message names, then try again.':
      '请按消息所述进行修改，然后重试。',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'Coordinator 运行的是不支持 Worker 邀请的较早 Dagu 版本。',
    'Upgrade the coordinator.': '请升级 Coordinator。',
    'This invite is already gone.': '此邀请已不存在。',
    'The list is read again. Close this dialog.':
      '列表将重新读取。请关闭此对话框。',
  },
  ja: {
    'Only admins can manage workers and invites.':
      'ワーカーと招待を管理できるのは管理者だけです。',
    'Sign in with an admin account, or ask an admin.':
      '管理者アカウントでサインインするか、管理者に依頼してください。',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      '招待を使うには、コーディネーターの設定から peer.cert_file と peer.key_file を削除して再起動してください。',
    'No coordinator is running or reachable.':
      '動作中または到達可能なコーディネーターがありません。',
    'Start the coordinator, or check that this server can reach it, then try again.':
      'コーディネーターを起動するか、このサーバーから到達できるか確認してから、もう一度お試しください。',
    'The request failed.': 'リクエストに失敗しました。',
    Used: '使用済み',
    'Single use': '1 回限り',
    Reusable: '再利用可能',
    'Loading invites...': '招待を読み込み中...',
    'No invites': '招待はありません',
    'Created by': '作成者',
    Expiry: '有効期限',
    'No expiry': '期限なし',
    Uses: '使用回数',
    'Last use': '最終使用',
    State: '状態',
    Invites: '招待',
    'Invite {id}': '招待 {id}',
    'Delete invite': '招待を削除',
    'Delete invite {id} ({kind})': '招待 {id}（{kind}）を削除',
    'Invite {id}: {kind}, created {time}': '招待 {id}：{kind}、{time} に作成',
    By: '作成者',
    use: '回使用',
    uses: '回使用',
    'This invite no longer exists.': 'この招待はもう存在しません。',
    'It was deleted, or it ended more than 7 days ago.':
      '削除されたか、終了から 7 日以上たっています。',
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'この招待で新しいワーカーは参加できなくなります。参加済みのワーカーは証明書を保持します。',
    'Delete works on the local server only. Switch to it to delete an invite.':
      '削除はローカルサーバーでのみ使えます。ローカルサーバーに切り替えてから招待を削除してください。',
    'The coordinator uses its own certificates, so it has no built-in CA for invites.':
      'コーディネーターは独自の証明書を使っているため、招待用の内蔵 CA がありません。',
    'The coordinator could not create or read its CA.':
      'コーディネーターが CA を作成または読み込めませんでした。',
    'See the coordinator start warning, fix its CA, then restart it.':
      'コーディネーターの起動時の警告を確認して CA を直し、再起動してください。',
    'This server has no peer token for the coordinator.':
      'このサーバーにはコーディネーター用の peer token がありません。',
    'Set peer.token on this server to the peer token of the coordinator.':
      'このサーバーの peer.token にコーディネーターの peer token を設定してください。',
    'This server and the coordinator have different peer tokens.':
      'このサーバーとコーディネーターの peer token が異なります。',
    'Give the server the same peer.token as the coordinator.':
      'サーバーにコーディネーターと同じ peer.token を設定してください。',
    'The coordinator requires TLS, and this server called it over plaintext.':
      'コーディネーターは TLS を必要としますが、このサーバーは平文で呼び出しました。',
    'Remove {setting} from the server config, then restart it.':
      'サーバーの設定から {setting} を削除して再起動してください。',
    'The coordinator refused the server.':
      'コーディネーターがこのサーバーを拒否しました。',
    'Make the change the message names, then try again.':
      'メッセージにある変更を行ってから、もう一度お試しください。',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'コーディネーターはワーカー招待のない以前の Dagu を動かしています。',
    'Upgrade the coordinator.':
      'コーディネーターをアップグレードしてください。',
    'This invite is already gone.': 'この招待はすでにありません。',
    'The list is read again. Close this dialog.':
      '一覧を読み直します。このダイアログを閉じてください。',
  },
  de: {
    'Only admins can manage workers and invites.':
      'Nur Admins können Worker und Einladungen verwalten.',
    'Sign in with an admin account, or ask an admin.':
      'Melden Sie sich mit einem Admin-Konto an, oder fragen Sie einen Admin.',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      'Um Einladungen zu nutzen, entfernen Sie peer.cert_file und peer.key_file aus der Coordinator-Konfiguration und starten Sie ihn neu.',
    'No coordinator is running or reachable.':
      'Kein Coordinator läuft oder ist erreichbar.',
    'Start the coordinator, or check that this server can reach it, then try again.':
      'Starten Sie den Coordinator oder prüfen Sie, ob dieser Server ihn erreicht, und versuchen Sie es dann erneut.',
    'The request failed.': 'Die Anfrage ist fehlgeschlagen.',
    Used: 'Verwendet',
    'Single use': 'Einmalig',
    Reusable: 'Wiederverwendbar',
    'Loading invites...': 'Einladungen werden geladen …',
    'No invites': 'Keine Einladungen',
    'Created by': 'Erstellt von',
    Expiry: 'Ablauf',
    'No expiry': 'Kein Ablauf',
    Uses: 'Verwendungen',
    'Last use': 'Zuletzt verwendet',
    State: 'Status',
    Invites: 'Einladungen',
    'Invite {id}': 'Einladung {id}',
    'Delete invite': 'Einladung löschen',
    'Delete invite {id} ({kind})': 'Einladung {id} ({kind}) löschen',
    'Invite {id}: {kind}, created {time}':
      'Einladung {id}: {kind}, erstellt {time}',
    By: 'Von',
    use: 'Verwendung',
    uses: 'Verwendungen',
    'This invite no longer exists.': 'Diese Einladung existiert nicht mehr.',
    'It was deleted, or it ended more than 7 days ago.':
      'Sie wurde gelöscht oder ist vor mehr als 7 Tagen abgelaufen.',
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'Mit dieser Einladung kann kein neuer Worker mehr beitreten. Worker, die damit beigetreten sind, behalten ihre Zertifikate.',
    'Delete works on the local server only. Switch to it to delete an invite.':
      'Löschen funktioniert nur auf dem lokalen Server. Wechseln Sie zu ihm, um eine Einladung zu löschen.',
    'The coordinator uses its own certificates, so it has no built-in CA for invites.':
      'Der Coordinator nutzt eigene Zertifikate und hat daher keine eingebaute CA für Einladungen.',
    'The coordinator could not create or read its CA.':
      'Der Coordinator konnte seine CA nicht erstellen oder lesen.',
    'See the coordinator start warning, fix its CA, then restart it.':
      'Lesen Sie die Startwarnung des Coordinators, beheben Sie seine CA und starten Sie ihn neu.',
    'This server has no peer token for the coordinator.':
      'Dieser Server hat kein Peer-Token für den Coordinator.',
    'Set peer.token on this server to the peer token of the coordinator.':
      'Setzen Sie peer.token auf diesem Server auf das Peer-Token des Coordinators.',
    'This server and the coordinator have different peer tokens.':
      'Dieser Server und der Coordinator haben verschiedene Peer-Tokens.',
    'Give the server the same peer.token as the coordinator.':
      'Geben Sie dem Server dasselbe peer.token wie dem Coordinator.',
    'The coordinator requires TLS, and this server called it over plaintext.':
      'Der Coordinator verlangt TLS, und dieser Server hat ihn im Klartext aufgerufen.',
    'Remove {setting} from the server config, then restart it.':
      'Entfernen Sie {setting} aus der Serverkonfiguration und starten Sie ihn neu.',
    'The coordinator refused the server.':
      'Der Coordinator hat den Server abgelehnt.',
    'Make the change the message names, then try again.':
      'Nehmen Sie die Änderung vor, die die Meldung nennt, und versuchen Sie es erneut.',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'Der Coordinator läuft mit einer älteren Dagu-Version ohne Worker-Einladungen.',
    'Upgrade the coordinator.': 'Aktualisieren Sie den Coordinator.',
    'This invite is already gone.': 'Diese Einladung gibt es schon nicht mehr.',
    'The list is read again. Close this dialog.':
      'Die Liste wird neu gelesen. Schließen Sie diesen Dialog.',
  },
  fr: {
    'Only admins can manage workers and invites.':
      'Seuls les admins peuvent gérer les workers et les invitations.',
    'Sign in with an admin account, or ask an admin.':
      'Connectez-vous avec un compte admin, ou demandez à un admin.',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      'Pour utiliser les invitations, retirez peer.cert_file et peer.key_file de la configuration du coordinateur, puis redémarrez-le.',
    'No coordinator is running or reachable.':
      'Aucun coordinateur n’est lancé ou joignable.',
    'Start the coordinator, or check that this server can reach it, then try again.':
      'Lancez le coordinateur, ou vérifiez que ce serveur peut le joindre, puis réessayez.',
    'The request failed.': 'La requête a échoué.',
    Used: 'Utilisée',
    'Single use': 'Usage unique',
    Reusable: 'Réutilisable',
    'Loading invites...': 'Chargement des invitations…',
    'No invites': 'Aucune invitation',
    'Created by': 'Créée par',
    Expiry: 'Expiration',
    'No expiry': 'Sans expiration',
    Uses: 'Utilisations',
    'Last use': 'Dernière utilisation',
    State: 'État',
    Invites: 'Invitations',
    'Invite {id}': 'Invitation {id}',
    'Delete invite': 'Supprimer l’invitation',
    'Delete invite {id} ({kind})': 'Supprimer l’invitation {id} ({kind})',
    'Invite {id}: {kind}, created {time}':
      'Invitation {id} : {kind}, créée le {time}',
    By: 'Par',
    use: 'utilisation',
    uses: 'utilisations',
    'This invite no longer exists.': 'Cette invitation n’existe plus.',
    'It was deleted, or it ended more than 7 days ago.':
      'Elle a été supprimée, ou elle a pris fin il y a plus de 7 jours.',
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'Aucun nouveau worker ne peut rejoindre avec cette invitation. Les workers qui l’ont utilisée gardent leurs certificats.',
    'Delete works on the local server only. Switch to it to delete an invite.':
      'La suppression ne fonctionne que sur le serveur local. Passez-y pour supprimer une invitation.',
    'The coordinator uses its own certificates, so it has no built-in CA for invites.':
      'Le coordinateur utilise ses propres certificats, il n’a donc pas d’AC intégrée pour les invitations.',
    'The coordinator could not create or read its CA.':
      'Le coordinateur n’a pas pu créer ou lire son AC.',
    'See the coordinator start warning, fix its CA, then restart it.':
      'Consultez l’avertissement de démarrage du coordinateur, corrigez son AC, puis redémarrez-le.',
    'This server has no peer token for the coordinator.':
      'Ce serveur n’a pas de jeton de pair pour le coordinateur.',
    'Set peer.token on this server to the peer token of the coordinator.':
      'Définissez sur ce serveur peer.token avec le jeton de pair du coordinateur.',
    'This server and the coordinator have different peer tokens.':
      'Ce serveur et le coordinateur ont des jetons de pair différents.',
    'Give the server the same peer.token as the coordinator.':
      'Donnez au serveur le même peer.token que le coordinateur.',
    'The coordinator requires TLS, and this server called it over plaintext.':
      'Le coordinateur exige TLS, et ce serveur l’a appelé en clair.',
    'Remove {setting} from the server config, then restart it.':
      'Retirez {setting} de la configuration du serveur, puis redémarrez-le.',
    'The coordinator refused the server.':
      'Le coordinateur a refusé le serveur.',
    'Make the change the message names, then try again.':
      'Faites la modification que le message indique, puis réessayez.',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'Le coordinateur exécute une version antérieure de Dagu sans invitations de workers.',
    'Upgrade the coordinator.': 'Mettez à jour le coordinateur.',
    'This invite is already gone.': 'Cette invitation n’existe déjà plus.',
    'The list is read again. Close this dialog.':
      'La liste est relue. Fermez cette boîte de dialogue.',
  },
});
