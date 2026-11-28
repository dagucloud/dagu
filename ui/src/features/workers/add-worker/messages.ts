// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

/** The static messages of the Add worker dialog. */
export const addWorkerMessages = defineMessages({
  en: {
    'Copy the change': 'Copy the change',
    'The coordinator cannot do this yet. Try again in a moment.':
      'The coordinator cannot do this yet. Try again in a moment.',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.',
    'Shown after step 1': 'Shown after step 1',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} is a multicast or broadcast address. Enter the address of one machine.',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.',
    'Run the chart in distributed mode, whose coordinator listens on 0.0.0.0, and remove any DAGU_COORDINATOR_HOST from extraEnv. Then upgrade the release:':
      'Run the chart in distributed mode, whose coordinator listens on 0.0.0.0, and remove any DAGU_COORDINATOR_HOST from extraEnv. Then upgrade the release:',
    'Enter host:port, such as 192.168.1.20:50055.':
      'Enter host:port, such as 192.168.1.20:50055.',
    'The address holds an invite. The invite goes only into the commands.':
      'The address holds an invite. The invite goes only into the commands.',
    'Address and port': 'Address and port',
    '{host} is not one host. Enter an IP address without a zone, or a DNS name.':
      '{host} is not one host. Enter an IP address without a zone, or a DNS name.',
    '{host} is a link-local address, which another network does not route. Enter another address of the machine.':
      '{host} is a link-local address, which another network does not route. Enter another address of the machine.',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again.':
      'Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again.',
    'This coordinator did not report. Start or upgrade it, then check again.':
      'This coordinator did not report. Start or upgrade it, then check again.',
    'The server refused the address. Check it and try again.':
      'The server refused the address. Check it and try again.',
    'The coordinator refused this server. Its log names the reason.':
      'The coordinator refused this server. Its log names the reason.',
    "The coordinator requires TLS. Remove the peer.insecure setting from this server's config, then restart it.":
      "The coordinator requires TLS. Remove the peer.insecure setting from this server's config, then restart it.",
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.",
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.",
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.',
    'Switch to the local server to add a worker.':
      'Switch to the local server to add a worker.',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} looks like a container ID. Enter the address of the machine that runs the container.',
    'Add worker': 'Add worker',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      'Run a worker on another machine. It joins this Dagu with an invite.',
    'Accept workers from other hosts': 'Accept workers from other hosts',
    Ready: 'Ready',
    'Needs change': 'Needs change',
    'Only admins can add workers.': 'Only admins can add workers.',
    'The request failed.': 'The request failed.',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.',
    'The coordinator listens on {host}, so only this machine can connect.':
      'The coordinator listens on {host}, so only this machine can connect.',
    'Recreate the container with these options:':
      'Recreate the container with these options:',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.',
    'Invites need the built-in CA, which this coordinator does not have.':
      'Invites need the built-in CA, which this coordinator does not have.',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.",
    'Address workers use to reach the coordinator':
      'Address workers use to reach the coordinator',
    'Addresses of this machine:': 'Addresses of this machine:',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.',
    'Enter the address workers use to reach the coordinator.':
      'Enter the address workers use to reach the coordinator.',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} is a listen address. Enter an address that workers can dial.',
    'Enter a port from 1 to 65535.': 'Enter a port from 1 to 65535.',
    'Shown after steps 1 and 2': 'Shown after steps 1 and 2',
  },
  'zh-CN': {
    'Copy the change': '复制修改内容',
    'The coordinator cannot do this yet. Try again in a moment.':
      '协调器暂时无法执行此操作。请稍后重试。',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      '协调器在当前状态下无法执行此操作。其日志说明了原因；修复后请重试。',
    'Shown after step 1': '完成步骤 1 后显示',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} 是多播或广播地址。请输入一台机器的地址。',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      '参数 {name} 设置了 coordinator.host。在启动 Dagu 的地方将其设为 0.0.0.0：',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      '在 {file} 中，于 coordinator 块内将 host 设为 0.0.0.0；如果文件中没有该块，请添加。',
    'Run the chart in distributed mode, whose coordinator listens on 0.0.0.0, and remove any DAGU_COORDINATOR_HOST from extraEnv. Then upgrade the release:':
      '以分布式模式运行 Chart（其协调器监听 0.0.0.0），并从 extraEnv 中删除任何 DAGU_COORDINATOR_HOST。然后升级 Release：',
    'Enter host:port, such as 192.168.1.20:50055.':
      '请输入 host:port，例如 192.168.1.20:50055。',
    'The address holds an invite. The invite goes only into the commands.':
      '地址中包含邀请。邀请只应放在命令中。',
    'Address and port': '地址和端口',
    '{host} is not one host. Enter an IP address without a zone, or a DNS name.':
      '{host} 不是单个主机。请输入不带区域的 IP 地址或 DNS 名称。',
    '{host} is a link-local address, which another network does not route. Enter another address of the machine.':
      '{host} 是链路本地地址，其他网络不会路由到它。请输入该机器的其他地址。',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again.':
      '每项修改都需要重启 Dagu。服务器恢复后此对话框会重新检查；如果没有，请点击“重新检查”。',
    'This coordinator did not report. Start or upgrade it, then check again.':
      '此协调器未报告。请启动或升级它，然后重新检查。',
    'The server refused the address. Check it and try again.':
      '服务器拒绝了该地址。请检查后重试。',
    'The coordinator refused this server. Its log names the reason.':
      '协调器拒绝了此服务器。其日志说明了原因。',
    "The coordinator requires TLS. Remove the peer.insecure setting from this server's config, then restart it.":
      '协调器要求 TLS。请从此服务器的配置中删除 peer.insecure 设置，然后重启它。',
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      '此服务器的 peer.token 与协调器的不同。请在两者上设置相同的 peer.token，然后重启它们。',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      '此服务器没有协调器的 peer token。请将此服务器的 peer.token 设为与协调器相同，然后重启它。',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      '没有正在运行或可访问的协调器。请启动一个（例如使用 dagu start-all），然后重新检查。',
    'Switch to the local server to add a worker.':
      '切换到本地服务器以添加 Worker。',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} 看起来是容器 ID。请输入运行该容器的机器的地址。',
    'Add worker': '添加 Worker',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      '在另一台机器上运行 Worker。它通过邀请加入此 Dagu。',
    'Accept workers from other hosts': '接受来自其他主机的 Worker',
    Ready: '就绪',
    'Needs change': '需要修改',
    'Only admins can add workers.': '只有管理员可以添加 Worker。',
    'The request failed.': '请求失败。',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      '协调器来自较早的 Dagu 版本。升级所有协调器后才能添加 Worker。',
    'The coordinator listens on {host}, so only this machine can connect.':
      '协调器监听 {host}，因此只有本机可以连接。',
    'Recreate the container with these options:': '使用以下选项重新创建容器：',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      '变量 {name} 设置了 coordinator.host。在启动 Dagu 的地方将其设为 0.0.0.0：',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      '邀请需要内置 CA，但此协调器使用 peer.cert_file 和 peer.key_file。删除这两项即可使用邀请，或使用你自己的证书连接每个 Worker。',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      '邀请需要内置 CA，但此协调器无法创建或读取它。其启动警告说明了原因。',
    'Invites need the built-in CA, which this coordinator does not have.':
      '邀请需要内置 CA，而此协调器没有。',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      '为容器发布端口 {port}，例如使用 -p {port}:{port}。Dagu 无法看到容器发布了哪些端口。',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      '集群外的 Worker 需要协调器 Service 的类型为 LoadBalancer 或 NodePort，可通过 Chart 的 coordinator.service.type 设置。',
    'Address workers use to reach the coordinator':
      'Worker 用于连接协调器的地址',
    'Addresses of this machine:': '本机地址：',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      '输入运行该容器的机器的地址。Dagu 在容器内无法看到它。',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      '输入协调器 Service 的地址：其负载均衡器，或 NodePort 所用的节点。',
    'Enter the address workers use to reach the coordinator.':
      '输入 Worker 用于连接协调器的地址。',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} 只能访问其所在的机器。请输入其他主机上的 Worker 可以访问的地址。',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} 是监听地址。请输入 Worker 可以连接的地址。',
    'Enter a port from 1 to 65535.': '请输入 1 到 65535 之间的端口。',
    'Shown after steps 1 and 2': '完成步骤 1 和 2 后显示',
  },
  ja: {
    'Copy the change': '変更をコピー',
    'The coordinator cannot do this yet. Try again in a moment.':
      'コーディネーターはまだこれを実行できません。少し待ってからもう一度試してください。',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'コーディネーターは現在の状態ではこれを実行できません。理由はそのログにあります。修正してからもう一度試してください。',
    'Shown after step 1': 'ステップ 1 の後に表示',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} はマルチキャストまたはブロードキャストのアドレスです。1 台のマシンのアドレスを入力してください。',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'フラグ {name} が coordinator.host を設定しています。Dagu を起動する場所で 0.0.0.0 を指定してください:',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      '{file} の coordinator ブロック内で host を 0.0.0.0 にしてください。ブロックがなければ追加してください。',
    'Run the chart in distributed mode, whose coordinator listens on 0.0.0.0, and remove any DAGU_COORDINATOR_HOST from extraEnv. Then upgrade the release:':
      'チャートを分散モードで実行し (そのコーディネーターは 0.0.0.0 で待ち受けます)、extraEnv から DAGU_COORDINATOR_HOST を削除してください。その後リリースをアップグレードしてください:',
    'Enter host:port, such as 192.168.1.20:50055.':
      '192.168.1.20:50055 のように host:port を入力してください。',
    'The address holds an invite. The invite goes only into the commands.':
      'アドレスに招待が含まれています。招待はコマンドにだけ入れます。',
    'Address and port': 'アドレスとポート',
    '{host} is not one host. Enter an IP address without a zone, or a DNS name.':
      '{host} は 1 つのホストではありません。ゾーンなしの IP アドレスか DNS 名を入力してください。',
    '{host} is a link-local address, which another network does not route. Enter another address of the machine.':
      '{host} はリンクローカルアドレスで、他のネットワークからはルーティングされません。マシンの別のアドレスを入力してください。',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again.':
      'どの変更も Dagu の再起動が必要です。サーバーが戻るとこのダイアログが再確認します。そうならない場合は「再確認」を使ってください。',
    'This coordinator did not report. Start or upgrade it, then check again.':
      'このコーディネーターは報告しませんでした。起動またはアップグレードしてから再確認してください。',
    'The server refused the address. Check it and try again.':
      'サーバーがアドレスを拒否しました。確認してもう一度試してください。',
    'The coordinator refused this server. Its log names the reason.':
      'コーディネーターがこのサーバーを拒否しました。理由はそのログにあります。',
    "The coordinator requires TLS. Remove the peer.insecure setting from this server's config, then restart it.":
      'コーディネーターは TLS を必要とします。このサーバーの設定から peer.insecure の設定を削除し、再起動してください。',
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      'このサーバーの peer.token はコーディネーターのものと異なります。両方に同じ peer.token を設定し、再起動してください。',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      'このサーバーにはコーディネーター用の peer token がありません。このサーバーの peer.token をコーディネーターと同じにして、再起動してください。',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      '実行中または到達可能なコーディネーターがありません。dagu start-all などで起動してから再確認してください。',
    'Switch to the local server to add a worker.':
      'ワーカーを追加するにはローカルサーバーに切り替えてください。',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} はコンテナ ID のようです。コンテナを実行しているマシンのアドレスを入力してください。',
    'Add worker': 'ワーカーを追加',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      '別のマシンでワーカーを実行します。ワーカーは招待でこの Dagu に参加します。',
    'Accept workers from other hosts': '他のホストからのワーカーを受け入れる',
    Ready: '準備完了',
    'Needs change': '変更が必要',
    'Only admins can add workers.': 'ワーカーを追加できるのは管理者だけです。',
    'The request failed.': 'リクエストに失敗しました。',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'コーディネーターが以前の Dagu リリースです。ワーカーを追加するにはすべてのコーディネーターをアップグレードしてください。',
    'The coordinator listens on {host}, so only this machine can connect.':
      'コーディネーターは {host} で待ち受けているため、このマシンからしか接続できません。',
    'Recreate the container with these options:':
      '次のオプションでコンテナを作り直してください:',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      '変数 {name} が coordinator.host を設定しています。Dagu を起動する場所で 0.0.0.0 にしてください:',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      '招待には組み込み CA が必要ですが、このコーディネーターは peer.cert_file と peer.key_file を使っています。招待を使うには両方を削除するか、各ワーカーを独自の証明書で接続してください。',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      '招待には組み込み CA が必要ですが、このコーディネーターは CA を作成または読み込めませんでした。起動時の警告に原因が示されています。',
    'Invites need the built-in CA, which this coordinator does not have.':
      '招待には組み込み CA が必要ですが、このコーディネーターにはありません。',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      '-p {port}:{port} のように、コンテナのポート {port} を公開してください。Dagu はコンテナが公開するポートを確認できません。',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      'クラスター外のワーカーには、タイプが LoadBalancer または NodePort のコーディネーター Service が必要です。チャートの coordinator.service.type で設定します。',
    'Address workers use to reach the coordinator':
      'ワーカーがコーディネーターに接続するアドレス',
    'Addresses of this machine:': 'このマシンのアドレス:',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'コンテナを実行しているマシンのアドレスを入力してください。Dagu はコンテナ内からそれを確認できません。',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'コーディネーター Service のアドレスを入力してください: ロードバランサー、または NodePort の場合はノード。',
    'Enter the address workers use to reach the coordinator.':
      'ワーカーがコーディネーターに接続するアドレスを入力してください。',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} は自身のマシンにしか届きません。他のホストのワーカーが届くアドレスを入力してください。',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} は待ち受け用のアドレスです。ワーカーが接続できるアドレスを入力してください。',
    'Enter a port from 1 to 65535.':
      '1 から 65535 のポートを入力してください。',
    'Shown after steps 1 and 2': 'ステップ 1 と 2 の後に表示',
  },
  de: {
    'Copy the change': 'Änderung kopieren',
    'The coordinator cannot do this yet. Try again in a moment.':
      'Der Koordinator kann dies noch nicht tun. Versuchen Sie es gleich noch einmal.',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'Der Koordinator kann dies in seinem jetzigen Zustand nicht tun. Sein Log nennt den Grund; beheben Sie ihn und versuchen Sie es erneut.',
    'Shown after step 1': 'Erscheint nach Schritt 1',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} ist eine Multicast- oder Broadcast-Adresse. Geben Sie die Adresse eines einzelnen Rechners ein.',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'Das Flag {name} setzt coordinator.host. Geben Sie ihm dort, wo Dagu startet, 0.0.0.0:',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      'Setzen Sie in {file} host innerhalb des coordinator-Blocks auf 0.0.0.0, und fügen Sie den Block hinzu, falls die Datei keinen hat.',
    'Run the chart in distributed mode, whose coordinator listens on 0.0.0.0, and remove any DAGU_COORDINATOR_HOST from extraEnv. Then upgrade the release:':
      'Betreiben Sie das Chart im verteilten Modus, dessen Koordinator auf 0.0.0.0 lauscht, und entfernen Sie jedes DAGU_COORDINATOR_HOST aus extraEnv. Aktualisieren Sie dann das Release:',
    'Enter host:port, such as 192.168.1.20:50055.':
      'Geben Sie host:port ein, etwa 192.168.1.20:50055.',
    'The address holds an invite. The invite goes only into the commands.':
      'Die Adresse enthält eine Einladung. Die Einladung gehört nur in die Befehle.',
    'Address and port': 'Adresse und Port',
    '{host} is not one host. Enter an IP address without a zone, or a DNS name.':
      '{host} ist kein einzelner Host. Geben Sie eine IP-Adresse ohne Zone oder einen DNS-Namen ein.',
    '{host} is a link-local address, which another network does not route. Enter another address of the machine.':
      '{host} ist eine Link-Local-Adresse, die ein anderes Netz nicht routet. Geben Sie eine andere Adresse des Rechners ein.',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again.':
      'Jede Änderung erfordert einen Neustart von Dagu. Dieser Dialog prüft erneut, sobald der Server wieder da ist; sonst nutzen Sie Erneut prüfen.',
    'This coordinator did not report. Start or upgrade it, then check again.':
      'Dieser Koordinator hat nicht berichtet. Starten oder aktualisieren Sie ihn und prüfen Sie dann erneut.',
    'The server refused the address. Check it and try again.':
      'Der Server hat die Adresse abgelehnt. Prüfen Sie sie und versuchen Sie es erneut.',
    'The coordinator refused this server. Its log names the reason.':
      'Der Koordinator hat diesen Server abgewiesen. Sein Log nennt den Grund.',
    "The coordinator requires TLS. Remove the peer.insecure setting from this server's config, then restart it.":
      'Der Koordinator verlangt TLS. Entfernen Sie die Einstellung peer.insecure aus der Konfiguration dieses Servers und starten Sie ihn neu.',
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      'Das peer.token dieses Servers ist nicht das des Koordinators. Setzen Sie auf beiden dasselbe peer.token und starten Sie sie neu.',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      'Dieser Server hat kein Peer-Token für den Koordinator. Setzen Sie peer.token auf diesem Server auf das des Koordinators und starten Sie ihn neu.',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      'Kein Koordinator läuft oder ist erreichbar. Starten Sie einen, etwa mit dagu start-all, und prüfen Sie dann erneut.',
    'Switch to the local server to add a worker.':
      'Wechseln Sie zum lokalen Server, um einen Worker hinzuzufügen.',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} sieht nach einer Container-ID aus. Geben Sie die Adresse des Rechners ein, auf dem der Container läuft.',
    'Add worker': 'Worker hinzufügen',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      'Einen Worker auf einem anderen Rechner ausführen. Er tritt diesem Dagu mit einer Einladung bei.',
    'Accept workers from other hosts': 'Worker von anderen Hosts annehmen',
    Ready: 'Bereit',
    'Needs change': 'Änderung nötig',
    'Only admins can add workers.':
      'Nur Administratoren können Worker hinzufügen.',
    'The request failed.': 'Die Anfrage ist fehlgeschlagen.',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'Der Koordinator stammt aus einer früheren Dagu-Version. Aktualisieren Sie jeden Koordinator, um Worker hinzuzufügen.',
    'The coordinator listens on {host}, so only this machine can connect.':
      'Der Koordinator lauscht auf {host}, daher kann sich nur dieser Rechner verbinden.',
    'Recreate the container with these options:':
      'Erstellen Sie den Container mit diesen Optionen neu:',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      'Die Variable {name} setzt coordinator.host. Setzen Sie sie dort, wo Dagu startet, auf 0.0.0.0:',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      'Einladungen brauchen die eingebaute CA, aber dieser Koordinator nutzt peer.cert_file und peer.key_file. Entfernen Sie beide, um Einladungen zu nutzen, oder verbinden Sie jeden Worker mit Ihren eigenen Zertifikaten.',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      'Einladungen brauchen die eingebaute CA, aber dieser Koordinator konnte sie nicht erstellen oder lesen. Seine Startwarnung nennt die Ursache.',
    'Invites need the built-in CA, which this coordinator does not have.':
      'Einladungen brauchen die eingebaute CA, die dieser Koordinator nicht hat.',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      'Veröffentlichen Sie Port {port} für den Container, etwa mit -p {port}:{port}. Dagu sieht nicht, welche Ports ein Container veröffentlicht.',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      'Worker außerhalb des Clusters brauchen einen Koordinator-Service vom Typ LoadBalancer oder NodePort, wie ihn coordinator.service.type des Charts setzt.',
    'Address workers use to reach the coordinator':
      'Adresse, über die Worker den Koordinator erreichen',
    'Addresses of this machine:': 'Adressen dieses Rechners:',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'Geben Sie die Adresse des Rechners ein, auf dem der Container läuft. Dagu sieht sie nicht aus dem Container heraus.',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'Geben Sie die Adresse des Koordinator-Service ein: seinen Load Balancer oder bei NodePort einen Knoten.',
    'Enter the address workers use to reach the coordinator.':
      'Geben Sie die Adresse ein, über die Worker den Koordinator erreichen.',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} erreicht nur den eigenen Rechner. Geben Sie eine Adresse ein, die Worker auf anderen Hosts erreichen.',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} ist eine Lauschadresse. Geben Sie eine Adresse ein, die Worker anwählen können.',
    'Enter a port from 1 to 65535.':
      'Geben Sie einen Port von 1 bis 65535 ein.',
    'Shown after steps 1 and 2': 'Erscheint nach Schritt 1 und 2',
  },
  fr: {
    'Copy the change': 'Copier la modification',
    'The coordinator cannot do this yet. Try again in a moment.':
      'Le coordinateur ne peut pas encore le faire. Réessayez dans un instant.',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'Le coordinateur ne peut pas le faire dans son état actuel. Son journal en donne la raison ; corrigez-la, puis réessayez.',
    'Shown after step 1': 'Affiché après l’étape 1',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} est une adresse multicast ou de diffusion. Saisissez l’adresse d’une seule machine.',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'L’option {name} définit coordinator.host. Donnez-lui 0.0.0.0 là où Dagu démarre :',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      'Dans {file}, définissez host à 0.0.0.0 dans le bloc coordinator, en ajoutant le bloc si le fichier n’en a pas.',
    'Run the chart in distributed mode, whose coordinator listens on 0.0.0.0, and remove any DAGU_COORDINATOR_HOST from extraEnv. Then upgrade the release:':
      'Exécutez le chart en mode distribué, dont le coordinateur écoute sur 0.0.0.0, et retirez tout DAGU_COORDINATOR_HOST d’extraEnv. Puis mettez à jour la release :',
    'Enter host:port, such as 192.168.1.20:50055.':
      'Saisissez host:port, par exemple 192.168.1.20:50055.',
    'The address holds an invite. The invite goes only into the commands.':
      'L’adresse contient une invitation. L’invitation va seulement dans les commandes.',
    'Address and port': 'Adresse et port',
    '{host} is not one host. Enter an IP address without a zone, or a DNS name.':
      '{host} n’est pas un hôte unique. Saisissez une adresse IP sans zone, ou un nom DNS.',
    '{host} is a link-local address, which another network does not route. Enter another address of the machine.':
      '{host} est une adresse lien-local qu’un autre réseau ne route pas. Saisissez une autre adresse de la machine.',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again.':
      'Chaque modification nécessite un redémarrage de Dagu. Cette boîte de dialogue vérifie à nouveau quand le serveur revient ; sinon, utilisez Vérifier à nouveau.',
    'This coordinator did not report. Start or upgrade it, then check again.':
      'Ce coordinateur n’a pas répondu. Démarrez-le ou mettez-le à jour, puis vérifiez à nouveau.',
    'The server refused the address. Check it and try again.':
      'Le serveur a refusé l’adresse. Vérifiez-la et réessayez.',
    'The coordinator refused this server. Its log names the reason.':
      'Le coordinateur a refusé ce serveur. Son journal en donne la raison.',
    "The coordinator requires TLS. Remove the peer.insecure setting from this server's config, then restart it.":
      'Le coordinateur exige TLS. Retirez le réglage peer.insecure de la configuration de ce serveur, puis redémarrez-le.',
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      'Le peer.token de ce serveur n’est pas celui du coordinateur. Définissez le même peer.token sur les deux, puis redémarrez-les.',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      'Ce serveur n’a pas de jeton pair pour le coordinateur. Donnez à peer.token sur ce serveur la valeur de celui du coordinateur, puis redémarrez-le.',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      'Aucun coordinateur ne fonctionne ou n’est joignable. Démarrez-en un, par exemple avec dagu start-all, puis vérifiez à nouveau.',
    'Switch to the local server to add a worker.':
      'Passez au serveur local pour ajouter un worker.',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} ressemble à un ID de conteneur. Saisissez l’adresse de la machine qui exécute le conteneur.',
    'Add worker': 'Ajouter un worker',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      'Exécutez un worker sur une autre machine. Il rejoint ce Dagu avec une invitation.',
    'Accept workers from other hosts': 'Accepter les workers d’autres hôtes',
    Ready: 'Prêt',
    'Needs change': 'Modification requise',
    'Only admins can add workers.':
      'Seuls les administrateurs peuvent ajouter des workers.',
    'The request failed.': 'La requête a échoué.',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'Le coordinateur provient d’une version antérieure de Dagu. Mettez à jour chaque coordinateur pour ajouter des workers.',
    'The coordinator listens on {host}, so only this machine can connect.':
      'Le coordinateur écoute sur {host}, donc seule cette machine peut se connecter.',
    'Recreate the container with these options:':
      'Recréez le conteneur avec ces options :',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      'La variable {name} définit coordinator.host. Donnez-lui la valeur 0.0.0.0 là où Dagu démarre :',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      'Les invitations nécessitent la CA intégrée, mais ce coordinateur utilise peer.cert_file et peer.key_file. Supprimez les deux pour utiliser les invitations, ou connectez chaque worker avec vos propres certificats.',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      'Les invitations nécessitent la CA intégrée, mais ce coordinateur n’a pas pu la créer ni la lire. Son avertissement au démarrage en donne la cause.',
    'Invites need the built-in CA, which this coordinator does not have.':
      'Les invitations nécessitent la CA intégrée, que ce coordinateur n’a pas.',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      'Publiez le port {port} du conteneur, par exemple avec -p {port}:{port}. Dagu ne voit pas quels ports un conteneur publie.',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      'Les workers hors du cluster ont besoin d’un Service de coordinateur de type LoadBalancer ou NodePort, comme le définit coordinator.service.type du chart.',
    'Address workers use to reach the coordinator':
      'Adresse que les workers utilisent pour joindre le coordinateur',
    'Addresses of this machine:': 'Adresses de cette machine :',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'Saisissez l’adresse de la machine qui exécute le conteneur. Dagu ne la voit pas depuis le conteneur.',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'Saisissez l’adresse du Service du coordinateur : son load balancer, ou un nœud pour NodePort.',
    'Enter the address workers use to reach the coordinator.':
      'Saisissez l’adresse que les workers utilisent pour joindre le coordinateur.',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} ne joint que sa propre machine. Saisissez une adresse que les workers d’autres hôtes peuvent joindre.',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} est une adresse d’écoute. Saisissez une adresse que les workers peuvent joindre.',
    'Enter a port from 1 to 65535.': 'Saisissez un port de 1 à 65535.',
    'Shown after steps 1 and 2': 'Affiché après les étapes 1 et 2',
  },
});
