// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

/** The static messages of the Add worker dialog. */
export const addWorkerMessages = defineMessages({
  en: {
    'Switch to the local server to add a worker.':
      'Switch to the local server to add a worker.',
    "Give this server the coordinator's peer.token, then restart it.":
      "Give this server the coordinator's peer.token, then restart it.",
    'The coordinator refused this server.':
      'The coordinator refused this server.',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} looks like a container ID. Enter the address of the machine that runs the container.',
    'IP address or DNS name': 'IP address or DNS name',
    'Add worker': 'Add worker',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      'Run a worker on another machine. It joins this Dagu with an invite.',
    'Accept workers from other hosts': 'Accept workers from other hosts',
    Ready: 'Ready',
    'Needs change': 'Needs change',
    'Only admins can add workers.': 'Only admins can add workers.',
    'The server cannot reach a coordinator.':
      'The server cannot reach a coordinator.',
    'The request failed.': 'The request failed.',
    'The server did not answer.': 'The server did not answer.',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.',
    'No coordinator is running. Start one, such as with dagu start-all, then check again.':
      'No coordinator is running. Start one, such as with dagu start-all, then check again.',
    'This coordinator did not report: {error}':
      'This coordinator did not report: {error}',
    'The coordinator listens on {host}, so only this machine can connect.':
      'The coordinator listens on {host}, so only this machine can connect.',
    "Set coordinator.host to 0.0.0.0 through the chart's values, then upgrade the release:":
      "Set coordinator.host to 0.0.0.0 through the chart's values, then upgrade the release:",
    'Recreate the container with these options:':
      'Recreate the container with these options:',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:',
    'Add these lines to {file}:': 'Add these lines to {file}:',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.',
    'Invites need the built-in CA, which this coordinator does not have.':
      'Invites need the built-in CA, which this coordinator does not have.',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back.':
      'Each change needs a restart of Dagu. This dialog checks again when the server is back.',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.",
    'Copy the values': 'Copy the values',
    'Copy the options': 'Copy the options',
    'Copy the setting': 'Copy the setting',
    'Copy the lines': 'Copy the lines',
    'Address workers use to reach the coordinator':
      'Address workers use to reach the coordinator',
    Address: 'Address',
    'Addresses of this machine:': 'Addresses of this machine:',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.',
    'Enter the address workers use to reach the coordinator.':
      'Enter the address workers use to reach the coordinator.',
    'Enter a host name or IP address only, without a scheme or path.':
      'Enter a host name or IP address only, without a scheme or path.',
    'Enter the port in the Port field.': 'Enter the port in the Port field.',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} is a listen address. Enter an address that workers can dial.',
    'Enter a port from 1 to 65535.': 'Enter a port from 1 to 65535.',
    'Shown after steps 1 and 2': 'Shown after steps 1 and 2',
  },
  'zh-CN': {
    'Switch to the local server to add a worker.':
      '切换到本地服务器以添加 Worker。',
    "Give this server the coordinator's peer.token, then restart it.":
      '为此服务器设置协调器的 peer.token，然后重启它。',
    'The coordinator refused this server.': '协调器拒绝了此服务器。',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} 看起来是容器 ID。请输入运行该容器的机器的地址。',
    'IP address or DNS name': 'IP 地址或 DNS 名称',
    'Add worker': '添加 Worker',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      '在另一台机器上运行 Worker。它通过邀请加入此 Dagu。',
    'Accept workers from other hosts': '接受来自其他主机的 Worker',
    Ready: '就绪',
    'Needs change': '需要修改',
    'Only admins can add workers.': '只有管理员可以添加 Worker。',
    'The server cannot reach a coordinator.': '服务器无法连接到协调器。',
    'The request failed.': '请求失败。',
    'The server did not answer.': '服务器没有响应。',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      '协调器来自较早的 Dagu 版本。升级所有协调器后才能添加 Worker。',
    'No coordinator is running. Start one, such as with dagu start-all, then check again.':
      '没有正在运行的协调器。请启动一个（例如使用 dagu start-all），然后重新检查。',
    'This coordinator did not report: {error}': '此协调器未报告：{error}',
    'The coordinator listens on {host}, so only this machine can connect.':
      '协调器监听 {host}，因此只有本机可以连接。',
    "Set coordinator.host to 0.0.0.0 through the chart's values, then upgrade the release:":
      '通过 Chart 的 values 将 coordinator.host 设为 0.0.0.0，然后升级 Release：',
    'Recreate the container with these options:': '使用以下选项重新创建容器：',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      '变量 {name} 设置了 coordinator.host。在启动 Dagu 的地方将其设为 0.0.0.0：',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      '参数 {name} 设置了 coordinator.host。在启动 Dagu 的地方将其设为 0.0.0.0：',
    'Add these lines to {file}:': '将以下内容添加到 {file}：',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      '邀请需要内置 CA，但此协调器使用 peer.cert_file 和 peer.key_file。删除这两项即可使用邀请，或使用你自己的证书连接每个 Worker。',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      '邀请需要内置 CA，但此协调器无法创建或读取它。其启动警告说明了原因。',
    'Invites need the built-in CA, which this coordinator does not have.':
      '邀请需要内置 CA，而此协调器没有。',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back.':
      '每项修改都需要重启 Dagu。服务器恢复后，此对话框会重新检查。',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      '为容器发布端口 {port}，例如使用 -p {port}:{port}。Dagu 无法看到容器发布了哪些端口。',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      '集群外的 Worker 需要协调器 Service 的类型为 LoadBalancer 或 NodePort，可通过 Chart 的 coordinator.service.type 设置。',
    'Copy the values': '复制 values',
    'Copy the options': '复制选项',
    'Copy the setting': '复制设置',
    'Copy the lines': '复制内容',
    'Address workers use to reach the coordinator':
      'Worker 用于连接协调器的地址',
    Address: '地址',
    'Addresses of this machine:': '本机地址：',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      '输入运行该容器的机器的地址。Dagu 在容器内无法看到它。',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      '输入协调器 Service 的地址：其负载均衡器，或 NodePort 所用的节点。',
    'Enter the address workers use to reach the coordinator.':
      '输入 Worker 用于连接协调器的地址。',
    'Enter a host name or IP address only, without a scheme or path.':
      '只输入主机名或 IP 地址，不要包含协议或路径。',
    'Enter the port in the Port field.': '请在“端口”字段中输入端口。',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} 只能访问其所在的机器。请输入其他主机上的 Worker 可以访问的地址。',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} 是监听地址。请输入 Worker 可以连接的地址。',
    'Enter a port from 1 to 65535.': '请输入 1 到 65535 之间的端口。',
    'Shown after steps 1 and 2': '完成步骤 1 和 2 后显示',
  },
  ja: {
    'Switch to the local server to add a worker.':
      'ワーカーを追加するにはローカルサーバーに切り替えてください。',
    "Give this server the coordinator's peer.token, then restart it.":
      'このサーバーにコーディネーターの peer.token を設定し、再起動してください。',
    'The coordinator refused this server.':
      'コーディネーターがこのサーバーを拒否しました。',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} はコンテナ ID のようです。コンテナを実行しているマシンのアドレスを入力してください。',
    'IP address or DNS name': 'IP アドレスまたは DNS 名',
    'Add worker': 'ワーカーを追加',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      '別のマシンでワーカーを実行します。ワーカーは招待でこの Dagu に参加します。',
    'Accept workers from other hosts': '他のホストからのワーカーを受け入れる',
    Ready: '準備完了',
    'Needs change': '変更が必要',
    'Only admins can add workers.': 'ワーカーを追加できるのは管理者だけです。',
    'The server cannot reach a coordinator.':
      'サーバーがコーディネーターに接続できません。',
    'The request failed.': 'リクエストに失敗しました。',
    'The server did not answer.': 'サーバーが応答しませんでした。',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'コーディネーターが以前の Dagu リリースです。ワーカーを追加するにはすべてのコーディネーターをアップグレードしてください。',
    'No coordinator is running. Start one, such as with dagu start-all, then check again.':
      '実行中のコーディネーターがありません。dagu start-all などで起動してから再確認してください。',
    'This coordinator did not report: {error}':
      'このコーディネーターは報告しませんでした: {error}',
    'The coordinator listens on {host}, so only this machine can connect.':
      'コーディネーターは {host} で待ち受けているため、このマシンからしか接続できません。',
    "Set coordinator.host to 0.0.0.0 through the chart's values, then upgrade the release:":
      'チャートの values で coordinator.host を 0.0.0.0 にし、リリースをアップグレードしてください:',
    'Recreate the container with these options:':
      '次のオプションでコンテナを作り直してください:',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      '変数 {name} が coordinator.host を設定しています。Dagu を起動する場所で 0.0.0.0 にしてください:',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'フラグ {name} が coordinator.host を設定しています。Dagu を起動する場所で 0.0.0.0 を指定してください:',
    'Add these lines to {file}:': '{file} に次の行を追加してください:',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      '招待には組み込み CA が必要ですが、このコーディネーターは peer.cert_file と peer.key_file を使っています。招待を使うには両方を削除するか、各ワーカーを独自の証明書で接続してください。',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      '招待には組み込み CA が必要ですが、このコーディネーターは CA を作成または読み込めませんでした。起動時の警告に原因が示されています。',
    'Invites need the built-in CA, which this coordinator does not have.':
      '招待には組み込み CA が必要ですが、このコーディネーターにはありません。',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back.':
      'どの変更も Dagu の再起動が必要です。サーバーが戻ると、このダイアログが再確認します。',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      '-p {port}:{port} のように、コンテナのポート {port} を公開してください。Dagu はコンテナが公開するポートを確認できません。',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      'クラスター外のワーカーには、タイプが LoadBalancer または NodePort のコーディネーター Service が必要です。チャートの coordinator.service.type で設定します。',
    'Copy the values': 'values をコピー',
    'Copy the options': 'オプションをコピー',
    'Copy the setting': '設定をコピー',
    'Copy the lines': '行をコピー',
    'Address workers use to reach the coordinator':
      'ワーカーがコーディネーターに接続するアドレス',
    Address: 'アドレス',
    'Addresses of this machine:': 'このマシンのアドレス:',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'コンテナを実行しているマシンのアドレスを入力してください。Dagu はコンテナ内からそれを確認できません。',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'コーディネーター Service のアドレスを入力してください: ロードバランサー、または NodePort の場合はノード。',
    'Enter the address workers use to reach the coordinator.':
      'ワーカーがコーディネーターに接続するアドレスを入力してください。',
    'Enter a host name or IP address only, without a scheme or path.':
      'スキームやパスを付けず、ホスト名か IP アドレスだけを入力してください。',
    'Enter the port in the Port field.': 'ポートはポート欄に入力してください。',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} は自身のマシンにしか届きません。他のホストのワーカーが届くアドレスを入力してください。',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} は待ち受け用のアドレスです。ワーカーが接続できるアドレスを入力してください。',
    'Enter a port from 1 to 65535.':
      '1 から 65535 のポートを入力してください。',
    'Shown after steps 1 and 2': 'ステップ 1 と 2 の後に表示',
  },
  de: {
    'Switch to the local server to add a worker.':
      'Wechseln Sie zum lokalen Server, um einen Worker hinzuzufügen.',
    "Give this server the coordinator's peer.token, then restart it.":
      'Geben Sie diesem Server das peer.token des Koordinators und starten Sie ihn neu.',
    'The coordinator refused this server.':
      'Der Koordinator hat diesen Server abgewiesen.',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} sieht nach einer Container-ID aus. Geben Sie die Adresse des Rechners ein, auf dem der Container läuft.',
    'IP address or DNS name': 'IP-Adresse oder DNS-Name',
    'Add worker': 'Worker hinzufügen',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      'Einen Worker auf einem anderen Rechner ausführen. Er tritt diesem Dagu mit einer Einladung bei.',
    'Accept workers from other hosts': 'Worker von anderen Hosts annehmen',
    Ready: 'Bereit',
    'Needs change': 'Änderung nötig',
    'Only admins can add workers.':
      'Nur Administratoren können Worker hinzufügen.',
    'The server cannot reach a coordinator.':
      'Der Server erreicht keinen Koordinator.',
    'The request failed.': 'Die Anfrage ist fehlgeschlagen.',
    'The server did not answer.': 'Der Server hat nicht geantwortet.',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'Der Koordinator stammt aus einer früheren Dagu-Version. Aktualisieren Sie jeden Koordinator, um Worker hinzuzufügen.',
    'No coordinator is running. Start one, such as with dagu start-all, then check again.':
      'Kein Koordinator läuft. Starten Sie einen, etwa mit dagu start-all, und prüfen Sie dann erneut.',
    'This coordinator did not report: {error}':
      'Dieser Koordinator hat nicht berichtet: {error}',
    'The coordinator listens on {host}, so only this machine can connect.':
      'Der Koordinator lauscht auf {host}, daher kann sich nur dieser Rechner verbinden.',
    "Set coordinator.host to 0.0.0.0 through the chart's values, then upgrade the release:":
      'Setzen Sie coordinator.host über die Values des Charts auf 0.0.0.0 und aktualisieren Sie dann das Release:',
    'Recreate the container with these options:':
      'Erstellen Sie den Container mit diesen Optionen neu:',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      'Die Variable {name} setzt coordinator.host. Setzen Sie sie dort, wo Dagu startet, auf 0.0.0.0:',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'Das Flag {name} setzt coordinator.host. Geben Sie ihm dort, wo Dagu startet, 0.0.0.0:',
    'Add these lines to {file}:': 'Fügen Sie diese Zeilen zu {file} hinzu:',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      'Einladungen brauchen die eingebaute CA, aber dieser Koordinator nutzt peer.cert_file und peer.key_file. Entfernen Sie beide, um Einladungen zu nutzen, oder verbinden Sie jeden Worker mit Ihren eigenen Zertifikaten.',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      'Einladungen brauchen die eingebaute CA, aber dieser Koordinator konnte sie nicht erstellen oder lesen. Seine Startwarnung nennt die Ursache.',
    'Invites need the built-in CA, which this coordinator does not have.':
      'Einladungen brauchen die eingebaute CA, die dieser Koordinator nicht hat.',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back.':
      'Jede Änderung erfordert einen Neustart von Dagu. Dieser Dialog prüft erneut, sobald der Server wieder da ist.',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      'Veröffentlichen Sie Port {port} für den Container, etwa mit -p {port}:{port}. Dagu sieht nicht, welche Ports ein Container veröffentlicht.',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      'Worker außerhalb des Clusters brauchen einen Koordinator-Service vom Typ LoadBalancer oder NodePort, wie ihn coordinator.service.type des Charts setzt.',
    'Copy the values': 'Values kopieren',
    'Copy the options': 'Optionen kopieren',
    'Copy the setting': 'Einstellung kopieren',
    'Copy the lines': 'Zeilen kopieren',
    'Address workers use to reach the coordinator':
      'Adresse, über die Worker den Koordinator erreichen',
    Address: 'Adresse',
    'Addresses of this machine:': 'Adressen dieses Rechners:',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'Geben Sie die Adresse des Rechners ein, auf dem der Container läuft. Dagu sieht sie nicht aus dem Container heraus.',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'Geben Sie die Adresse des Koordinator-Service ein: seinen Load Balancer oder bei NodePort einen Knoten.',
    'Enter the address workers use to reach the coordinator.':
      'Geben Sie die Adresse ein, über die Worker den Koordinator erreichen.',
    'Enter a host name or IP address only, without a scheme or path.':
      'Geben Sie nur einen Hostnamen oder eine IP-Adresse ein, ohne Schema oder Pfad.',
    'Enter the port in the Port field.': 'Geben Sie den Port im Feld Port ein.',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} erreicht nur den eigenen Rechner. Geben Sie eine Adresse ein, die Worker auf anderen Hosts erreichen.',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} ist eine Lauschadresse. Geben Sie eine Adresse ein, die Worker anwählen können.',
    'Enter a port from 1 to 65535.':
      'Geben Sie einen Port von 1 bis 65535 ein.',
    'Shown after steps 1 and 2': 'Erscheint nach Schritt 1 und 2',
  },
  fr: {
    'Switch to the local server to add a worker.':
      'Passez au serveur local pour ajouter un worker.',
    "Give this server the coordinator's peer.token, then restart it.":
      'Donnez à ce serveur le peer.token du coordinateur, puis redémarrez-le.',
    'The coordinator refused this server.':
      'Le coordinateur a refusé ce serveur.',
    '{host} looks like a container ID. Enter the address of the machine that runs the container.':
      '{host} ressemble à un ID de conteneur. Saisissez l’adresse de la machine qui exécute le conteneur.',
    'IP address or DNS name': 'Adresse IP ou nom DNS',
    'Add worker': 'Ajouter un worker',
    'Run a worker on another machine. It joins this Dagu with an invite.':
      'Exécutez un worker sur une autre machine. Il rejoint ce Dagu avec une invitation.',
    'Accept workers from other hosts': 'Accepter les workers d’autres hôtes',
    Ready: 'Prêt',
    'Needs change': 'Modification requise',
    'Only admins can add workers.':
      'Seuls les administrateurs peuvent ajouter des workers.',
    'The server cannot reach a coordinator.':
      'Le serveur ne peut joindre aucun coordinateur.',
    'The request failed.': 'La requête a échoué.',
    'The server did not answer.': 'Le serveur n’a pas répondu.',
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.':
      'Le coordinateur provient d’une version antérieure de Dagu. Mettez à jour chaque coordinateur pour ajouter des workers.',
    'No coordinator is running. Start one, such as with dagu start-all, then check again.':
      'Aucun coordinateur ne fonctionne. Démarrez-en un, par exemple avec dagu start-all, puis vérifiez à nouveau.',
    'This coordinator did not report: {error}':
      'Ce coordinateur n’a pas répondu : {error}',
    'The coordinator listens on {host}, so only this machine can connect.':
      'Le coordinateur écoute sur {host}, donc seule cette machine peut se connecter.',
    "Set coordinator.host to 0.0.0.0 through the chart's values, then upgrade the release:":
      'Définissez coordinator.host sur 0.0.0.0 dans les values du chart, puis mettez à jour la release :',
    'Recreate the container with these options:':
      'Recréez le conteneur avec ces options :',
    'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:':
      'La variable {name} définit coordinator.host. Donnez-lui la valeur 0.0.0.0 là où Dagu démarre :',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'L’option {name} définit coordinator.host. Donnez-lui 0.0.0.0 là où Dagu démarre :',
    'Add these lines to {file}:': 'Ajoutez ces lignes à {file} :',
    'Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates.':
      'Les invitations nécessitent la CA intégrée, mais ce coordinateur utilise peer.cert_file et peer.key_file. Supprimez les deux pour utiliser les invitations, ou connectez chaque worker avec vos propres certificats.',
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.':
      'Les invitations nécessitent la CA intégrée, mais ce coordinateur n’a pas pu la créer ni la lire. Son avertissement au démarrage en donne la cause.',
    'Invites need the built-in CA, which this coordinator does not have.':
      'Les invitations nécessitent la CA intégrée, que ce coordinateur n’a pas.',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back.':
      'Chaque modification nécessite un redémarrage de Dagu. Cette boîte de dialogue vérifie à nouveau quand le serveur revient.',
    'Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes.':
      'Publiez le port {port} du conteneur, par exemple avec -p {port}:{port}. Dagu ne voit pas quels ports un conteneur publie.',
    "Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets.":
      'Les workers hors du cluster ont besoin d’un Service de coordinateur de type LoadBalancer ou NodePort, comme le définit coordinator.service.type du chart.',
    'Copy the values': 'Copier les values',
    'Copy the options': 'Copier les options',
    'Copy the setting': 'Copier le réglage',
    'Copy the lines': 'Copier les lignes',
    'Address workers use to reach the coordinator':
      'Adresse que les workers utilisent pour joindre le coordinateur',
    Address: 'Adresse',
    'Addresses of this machine:': 'Adresses de cette machine :',
    'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.':
      'Saisissez l’adresse de la machine qui exécute le conteneur. Dagu ne la voit pas depuis le conteneur.',
    'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.':
      'Saisissez l’adresse du Service du coordinateur : son load balancer, ou un nœud pour NodePort.',
    'Enter the address workers use to reach the coordinator.':
      'Saisissez l’adresse que les workers utilisent pour joindre le coordinateur.',
    'Enter a host name or IP address only, without a scheme or path.':
      'Saisissez seulement un nom d’hôte ou une adresse IP, sans schéma ni chemin.',
    'Enter the port in the Port field.':
      'Saisissez le port dans le champ Port.',
    '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.':
      '{host} ne joint que sa propre machine. Saisissez une adresse que les workers d’autres hôtes peuvent joindre.',
    '{host} is a listen address. Enter an address that workers can dial.':
      '{host} est une adresse d’écoute. Saisissez une adresse que les workers peuvent joindre.',
    'Enter a port from 1 to 65535.': 'Saisissez un port de 1 à 65535.',
    'Shown after steps 1 and 2': 'Affiché après les étapes 1 et 2',
  },
});
