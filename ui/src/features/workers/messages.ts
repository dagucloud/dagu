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
    'The coordinator refused the request.':
      'The coordinator refused the request.',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.',
    'Nothing changed. Make the change the message names.':
      'Nothing changed. Make the change the message names.',
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
  },
  'zh-CN': {
    'Only admins can manage workers and invites.':
      '只有管理员可以管理 Worker 和邀请。',
    'Sign in with an admin account, or ask an admin.':
      '请使用管理员账号登录，或请管理员操作。',
    'The coordinator refused the request.': 'Coordinator 拒绝了该请求。',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      '要使用邀请，请从 Coordinator 配置中删除 peer.cert_file 和 peer.key_file，然后重启。',
    'Nothing changed. Make the change the message names.':
      '没有任何更改。请按消息所述进行修改。',
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
  },
  ja: {
    'Only admins can manage workers and invites.':
      'ワーカーと招待を管理できるのは管理者だけです。',
    'Sign in with an admin account, or ask an admin.':
      '管理者アカウントでサインインするか、管理者に依頼してください。',
    'The coordinator refused the request.':
      'コーディネーターがリクエストを拒否しました。',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      '招待を使うには、コーディネーターの設定から peer.cert_file と peer.key_file を削除して再起動してください。',
    'Nothing changed. Make the change the message names.':
      '何も変更されていません。メッセージにある変更を行ってください。',
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
  },
  de: {
    'Only admins can manage workers and invites.':
      'Nur Admins können Worker und Einladungen verwalten.',
    'Sign in with an admin account, or ask an admin.':
      'Melden Sie sich mit einem Admin-Konto an, oder fragen Sie einen Admin.',
    'The coordinator refused the request.':
      'Der Coordinator hat die Anfrage abgelehnt.',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      'Um Einladungen zu nutzen, entfernen Sie peer.cert_file und peer.key_file aus der Coordinator-Konfiguration und starten Sie ihn neu.',
    'Nothing changed. Make the change the message names.':
      'Nichts wurde geändert. Nehmen Sie die Änderung vor, die die Meldung nennt.',
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
  },
  fr: {
    'Only admins can manage workers and invites.':
      'Seuls les admins peuvent gérer les workers et les invitations.',
    'Sign in with an admin account, or ask an admin.':
      'Connectez-vous avec un compte admin, ou demandez à un admin.',
    'The coordinator refused the request.':
      'Le coordinateur a refusé la requête.',
    'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.':
      'Pour utiliser les invitations, retirez peer.cert_file et peer.key_file de la configuration du coordinateur, puis redémarrez-le.',
    'Nothing changed. Make the change the message names.':
      'Rien n’a changé. Faites la modification que le message indique.',
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
  },
});
