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
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'No new worker can join with this invite. Workers that joined with it keep their certificates.',
    'Delete works only for the server this page belongs to. To delete an invite of the selected node, open the Dagu UI of that node.':
      'Delete works only for the server this page belongs to. To delete an invite of the selected node, open the Dagu UI of that node.',
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
    'Clear {insecure} or {skipVerify} in the server config if set, and set {caFile} to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.':
      'Clear {insecure} or {skipVerify} in the server config if set, and set {caFile} to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.',
    'The coordinator refused the server.':
      'The coordinator refused the server.',
    'Check the coordinator log for the reason, or run {command} on this host, then try again.':
      'Check the coordinator log for the reason, or run {command} on this host, then try again.',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'The coordinator runs an earlier Dagu release without worker invites.',
    'Upgrade the coordinator.': 'Upgrade the coordinator.',
    'This invite is already gone.': 'This invite is already gone.',
    'The list is read again. Close this dialog.':
      'The list is read again. Close this dialog.',
    'The coordinator refused the request.':
      'The coordinator refused the request.',
    'The selected server does not offer worker invites.':
      'The selected server does not offer worker invites.',
    'Check that it runs a Dagu release with worker invites.':
      'Check that it runs a Dagu release with worker invites.',
    'Server message: “{message}”': 'Server message: “{message}”',
    'The coordinator could not do this in its current state.':
      'The coordinator could not do this in its current state.',
    'The coordinator answered but cannot do this yet.':
      'The coordinator answered but cannot do this yet.',
    'Wait a moment, then try again.': 'Wait a moment, then try again.',
    'This worker connects with the peer token, so it would connect again at once.':
      'This worker connects with the peer token, so it would connect again at once.',
    'Stop it on its host, or change the peer token.':
      'Stop it on its host, or change the peer token.',
    'This worker no longer holds the certificate the list showed, as after it joined again.':
      'This worker no longer holds the certificate the list showed, as after it joined again.',
    'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.':
      'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.',
    'Check the coordinator log for the state that conflicts, then try again once it is resolved.':
      'Check the coordinator log for the state that conflicts, then try again once it is resolved.',
    'The credentials this server uses for the selected node lack admin access there.':
      'The credentials this server uses for the selected node lack admin access there.',
    'Give those credentials the admin role on the selected node.':
      'Give those credentials the admin role on the selected node.',
    'Remove {worker}': 'Remove {worker}',
    'Removing (1 run left)': 'Removing (1 run left)',
    'Removing ({count} runs left)': 'Removing ({count} runs left)',
    'Stop runs now': 'Stop runs now',
    'Stop runs now on {worker}': 'Stop runs now on {worker}',
    'A worker on TLS or Plaintext has no certificate and no Remove, since it would connect again at once. To stop one, stop it on its host, or change the peer token if the coordinator requires one.':
      'A worker on TLS or Plaintext has no certificate and no Remove, since it would connect again at once. To stop one, stop it on its host, or change the peer token if the coordinator requires one.',
    'This worker is no longer listed.': 'This worker is no longer listed.',
    'It may be removed already. The list shows its state.':
      'It may be removed already. The list shows its state.',
    'Stop its runs and remove': 'Stop its runs and remove',
    'Its certificate is revoked, and it stays out across restarts until it gets a new invite.':
      'Its certificate is revoked, and it stays out across restarts until it gets a new invite.',
    'It has no running runs.': 'It has no running runs.',
    'It has 1 running run:': 'It has 1 running run:',
    'It has {count} running runs:': 'It has {count} running runs:',
    'Remove after its runs finish': 'Remove after its runs finish',
    'It gets no new runs, and is removed when its last run ends.':
      'It gets no new runs, and is removed when its last run ends.',
    'Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended.':
      'Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended.',
    'It joined with a reusable invite, which still lets new workers join, a replacement for this one included.':
      'It joined with a reusable invite, which still lets new workers join, a replacement for this one included.',
    'Other workers on this invite: {workers}':
      'Other workers on this invite: {workers}',
    'Also delete the invite': 'Also delete the invite',
    'Workers that joined with it keep their certificates.':
      'Workers that joined with it keep their certificates.',
    'It runs in Kubernetes: its pod keeps restarting until its Deployment is scaled down.':
      'It runs in Kubernetes: its pod keeps restarting until its Deployment is scaled down.',
    'A replacement pod can join with the invite while it is active, so delete the invite as well.':
      'A replacement pod can join with the invite while it is active, so delete the invite as well.',
    'The invite it joined with could not be read, so this cannot say whether the invite still lets new workers join. Remove still works.':
      'The invite it joined with could not be read, so this cannot say whether the invite still lets new workers join. Remove still works.',
    'Stop the runs of {worker}': 'Stop the runs of {worker}',
    'Remove and Stop runs now work on the local server only. Switch to it to use them.':
      'Remove and Stop runs now work on the local server only. Switch to it to use them.',
    'Stop runs': 'Stop runs',
    'Its run gets a cancel request. The worker is removed within two minutes, whether or not the run has ended.':
      'Its run gets a cancel request. The worker is removed within two minutes, whether or not the run has ended.',
    'Each of its {count} runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.':
      'Each of its {count} runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.',
    'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.':
      'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.',
    'The invite it joined with no longer exists, so it lets no new worker join.':
      'The invite it joined with no longer exists, so it lets no new worker join.',
    'The coordinator runs an earlier Dagu release that cannot remove workers.':
      'The coordinator runs an earlier Dagu release that cannot remove workers.',
    'The server accepted the removal.': 'The server accepted the removal.',
    'The invite was not deleted.': 'The invite was not deleted.',
    'Delete the invite': 'Delete the invite',
    'The coordinator uses its own certificates, so it has no worker certificates to remove.':
      'The coordinator uses its own certificates, so it has no worker certificates to remove.',
    'Stop the worker on its host, or change the peer token.':
      'Stop the worker on its host, or change the peer token.',
    'Invite to delete: {id} ({kind})': 'Invite to delete: {id} ({kind})',
    'To try again, close this dialog and confirm again from the list.':
      'To try again, close this dialog and confirm again from the list.',
    'Invite {id} was not deleted.': 'Invite {id} was not deleted.',
    'The selected node has no peer token for its coordinator.':
      'The selected node has no peer token for its coordinator.',
    'Set peer.token on the selected node to the peer token of its coordinator.':
      'Set peer.token on the selected node to the peer token of its coordinator.',
    'The selected node and its coordinator have different peer tokens.':
      'The selected node and its coordinator have different peer tokens.',
    'Give the selected node the same peer.token as its coordinator.':
      'Give the selected node the same peer.token as its coordinator.',
    'The coordinator requires TLS, and the selected node called it over plaintext.':
      'The coordinator requires TLS, and the selected node called it over plaintext.',
    'Clear {insecure} or {skipVerify} in the config of the selected node if set, and set {caFile} there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.':
      'Clear {insecure} or {skipVerify} in the config of the selected node if set, and set {caFile} there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.',
    'The coordinator refused the selected node.':
      'The coordinator refused the selected node.',
    'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.':
      'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.',
    'Start the coordinator, or check that the selected node can reach it, then try again.':
      'Start the coordinator, or check that the selected node can reach it, then try again.',
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
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      '新的 Worker 将无法通过此邀请加入。已通过它加入的 Worker 保留其证书。',
    'Delete works only for the server this page belongs to. To delete an invite of the selected node, open the Dagu UI of that node.':
      '删除仅适用于此页面所属的服务器。要删除所选节点的邀请，请打开该节点的 Dagu UI。',
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
    'Clear {insecure} or {skipVerify} in the server config if set, and set {caFile} to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.':
      '如果服务器配置中设置了 {insecure} 或 {skipVerify}，请将其清除；如果此服务器没有 Coordinator 的 CA 证书，请将 {caFile} 设置为该证书。如果两者都没有问题，请检查到 Coordinator 的网络路径是否允许 TLS 通过。',
    'The coordinator refused the server.': 'Coordinator 拒绝了此服务器。',
    'Check the coordinator log for the reason, or run {command} on this host, then try again.':
      '请查看 Coordinator 日志了解原因，或在此主机上运行 {command}，然后重试。',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'Coordinator 运行的是不支持 Worker 邀请的较早 Dagu 版本。',
    'Upgrade the coordinator.': '请升级 Coordinator。',
    'This invite is already gone.': '此邀请已不存在。',
    'The list is read again. Close this dialog.':
      '列表将重新读取。请关闭此对话框。',
    'The coordinator refused the request.': 'Coordinator 拒绝了该请求。',
    'The selected server does not offer worker invites.':
      '所选服务器不提供 Worker 邀请。',
    'Check that it runs a Dagu release with worker invites.':
      '请确认它运行的 Dagu 版本支持 Worker 邀请。',
    'Server message: “{message}”': '服务器消息：“{message}”',
    'The coordinator could not do this in its current state.':
      'Coordinator 在当前状态下无法执行此操作。',
    'The coordinator answered but cannot do this yet.':
      'Coordinator 已响应，但暂时无法执行此操作。',
    'Wait a moment, then try again.': '请稍候，然后重试。',
    'This worker connects with the peer token, so it would connect again at once.':
      '此 Worker 使用 peer token 连接，因此会立即重新连接。',
    'Stop it on its host, or change the peer token.':
      '请在其主机上停止它，或更换 peer token。',
    'This worker no longer holds the certificate the list showed, as after it joined again.':
      '此 Worker 已不再持有列表中显示的证书，例如它重新加入之后。',
    'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.':
      '该 Worker 未被移除。列表现在显示该 Worker 的当前状态：请检查后，如有需要再次移除。',
    'Check the coordinator log for the state that conflicts, then try again once it is resolved.':
      '请查看 Coordinator 日志中冲突的状态，待其解决后重试。',
    'The credentials this server uses for the selected node lack admin access there.':
      '此服务器用于所选节点的凭据在该节点上没有管理员权限。',
    'Give those credentials the admin role on the selected node.':
      '请在所选节点上为这些凭据授予管理员角色。',
    'Remove {worker}': '移除 {worker}',
    'Removing (1 run left)': '正在移除（剩余 1 个运行）',
    'Removing ({count} runs left)': '正在移除（剩余 {count} 个运行）',
    'Stop runs now': '立即停止运行',
    'Stop runs now on {worker}': '立即停止 {worker} 上的运行',
    'A worker on TLS or Plaintext has no certificate and no Remove, since it would connect again at once. To stop one, stop it on its host, or change the peer token if the coordinator requires one.':
      '使用 TLS 或明文连接的 Worker 没有证书，也没有“移除”操作，因为它会立即重新连接。要停止它，请在其主机上停止；如果 Coordinator 要求 peer token，也可以更换 peer token。',
    'This worker is no longer listed.': '此 Worker 已不在列表中。',
    'It may be removed already. The list shows its state.':
      '它可能已被移除。列表显示其当前状态。',
    'Stop its runs and remove': '停止其运行并移除',
    'Its certificate is revoked, and it stays out across restarts until it gets a new invite.':
      '其证书将被吊销，在获得新邀请之前，重启后也无法重新加入。',
    'It has no running runs.': '它没有正在进行的运行。',
    'It has 1 running run:': '它有 1 个正在进行的运行：',
    'It has {count} running runs:': '它有 {count} 个正在进行的运行：',
    'Remove after its runs finish': '在其运行结束后移除',
    'It gets no new runs, and is removed when its last run ends.':
      '它不再获得新的运行，并在最后一个运行结束时被移除。',
    'Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended.':
      '每个运行都会收到取消请求，无论运行是否结束，该 Worker 都会在两分钟内被移除。',
    'It joined with a reusable invite, which still lets new workers join, a replacement for this one included.':
      '它通过可重复使用的邀请加入，该邀请仍允许新的 Worker 加入，包括替代它的 Worker。',
    'Other workers on this invite: {workers}':
      '使用此邀请的其他 Worker：{workers}',
    'Also delete the invite': '同时删除该邀请',
    'Workers that joined with it keep their certificates.':
      '已通过它加入的 Worker 保留其证书。',
    'It runs in Kubernetes: its pod keeps restarting until its Deployment is scaled down.':
      '它运行在 Kubernetes 中：在其 Deployment 缩容之前，其 Pod 会不断重启。',
    'A replacement pod can join with the invite while it is active, so delete the invite as well.':
      '邀请有效期间，替代的 Pod 可以通过它加入，因此请同时删除邀请。',
    'The invite it joined with could not be read, so this cannot say whether the invite still lets new workers join. Remove still works.':
      '无法读取它加入时使用的邀请，因此无法确定该邀请是否仍允许新的 Worker 加入。仍可以移除。',
    'Stop the runs of {worker}': '停止 {worker} 的运行',
    'Remove and Stop runs now work on the local server only. Switch to it to use them.':
      '移除和立即停止运行仅适用于本地服务器。请切换到本地服务器后使用。',
    'Stop runs': '停止运行',
    'Its run gets a cancel request. The worker is removed within two minutes, whether or not the run has ended.':
      '其运行会收到取消请求。无论运行是否结束，该 Worker 都会在两分钟内被移除。',
    'Each of its {count} runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.':
      '其 {count} 个运行都会收到取消请求。无论运行是否结束，该 Worker 都会在两分钟内被移除。',
    'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.':
      '此 Worker 已不再使用此对话框打开时显示的证书连接。请关闭此对话框，并为它重新确认。',
    'The invite it joined with no longer exists, so it lets no new worker join.':
      '它加入时使用的邀请已不存在，因此不会再有新的 Worker 通过它加入。',
    'The coordinator runs an earlier Dagu release that cannot remove workers.':
      'Coordinator 运行的是无法移除 Worker 的较早 Dagu 版本。',
    'The server accepted the removal.': '服务器已接受移除。',
    'The invite was not deleted.': '邀请未被删除。',
    'Delete the invite': '删除邀请',
    'The coordinator uses its own certificates, so it has no worker certificates to remove.':
      'Coordinator 使用自己的证书，因此没有可移除的 Worker 证书。',
    'Stop the worker on its host, or change the peer token.':
      '请在其主机上停止该 Worker，或更换 peer token。',
    'Invite to delete: {id} ({kind})': '要删除的邀请：{id}（{kind}）',
    'To try again, close this dialog and confirm again from the list.':
      '如需重试，请关闭此对话框，并从列表中重新确认。',
    'Invite {id} was not deleted.': '邀请 {id} 未被删除。',
    'The selected node has no peer token for its coordinator.':
      '所选节点没有其 Coordinator 的 peer token。',
    'Set peer.token on the selected node to the peer token of its coordinator.':
      '在所选节点上将 peer.token 设置为其 Coordinator 的 peer token。',
    'The selected node and its coordinator have different peer tokens.':
      '所选节点与其 Coordinator 的 peer token 不同。',
    'Give the selected node the same peer.token as its coordinator.':
      '为所选节点设置与其 Coordinator 相同的 peer.token。',
    'The coordinator requires TLS, and the selected node called it over plaintext.':
      'Coordinator 要求 TLS，而所选节点以明文调用了它。',
    'Clear {insecure} or {skipVerify} in the config of the selected node if set, and set {caFile} there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.':
      '如果所选节点的配置中设置了 {insecure} 或 {skipVerify}，请将其清除；如果该节点没有 Coordinator 的 CA 证书，请在该节点上将 {caFile} 设置为该证书。如果两者都没有问题，请检查从所选节点到 Coordinator 的网络路径是否允许 TLS 通过。',
    'The coordinator refused the selected node.':
      'Coordinator 拒绝了所选节点。',
    'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.':
      '请查看 Coordinator 日志了解原因，或在所选节点的主机上运行 {command}，然后重试。',
    'Start the coordinator, or check that the selected node can reach it, then try again.':
      '请启动 Coordinator，或检查所选节点能否访问它，然后重试。',
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
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'この招待で新しいワーカーは参加できなくなります。参加済みのワーカーは証明書を保持します。',
    'Delete works only for the server this page belongs to. To delete an invite of the selected node, open the Dagu UI of that node.':
      '削除はこのページのサーバーでのみ使えます。選択中のノードの招待を削除するには、そのノードの Dagu UI を開いてください。',
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
    'Clear {insecure} or {skipVerify} in the server config if set, and set {caFile} to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.':
      'サーバーの設定に {insecure} や {skipVerify} があれば外し、このサーバーにコーディネーターの CA 証明書がなければ {caFile} にそれを設定してください。どちらも問題なければ、コーディネーターまでのネットワーク経路が TLS を通すか確認してください。',
    'The coordinator refused the server.':
      'コーディネーターがこのサーバーを拒否しました。',
    'Check the coordinator log for the reason, or run {command} on this host, then try again.':
      'コーディネーターのログで理由を確認するか、このホストで {command} を実行してから、もう一度お試しください。',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'コーディネーターはワーカー招待のない以前の Dagu を動かしています。',
    'Upgrade the coordinator.':
      'コーディネーターをアップグレードしてください。',
    'This invite is already gone.': 'この招待はすでにありません。',
    'The list is read again. Close this dialog.':
      '一覧を読み直します。このダイアログを閉じてください。',
    'The coordinator refused the request.':
      'コーディネーターがリクエストを拒否しました。',
    'The selected server does not offer worker invites.':
      '選択中のサーバーはワーカー招待に対応していません。',
    'Check that it runs a Dagu release with worker invites.':
      'ワーカー招待に対応した Dagu を動かしているか確認してください。',
    'Server message: “{message}”': 'サーバーのメッセージ：「{message}」',
    'The coordinator could not do this in its current state.':
      'コーディネーターは現在の状態ではこの操作を行えません。',
    'The coordinator answered but cannot do this yet.':
      'コーディネーターは応答しましたが、まだこの操作を行えません。',
    'Wait a moment, then try again.':
      'しばらく待ってから、もう一度お試しください。',
    'This worker connects with the peer token, so it would connect again at once.':
      'このワーカーは peer token で接続しているため、すぐに再接続します。',
    'Stop it on its host, or change the peer token.':
      'そのホストで停止するか、peer token を変更してください。',
    'This worker no longer holds the certificate the list showed, as after it joined again.':
      'このワーカーは、一覧に表示した証明書をもう持っていません。再参加した後などに起こります。',
    'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.':
      'ワーカーは削除されていません。一覧にワーカーの現在の状態が表示されます。確認して、必要なら削除し直してください。',
    'Check the coordinator log for the state that conflicts, then try again once it is resolved.':
      'コーディネーターのログで競合している状態を確認し、解消してからもう一度お試しください。',
    'The credentials this server uses for the selected node lack admin access there.':
      'このサーバーが選択中のノードに使う認証情報には、そのノードの管理者権限がありません。',
    'Give those credentials the admin role on the selected node.':
      '選択中のノードで、その認証情報に管理者ロールを付与してください。',
    'Remove {worker}': '{worker} を削除',
    'Removing (1 run left)': '削除中（残り 1 件の実行）',
    'Removing ({count} runs left)': '削除中（残り {count} 件の実行）',
    'Stop runs now': '今すぐ実行を停止',
    'Stop runs now on {worker}': '{worker} の実行を今すぐ停止',
    'A worker on TLS or Plaintext has no certificate and no Remove, since it would connect again at once. To stop one, stop it on its host, or change the peer token if the coordinator requires one.':
      'TLS または平文で接続するワーカーには証明書がなく、すぐに再接続するため「削除」はありません。停止するには、そのホストで停止するか、コーディネーターが peer token を要求する場合はそれを変更してください。',
    'This worker is no longer listed.': 'このワーカーはもう一覧にありません。',
    'It may be removed already. The list shows its state.':
      'すでに削除された可能性があります。一覧に現在の状態が表示されます。',
    'Stop its runs and remove': '実行を停止して削除',
    'Its certificate is revoked, and it stays out across restarts until it gets a new invite.':
      '証明書が失効し、新しい招待を受け取るまで再起動しても参加できません。',
    'It has no running runs.': '実行中の実行はありません。',
    'It has 1 running run:': '実行中の実行が 1 件あります：',
    'It has {count} running runs:': '実行中の実行が {count} 件あります：',
    'Remove after its runs finish': '実行の終了後に削除',
    'It gets no new runs, and is removed when its last run ends.':
      '新しい実行を受け取らず、最後の実行が終わると削除されます。',
    'Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended.':
      '各実行に取り消し要求を送り、実行が終わったかどうかにかかわらず、2 分以内にワーカーを削除します。',
    'It joined with a reusable invite, which still lets new workers join, a replacement for this one included.':
      '再利用可能な招待で参加しています。この招待では、このワーカーの代わりを含め、今後も新しいワーカーが参加できます。',
    'Other workers on this invite: {workers}':
      'この招待を使う他のワーカー：{workers}',
    'Also delete the invite': '招待も削除',
    'Workers that joined with it keep their certificates.':
      '参加済みのワーカーは証明書を保持します。',
    'It runs in Kubernetes: its pod keeps restarting until its Deployment is scaled down.':
      'Kubernetes で動いています。Deployment をスケールダウンするまで Pod は再起動を続けます。',
    'A replacement pod can join with the invite while it is active, so delete the invite as well.':
      '招待が有効な間は代わりの Pod がそれで参加できるため、招待も削除してください。',
    'The invite it joined with could not be read, so this cannot say whether the invite still lets new workers join. Remove still works.':
      '参加に使った招待を読み込めなかったため、その招待で今も新しいワーカーが参加できるかは分かりません。削除はできます。',
    'Stop the runs of {worker}': '{worker} の実行を停止',
    'Remove and Stop runs now work on the local server only. Switch to it to use them.':
      '削除と今すぐ実行を停止はローカルサーバーでのみ使えます。ローカルサーバーに切り替えてから使ってください。',
    'Stop runs': '実行を停止',
    'Its run gets a cancel request. The worker is removed within two minutes, whether or not the run has ended.':
      '実行に取り消し要求を送ります。実行が終わったかどうかにかかわらず、2 分以内にワーカーを削除します。',
    'Each of its {count} runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.':
      '{count} 件の実行それぞれに取り消し要求を送ります。実行が終わったかどうかにかかわらず、2 分以内にワーカーを削除します。',
    'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.':
      'このワーカーは、このダイアログを開いたときに表示した証明書で接続していません。このダイアログを閉じて、改めて確認してください。',
    'The invite it joined with no longer exists, so it lets no new worker join.':
      '参加に使った招待はもう存在しないため、新しいワーカーはそれで参加できません。',
    'The coordinator runs an earlier Dagu release that cannot remove workers.':
      'コーディネーターはワーカーを削除できない以前の Dagu を動かしています。',
    'The server accepted the removal.': 'サーバーは削除を受け付けました。',
    'The invite was not deleted.': '招待は削除されていません。',
    'Delete the invite': '招待を削除',
    'The coordinator uses its own certificates, so it has no worker certificates to remove.':
      'コーディネーターは独自の証明書を使っているため、削除できるワーカー証明書がありません。',
    'Stop the worker on its host, or change the peer token.':
      'そのホストでワーカーを停止するか、peer token を変更してください。',
    'Invite to delete: {id} ({kind})': '削除する招待：{id}（{kind}）',
    'To try again, close this dialog and confirm again from the list.':
      'やり直すには、このダイアログを閉じて一覧から改めて確認してください。',
    'Invite {id} was not deleted.': '招待 {id} は削除されませんでした。',
    'The selected node has no peer token for its coordinator.':
      '選択したノードには、そのコーディネーターの peer token がありません。',
    'Set peer.token on the selected node to the peer token of its coordinator.':
      '選択したノードの peer.token に、そのコーディネーターの peer token を設定してください。',
    'The selected node and its coordinator have different peer tokens.':
      '選択したノードとそのコーディネーターの peer token が異なります。',
    'Give the selected node the same peer.token as its coordinator.':
      '選択したノードに、そのコーディネーターと同じ peer.token を設定してください。',
    'The coordinator requires TLS, and the selected node called it over plaintext.':
      'コーディネーターは TLS を必要としますが、選択したノードは平文で呼び出しました。',
    'Clear {insecure} or {skipVerify} in the config of the selected node if set, and set {caFile} there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.':
      '選択したノードの設定に {insecure} や {skipVerify} があれば外し、そのノードにコーディネーターの CA 証明書がなければ、そこで {caFile} にそれを設定してください。どちらも問題なければ、選択したノードからコーディネーターまでのネットワーク経路が TLS を通すか確認してください。',
    'The coordinator refused the selected node.':
      'コーディネーターが選択したノードを拒否しました。',
    'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.':
      'コーディネーターのログで理由を確認するか、選択したノードのホストで {command} を実行してから、もう一度お試しください。',
    'Start the coordinator, or check that the selected node can reach it, then try again.':
      'コーディネーターを起動するか、選択したノードから到達できるか確認してから、もう一度お試しください。',
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
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'Mit dieser Einladung kann kein neuer Worker mehr beitreten. Worker, die damit beigetreten sind, behalten ihre Zertifikate.',
    'Delete works only for the server this page belongs to. To delete an invite of the selected node, open the Dagu UI of that node.':
      'Löschen funktioniert nur für den Server, zu dem diese Seite gehört. Um eine Einladung des ausgewählten Knotens zu löschen, öffnen Sie die Dagu-Oberfläche dieses Knotens.',
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
    'Clear {insecure} or {skipVerify} in the server config if set, and set {caFile} to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.':
      'Entfernen Sie {insecure} oder {skipVerify} aus der Serverkonfiguration, falls gesetzt, und setzen Sie {caFile} auf das CA-Zertifikat des Coordinators, falls dieser Server keines hat. Ist beides in Ordnung, prüfen Sie, ob der Netzwerkpfad zum Coordinator TLS durchlässt.',
    'The coordinator refused the server.':
      'Der Coordinator hat den Server abgelehnt.',
    'Check the coordinator log for the reason, or run {command} on this host, then try again.':
      'Sehen Sie im Log des Coordinators nach dem Grund, oder führen Sie {command} auf diesem Host aus, und versuchen Sie es dann erneut.',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'Der Coordinator läuft mit einer älteren Dagu-Version ohne Worker-Einladungen.',
    'Upgrade the coordinator.': 'Aktualisieren Sie den Coordinator.',
    'This invite is already gone.': 'Diese Einladung gibt es schon nicht mehr.',
    'The list is read again. Close this dialog.':
      'Die Liste wird neu gelesen. Schließen Sie diesen Dialog.',
    'The coordinator refused the request.':
      'Der Coordinator hat die Anfrage abgelehnt.',
    'The selected server does not offer worker invites.':
      'Der ausgewählte Server bietet keine Worker-Einladungen an.',
    'Check that it runs a Dagu release with worker invites.':
      'Prüfen Sie, ob er eine Dagu-Version mit Worker-Einladungen ausführt.',
    'Server message: “{message}”': 'Servermeldung: „{message}“',
    'The coordinator could not do this in its current state.':
      'Der Coordinator konnte dies in seinem aktuellen Zustand nicht ausführen.',
    'The coordinator answered but cannot do this yet.':
      'Der Coordinator hat geantwortet, kann dies aber noch nicht ausführen.',
    'Wait a moment, then try again.':
      'Warten Sie einen Moment und versuchen Sie es dann erneut.',
    'This worker connects with the peer token, so it would connect again at once.':
      'Dieser Worker verbindet sich mit dem Peer-Token und würde sich daher sofort wieder verbinden.',
    'Stop it on its host, or change the peer token.':
      'Stoppen Sie ihn auf seinem Host, oder ändern Sie das Peer-Token.',
    'This worker no longer holds the certificate the list showed, as after it joined again.':
      'Dieser Worker hat das Zertifikat aus der Liste nicht mehr, etwa nachdem er erneut beigetreten ist.',
    'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.':
      'Der Worker wurde nicht entfernt. Die Liste zeigt den Worker jetzt, wie er ist: Prüfen Sie ihn und entfernen Sie ihn bei Bedarf erneut.',
    'Check the coordinator log for the state that conflicts, then try again once it is resolved.':
      'Sehen Sie im Log des Coordinators nach dem Zustand, der im Konflikt steht, und versuchen Sie es erneut, sobald er behoben ist.',
    'The credentials this server uses for the selected node lack admin access there.':
      'Die Zugangsdaten, die dieser Server für den ausgewählten Knoten nutzt, haben dort keine Admin-Rechte.',
    'Give those credentials the admin role on the selected node.':
      'Geben Sie diesen Zugangsdaten auf dem ausgewählten Knoten die Admin-Rolle.',
    'Remove {worker}': '{worker} entfernen',
    'Removing (1 run left)': 'Wird entfernt (1 Lauf übrig)',
    'Removing ({count} runs left)': 'Wird entfernt ({count} Läufe übrig)',
    'Stop runs now': 'Läufe jetzt stoppen',
    'Stop runs now on {worker}': 'Läufe auf {worker} jetzt stoppen',
    'A worker on TLS or Plaintext has no certificate and no Remove, since it would connect again at once. To stop one, stop it on its host, or change the peer token if the coordinator requires one.':
      'Ein Worker über TLS oder Klartext hat kein Zertifikat und kein Entfernen, da er sich sofort wieder verbinden würde. Um ihn zu stoppen, stoppen Sie ihn auf seinem Host, oder ändern Sie das Peer-Token, wenn der Coordinator eines verlangt.',
    'This worker is no longer listed.':
      'Dieser Worker ist nicht mehr gelistet.',
    'It may be removed already. The list shows its state.':
      'Er ist vielleicht schon entfernt. Die Liste zeigt seinen Zustand.',
    'Stop its runs and remove': 'Läufe stoppen und entfernen',
    'Its certificate is revoked, and it stays out across restarts until it gets a new invite.':
      'Sein Zertifikat wird widerrufen, und er bleibt auch nach Neustarts ausgeschlossen, bis er eine neue Einladung erhält.',
    'It has no running runs.': 'Er hat keine laufenden Läufe.',
    'It has 1 running run:': 'Er hat 1 laufenden Lauf:',
    'It has {count} running runs:': 'Er hat {count} laufende Läufe:',
    'Remove after its runs finish': 'Nach Ende seiner Läufe entfernen',
    'It gets no new runs, and is removed when its last run ends.':
      'Er erhält keine neuen Läufe und wird entfernt, wenn sein letzter Lauf endet.',
    'Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended.':
      'Jeder Lauf erhält eine Abbruchanfrage, und der Worker wird innerhalb von zwei Minuten entfernt, ob seine Läufe geendet haben oder nicht.',
    'It joined with a reusable invite, which still lets new workers join, a replacement for this one included.':
      'Er ist mit einer wiederverwendbaren Einladung beigetreten, mit der weiterhin neue Worker beitreten können, auch ein Ersatz für ihn.',
    'Other workers on this invite: {workers}':
      'Andere Worker mit dieser Einladung: {workers}',
    'Also delete the invite': 'Auch die Einladung löschen',
    'Workers that joined with it keep their certificates.':
      'Worker, die damit beigetreten sind, behalten ihre Zertifikate.',
    'It runs in Kubernetes: its pod keeps restarting until its Deployment is scaled down.':
      'Er läuft in Kubernetes: Sein Pod startet immer wieder neu, bis sein Deployment herunterskaliert wird.',
    'A replacement pod can join with the invite while it is active, so delete the invite as well.':
      'Ein Ersatz-Pod kann mit der Einladung beitreten, solange sie aktiv ist. Löschen Sie daher auch die Einladung.',
    'The invite it joined with could not be read, so this cannot say whether the invite still lets new workers join. Remove still works.':
      'Die Einladung, mit der er beigetreten ist, konnte nicht gelesen werden. Daher ist unklar, ob mit ihr noch neue Worker beitreten können. Entfernen funktioniert trotzdem.',
    'Stop the runs of {worker}': 'Läufe von {worker} stoppen',
    'Remove and Stop runs now work on the local server only. Switch to it to use them.':
      'Entfernen und Läufe jetzt stoppen funktionieren nur auf dem lokalen Server. Wechseln Sie zu ihm, um sie zu nutzen.',
    'Stop runs': 'Läufe stoppen',
    'Its run gets a cancel request. The worker is removed within two minutes, whether or not the run has ended.':
      'Sein Lauf erhält eine Abbruchanfrage. Der Worker wird innerhalb von zwei Minuten entfernt, ob der Lauf geendet hat oder nicht.',
    'Each of its {count} runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.':
      'Jeder seiner {count} Läufe erhält eine Abbruchanfrage. Der Worker wird innerhalb von zwei Minuten entfernt, ob sie geendet haben oder nicht.',
    'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.':
      'Dieser Worker verbindet sich nicht mehr mit dem Zertifikat, das beim Öffnen dieses Dialogs angezeigt wurde. Schließen Sie diesen Dialog und bestätigen Sie für ihn erneut.',
    'The invite it joined with no longer exists, so it lets no new worker join.':
      'Die Einladung, mit der er beigetreten ist, gibt es nicht mehr, daher kann kein neuer Worker damit beitreten.',
    'The coordinator runs an earlier Dagu release that cannot remove workers.':
      'Der Coordinator läuft mit einer älteren Dagu-Version, die keine Worker entfernen kann.',
    'The server accepted the removal.':
      'Der Server hat das Entfernen angenommen.',
    'The invite was not deleted.': 'Die Einladung wurde nicht gelöscht.',
    'Delete the invite': 'Einladung löschen',
    'The coordinator uses its own certificates, so it has no worker certificates to remove.':
      'Der Coordinator nutzt eigene Zertifikate und hat daher keine Worker-Zertifikate zum Entfernen.',
    'Stop the worker on its host, or change the peer token.':
      'Stoppen Sie den Worker auf seinem Host, oder ändern Sie das Peer-Token.',
    'Invite to delete: {id} ({kind})': 'Zu löschende Einladung: {id} ({kind})',
    'To try again, close this dialog and confirm again from the list.':
      'Um es erneut zu versuchen, schließen Sie diesen Dialog und bestätigen Sie erneut aus der Liste.',
    'Invite {id} was not deleted.': 'Die Einladung {id} wurde nicht gelöscht.',
    'The selected node has no peer token for its coordinator.':
      'Der ausgewählte Knoten hat kein Peer-Token für seinen Coordinator.',
    'Set peer.token on the selected node to the peer token of its coordinator.':
      'Setzen Sie peer.token auf dem ausgewählten Knoten auf das Peer-Token seines Coordinators.',
    'The selected node and its coordinator have different peer tokens.':
      'Der ausgewählte Knoten und sein Coordinator haben unterschiedliche Peer-Tokens.',
    'Give the selected node the same peer.token as its coordinator.':
      'Geben Sie dem ausgewählten Knoten dasselbe peer.token wie seinem Coordinator.',
    'The coordinator requires TLS, and the selected node called it over plaintext.':
      'Der Coordinator verlangt TLS, und der ausgewählte Knoten hat ihn im Klartext aufgerufen.',
    'Clear {insecure} or {skipVerify} in the config of the selected node if set, and set {caFile} there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.':
      'Entfernen Sie {insecure} oder {skipVerify} aus der Konfiguration des ausgewählten Knotens, falls gesetzt, und setzen Sie dort {caFile} auf das CA-Zertifikat des Coordinators, falls er keines hat. Ist beides in Ordnung, prüfen Sie, ob der Netzwerkpfad vom ausgewählten Knoten zum Coordinator TLS durchlässt.',
    'The coordinator refused the selected node.':
      'Der Coordinator hat den ausgewählten Knoten abgelehnt.',
    'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.':
      'Prüfen Sie das Protokoll des Coordinators auf den Grund, oder führen Sie {command} auf dem Host des ausgewählten Knotens aus, und versuchen Sie es dann erneut.',
    'Start the coordinator, or check that the selected node can reach it, then try again.':
      'Starten Sie den Coordinator, oder prüfen Sie, ob der ausgewählte Knoten ihn erreichen kann, und versuchen Sie es dann erneut.',
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
    'No new worker can join with this invite. Workers that joined with it keep their certificates.':
      'Aucun nouveau worker ne peut rejoindre avec cette invitation. Les workers qui l’ont utilisée gardent leurs certificats.',
    'Delete works only for the server this page belongs to. To delete an invite of the selected node, open the Dagu UI of that node.':
      'La suppression ne fonctionne que pour le serveur de cette page. Pour supprimer une invitation du nœud sélectionné, ouvrez l’interface Dagu de ce nœud.',
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
    'Clear {insecure} or {skipVerify} in the server config if set, and set {caFile} to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.':
      'Retirez {insecure} ou {skipVerify} de la configuration du serveur s’ils y figurent, et définissez {caFile} sur le certificat d’AC du coordinateur si ce serveur n’en a pas. Si les deux sont en ordre, vérifiez que le chemin réseau vers le coordinateur laisse passer TLS.',
    'The coordinator refused the server.':
      'Le coordinateur a refusé le serveur.',
    'Check the coordinator log for the reason, or run {command} on this host, then try again.':
      'Consultez le journal du coordinateur pour connaître la raison, ou lancez {command} sur cet hôte, puis réessayez.',
    'The coordinator runs an earlier Dagu release without worker invites.':
      'Le coordinateur exécute une version antérieure de Dagu sans invitations de workers.',
    'Upgrade the coordinator.': 'Mettez à jour le coordinateur.',
    'This invite is already gone.': 'Cette invitation n’existe déjà plus.',
    'The list is read again. Close this dialog.':
      'La liste est relue. Fermez cette boîte de dialogue.',
    'The coordinator refused the request.':
      'Le coordinateur a refusé la requête.',
    'The selected server does not offer worker invites.':
      'Le serveur sélectionné ne propose pas d’invitations de workers.',
    'Check that it runs a Dagu release with worker invites.':
      'Vérifiez qu’il exécute une version de Dagu avec les invitations de workers.',
    'Server message: “{message}”': 'Message du serveur : « {message} »',
    'The coordinator could not do this in its current state.':
      'Le coordinateur n’a pas pu le faire dans son état actuel.',
    'The coordinator answered but cannot do this yet.':
      'Le coordinateur a répondu mais ne peut pas encore le faire.',
    'Wait a moment, then try again.': 'Patientez un instant, puis réessayez.',
    'This worker connects with the peer token, so it would connect again at once.':
      'Ce worker se connecte avec le jeton de pair, il se reconnecterait donc aussitôt.',
    'Stop it on its host, or change the peer token.':
      'Arrêtez-le sur son hôte, ou changez le jeton de pair.',
    'This worker no longer holds the certificate the list showed, as after it joined again.':
      'Ce worker n’a plus le certificat que la liste montrait, par exemple après avoir rejoint à nouveau.',
    'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.':
      'Le worker n’a pas été retiré. La liste montre maintenant le worker tel qu’il est : vérifiez-le, puis retirez-le à nouveau si besoin.',
    'Check the coordinator log for the state that conflicts, then try again once it is resolved.':
      'Consultez le journal du coordinateur pour l’état en conflit, puis réessayez une fois qu’il est résolu.',
    'The credentials this server uses for the selected node lack admin access there.':
      'Les identifiants que ce serveur utilise pour le nœud sélectionné n’y ont pas les droits d’admin.',
    'Give those credentials the admin role on the selected node.':
      'Donnez à ces identifiants le rôle admin sur le nœud sélectionné.',
    'Remove {worker}': 'Supprimer {worker}',
    'Removing (1 run left)': 'Retrait en cours (1 exécution restante)',
    'Removing ({count} runs left)':
      'Retrait en cours ({count} exécutions restantes)',
    'Stop runs now': 'Arrêter les exécutions maintenant',
    'Stop runs now on {worker}':
      'Arrêter les exécutions maintenant sur {worker}',
    'A worker on TLS or Plaintext has no certificate and no Remove, since it would connect again at once. To stop one, stop it on its host, or change the peer token if the coordinator requires one.':
      'Un worker en TLS ou en clair n’a ni certificat ni retrait, car il se reconnecterait aussitôt. Pour l’arrêter, arrêtez-le sur son hôte, ou changez le jeton de pair si le coordinateur en exige un.',
    'This worker is no longer listed.': 'Ce worker n’est plus listé.',
    'It may be removed already. The list shows its state.':
      'Il est peut-être déjà retiré. La liste montre son état.',
    'Stop its runs and remove': 'Arrêter ses exécutions et le retirer',
    'Its certificate is revoked, and it stays out across restarts until it gets a new invite.':
      'Son certificat est révoqué et il reste exclu après chaque redémarrage, jusqu’à ce qu’il reçoive une nouvelle invitation.',
    'It has no running runs.': 'Il n’a aucune exécution en cours.',
    'It has 1 running run:': 'Il a 1 exécution en cours :',
    'It has {count} running runs:': 'Il a {count} exécutions en cours :',
    'Remove after its runs finish': 'Retirer après la fin de ses exécutions',
    'It gets no new runs, and is removed when its last run ends.':
      'Il ne reçoit plus de nouvelles exécutions et il est retiré à la fin de la dernière.',
    'Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended.':
      'Chaque exécution reçoit une demande d’annulation, et le worker est retiré dans les deux minutes, que ses exécutions soient terminées ou non.',
    'It joined with a reusable invite, which still lets new workers join, a replacement for this one included.':
      'Il a rejoint avec une invitation réutilisable, qui laisse encore rejoindre de nouveaux workers, y compris un remplaçant pour celui-ci.',
    'Other workers on this invite: {workers}':
      'Autres workers de cette invitation : {workers}',
    'Also delete the invite': 'Supprimer aussi l’invitation',
    'Workers that joined with it keep their certificates.':
      'Les workers qui l’ont utilisée gardent leurs certificats.',
    'It runs in Kubernetes: its pod keeps restarting until its Deployment is scaled down.':
      'Il tourne dans Kubernetes : son pod redémarre sans cesse tant que son Deployment n’est pas réduit à zéro.',
    'A replacement pod can join with the invite while it is active, so delete the invite as well.':
      'Un pod de remplacement peut rejoindre avec l’invitation tant qu’elle est active. Supprimez donc aussi l’invitation.',
    'The invite it joined with could not be read, so this cannot say whether the invite still lets new workers join. Remove still works.':
      'L’invitation qu’il a utilisée n’a pas pu être lue, on ne peut donc pas dire si elle laisse encore rejoindre de nouveaux workers. Le retrait reste possible.',
    'Stop the runs of {worker}': 'Arrêter les exécutions de {worker}',
    'Remove and Stop runs now work on the local server only. Switch to it to use them.':
      'Le retrait et l’arrêt immédiat des exécutions ne fonctionnent que sur le serveur local. Passez-y pour les utiliser.',
    'Stop runs': 'Arrêter les exécutions',
    'Its run gets a cancel request. The worker is removed within two minutes, whether or not the run has ended.':
      'Son exécution reçoit une demande d’annulation. Le worker est retiré dans les deux minutes, que l’exécution soit terminée ou non.',
    'Each of its {count} runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.':
      'Chacune de ses {count} exécutions reçoit une demande d’annulation. Le worker est retiré dans les deux minutes, qu’elles soient terminées ou non.',
    'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.':
      'Ce worker ne se connecte plus avec le certificat affiché à l’ouverture de cette boîte de dialogue. Fermez-la et confirmez à nouveau pour lui.',
    'The invite it joined with no longer exists, so it lets no new worker join.':
      'L’invitation qu’il a utilisée n’existe plus, aucun nouveau worker ne peut donc rejoindre avec elle.',
    'The coordinator runs an earlier Dagu release that cannot remove workers.':
      'Le coordinateur exécute une version antérieure de Dagu qui ne peut pas retirer de workers.',
    'The server accepted the removal.': 'Le serveur a accepté le retrait.',
    'The invite was not deleted.': 'L’invitation n’a pas été supprimée.',
    'Delete the invite': 'Supprimer l’invitation',
    'The coordinator uses its own certificates, so it has no worker certificates to remove.':
      'Le coordinateur utilise ses propres certificats, il n’a donc aucun certificat de worker à retirer.',
    'Stop the worker on its host, or change the peer token.':
      'Arrêtez le worker sur son hôte, ou changez le jeton de pair.',
    'Invite to delete: {id} ({kind})': 'Invitation à supprimer : {id} ({kind})',
    'To try again, close this dialog and confirm again from the list.':
      'Pour réessayer, fermez cette boîte de dialogue et confirmez à nouveau depuis la liste.',
    'Invite {id} was not deleted.': 'L’invitation {id} n’a pas été supprimée.',
    'The selected node has no peer token for its coordinator.':
      'Le nœud sélectionné n’a pas de jeton de pair pour son coordinateur.',
    'Set peer.token on the selected node to the peer token of its coordinator.':
      'Définissez peer.token sur le nœud sélectionné avec le jeton de pair de son coordinateur.',
    'The selected node and its coordinator have different peer tokens.':
      'Le nœud sélectionné et son coordinateur ont des jetons de pair différents.',
    'Give the selected node the same peer.token as its coordinator.':
      'Donnez au nœud sélectionné le même peer.token que son coordinateur.',
    'The coordinator requires TLS, and the selected node called it over plaintext.':
      'Le coordinateur exige TLS, et le nœud sélectionné l’a appelé en clair.',
    'Clear {insecure} or {skipVerify} in the config of the selected node if set, and set {caFile} there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.':
      'Retirez {insecure} ou {skipVerify} de la configuration du nœud sélectionné s’ils y figurent, et définissez-y {caFile} sur le certificat d’AC du coordinateur s’il n’en a pas. Si les deux sont en ordre, vérifiez que le chemin réseau du nœud sélectionné vers le coordinateur laisse passer TLS.',
    'The coordinator refused the selected node.':
      'Le coordinateur a refusé le nœud sélectionné.',
    'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.':
      'Consultez le journal du coordinateur pour en connaître la raison, ou exécutez {command} sur l’hôte du nœud sélectionné, puis réessayez.',
    'Start the coordinator, or check that the selected node can reach it, then try again.':
      'Démarrez le coordinateur, ou vérifiez que le nœud sélectionné peut le joindre, puis réessayez.',
  },
});
