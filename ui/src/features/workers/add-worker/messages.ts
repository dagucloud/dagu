// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

/** The static messages of the Add worker dialog. */
export const addWorkerMessages = defineMessages({
  en: {
    'Use this address': 'Use this address',
    'The coordinator now reports {address}.':
      'The coordinator now reports {address}.',
    "The coordinator requires TLS. On this server, clear any peer.insecure: true or peer.skip_tls_verify: true, whether a flag, a variable, or the config file sets it. If this server has no usable CA certificate for the coordinator, switch it to built-in TLS: clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate. Then restart it. If these settings are in order, check that the network path to the coordinator passes TLS.":
      "The coordinator requires TLS. On this server, clear any peer.insecure: true or peer.skip_tls_verify: true, whether a flag, a variable, or the config file sets it. If this server has no usable CA certificate for the coordinator, switch it to built-in TLS: clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate. Then restart it. If these settings are in order, check that the network path to the coordinator passes TLS.",
    'Invite created. Its command is below.':
      'Invite created. Its command is below.',
    'Creating invite…': 'Creating invite…',
    'Check that worker pods can reach this address. Workers in another namespace need the namespace-qualified Service address, such as dagu-coordinator.<namespace>.svc:50055.':
      'Check that worker pods can reach this address. Workers in another namespace need the namespace-qualified Service address, such as dagu-coordinator.<namespace>.svc:50055.',
    "Run this with the same kubectl context and namespace you applied the manifest with. The worker's log names the cause:":
      "Run this with the same kubectl context and namespace you applied the manifest with. The worker's log names the cause:",
    'Run this on the worker host, which needs Docker. For another worker on the same host, change the volume dagu-worker to one of its own and use a new invite, or a reusable one.':
      'Run this on the worker host, which needs Docker. For another worker on the same host, change the volume dagu-worker to one of its own and use a new invite, or a reusable one.',
    "Enter the coordinator Service's address and port. Workers in another namespace need its namespace-qualified name, such as dagu-coordinator.<namespace>.svc:50055.":
      "Enter the coordinator Service's address and port. Workers in another namespace need its namespace-qualified name, such as dagu-coordinator.<namespace>.svc:50055.",
    'Diagnostic command copied': 'Diagnostic command copied',
    'Command copied': 'Command copied',
    'Copy the diagnostic command': 'Copy the diagnostic command',
    "Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload, then restart it. A coordinator in its own pod shares its storage with the scheduler and the web UI, so all three need a volume they can each write; worker pods keep their own. With the Helm chart, use deploymentMode: distributed, which sets the variable. Move the data to a ReadWriteMany claim, then set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany before you upgrade.":
      "Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload, then restart it. A coordinator in its own pod shares its storage with the scheduler and the web UI, so all three need a volume they can each write; worker pods keep their own. With the Helm chart, use deploymentMode: distributed, which sets the variable. Move the data to a ReadWriteMany claim, then set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany before you upgrade.",
    'Outside the cluster': 'Outside the cluster',
    'Inside this cluster': 'Inside this cluster',
    'Where the workers run': 'Where the workers run',
    'Mutual TLS': 'Mutual TLS',
    'Server TLS': 'Server TLS',
    'This coordinator uses its own certificates, peer.cert_file and peer.key_file, so it has no built-in CA for invites. Add the worker with your own certificates and no invite, as server TLS describes, and mutual TLS when the coordinator sets peer.client_ca_file. Or move every Dagu process to built-in TLS together.':
      'This coordinator uses its own certificates, peer.cert_file and peer.key_file, so it has no built-in CA for invites. Add the worker with your own certificates and no invite, as server TLS describes, and mutual TLS when the coordinator sets peer.client_ca_file. Or move every Dagu process to built-in TLS together.',
    'Move to built-in TLS': 'Move to built-in TLS',
    "Enter the address of the coordinator Service that workers reach: its load balancer, or for NodePort, a node's address and the assigned node port. The Service's own name works only inside the cluster.":
      "Enter the address of the coordinator Service that workers reach: its load balancer, or for NodePort, a node's address and the assigned node port. The Service's own name works only inside the cluster.",
    '{count} workers joined with this invite.':
      '{count} workers joined with this invite.',
    '1 worker joined with this invite.': '1 worker joined with this invite.',
    'Looking for the worker...': 'Looking for the worker...',
    'Run this on the worker host. From a fresh container, it shows whether the host reaches the address and what the coordinator answers:':
      'Run this on the worker host. From a fresh container, it shows whether the host reaches the address and what the coordinator answers:',
    'Run this on the worker host. From an empty data directory, it dials even after the worker joined, and shows whether the host reaches the address and what the coordinator answers:':
      'Run this on the worker host. From an empty data directory, it dials even after the worker joined, and shows whether the host reaches the address and what the coordinator answers:',
    'Switch to the local server to add a worker.':
      'Switch to the local server to add a worker.',
    'Copy the change': 'Copy the change',
    'Check again': 'Check again',
    'The coordinator cannot do this yet. Try again in a moment.':
      'The coordinator cannot do this yet. Try again in a moment.',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.',
    'Shown after step 1': 'Shown after step 1',
    'The workers list could not be read, so the dialog cannot tell whether the worker joined.':
      'The workers list could not be read, so the dialog cannot tell whether the worker joined.',
    'This invite was made for {address}. To use {next}, create another invite.':
      'This invite was made for {address}. To use {next}, create another invite.',
    'Label {position} sets a key an earlier label sets; keys compare without case.':
      'Label {position} sets a key an earlier label sets; keys compare without case.',
    'Label {position} is empty. Write one key or key=value.':
      'Label {position} is empty. Write one key or key=value.',
    'Label {position} holds an invite. The invite goes only into the commands.':
      'Label {position} holds an invite. The invite goes only into the commands.',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} is a multicast or broadcast address. Enter the address of one machine.',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.',
    'Enter host:port, such as 192.168.1.20:50055.':
      'Enter host:port, such as 192.168.1.20:50055.',
    'The address holds an invite. The invite goes only into the commands.':
      'The address holds an invite. The invite goes only into the commands.',
    'Address and port': 'Address and port',
    'Look for the worker again': 'Look for the worker again',
    'Label {position} sets {key}, which a worker sets from its own host.':
      'Label {position} sets {key}, which a worker sets from its own host.',
    'Label {position} breaks the worker label rule: a key takes letters, digits, -, _, and ., at most 63, and a value also /, at most 255, each starting with a letter or digit.':
      'Label {position} breaks the worker label rule: a key takes letters, digits, -, _, and ., at most 63, and a value also /, at most 255, each starting with a letter or digit.',
    'Save this as a file and apply it with kubectl apply -f to the cluster that runs the workers.':
      'Save this as a file and apply it with kubectl apply -f to the cluster that runs the workers.',
    'This server is a development build ({version}), so no published image matches it. Run a released Dagu to add a worker with Docker or Kubernetes, or use the Linux tab.':
      'This server is a development build ({version}), so no published image matches it. Run a released Dagu to add a worker with Docker or Kubernetes, or use the Linux tab.',
    'Installation guide': 'Installation guide',
    'Install Dagu on the worker host, then run this there.':
      'Install Dagu on the worker host, then run this there.',
    'The coordinator cannot create invites.':
      'The coordinator cannot create invites.',
    'Copy the command': 'Copy the command',
    'How the worker runs': 'How the worker runs',
    'Labels (optional)': 'Labels (optional)',
    Invite: 'Invite',
    'Single use: one worker that keeps its data directory. Expires after 24 hours unless used.':
      'Single use: one worker that keeps its data directory. Expires after 24 hours unless used.',
    'Reusable: any number of hosts. Lasts until deleted.':
      'Reusable: any number of hosts. Lasts until deleted.',
    'Reusable: any number of containers, each with its own volume. Lasts until deleted.':
      'Reusable: any number of containers, each with its own volume. Lasts until deleted.',
    'Reusable invite: pods are replaced and scaled, and each new pod joins with it. It lasts until deleted.':
      'Reusable invite: pods are replaced and scaled, and each new pod joins with it. It lasts until deleted.',
    'Create invite': 'Create invite',
    'Single-use invite, expires {time}.': 'Single-use invite, expires {time}.',
    'Reusable invite, expires {time}.': 'Reusable invite, expires {time}.',
    'Reusable invite, until deleted.': 'Reusable invite, until deleted.',
    'The invite is shown only here. Closing the dialog keeps it.':
      'The invite is shown only here. Closing the dialog keeps it.',
    'Waiting for the worker to join...': 'Waiting for the worker to join...',
    'No worker has joined yet. The usual causes:':
      'No worker has joined yet. The usual causes:',
    'A firewall or security group closes port {port}.':
      'A firewall or security group closes port {port}.',
    'The container that runs Dagu does not publish port {port}.':
      'The container that runs Dagu does not publish port {port}.',
    'The worker cannot reach {address}.': 'The worker cannot reach {address}.',
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
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.",
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.",
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.',
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
    'Use this address': '使用此地址',
    'The coordinator now reports {address}.':
      '协调器现在报告的地址为 {address}。',
    "The coordinator requires TLS. On this server, clear any peer.insecure: true or peer.skip_tls_verify: true, whether a flag, a variable, or the config file sets it. If this server has no usable CA certificate for the coordinator, switch it to built-in TLS: clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate. Then restart it. If these settings are in order, check that the network path to the coordinator passes TLS.":
      '协调器要求 TLS。请在此服务器上清除所有 peer.insecure: true 或 peer.skip_tls_verify: true，无论它由参数、变量还是配置文件设置。如果此服务器没有协调器可用的 CA 证书，请将其切换到内置 TLS：清除 peer.cert_file、peer.key_file 和 peer.client_ca_file，然后将 peer.ca_cert_file 设为协调器的 CA 证书。然后重启它。如果这些设置都没有问题，请检查到协调器的网络路径是否允许 TLS 通过。',
    'Invite created. Its command is below.': '邀请已创建。其命令在下方。',
    'Creating invite…': '正在创建邀请…',
    'Check that worker pods can reach this address. Workers in another namespace need the namespace-qualified Service address, such as dagu-coordinator.<namespace>.svc:50055.':
      '请确认 Worker Pod 可以访问此地址。其他命名空间中的 Worker 需要使用带命名空间的 Service 地址，例如 dagu-coordinator.<namespace>.svc:50055。',
    "Run this with the same kubectl context and namespace you applied the manifest with. The worker's log names the cause:":
      '请使用应用清单时相同的 kubectl 上下文和命名空间运行此命令。Worker 的日志会说明原因：',
    'Run this on the worker host, which needs Docker. For another worker on the same host, change the volume dagu-worker to one of its own and use a new invite, or a reusable one.':
      '在 Worker 主机上运行此命令，该主机需要 Docker。如需在同一主机上运行另一个 Worker，请将卷 dagu-worker 改为它自己的卷，并使用新的邀请或可重复使用的邀请。',
    "Enter the coordinator Service's address and port. Workers in another namespace need its namespace-qualified name, such as dagu-coordinator.<namespace>.svc:50055.":
      '输入协调器 Service 的地址和端口。其他命名空间中的 Worker 需要使用带命名空间的名称，例如 dagu-coordinator.<namespace>.svc:50055。',
    'Diagnostic command copied': '诊断命令已复制',
    'Command copied': '命令已复制',
    'Copy the diagnostic command': '复制诊断命令',
    "Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload, then restart it. A coordinator in its own pod shares its storage with the scheduler and the web UI, so all three need a volume they can each write; worker pods keep their own. With the Helm chart, use deploymentMode: distributed, which sets the variable. Move the data to a ReadWriteMany claim, then set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany before you upgrade.":
      '在协调器的工作负载上将 DAGU_COORDINATOR_HOST 设为 0.0.0.0，然后重启它。在独立 Pod 中运行的协调器与调度器和 Web UI 共享存储，因此三者都需要各自可写的卷；Worker Pod 使用自己的卷。使用 Helm Chart 时，请使用 deploymentMode: distributed，它会设置该变量。将数据迁移到 ReadWriteMany 声明，然后在升级前将 persistence.existingClaim 设为该声明，并将 persistence.accessMode 设为 ReadWriteMany。',
    'Outside the cluster': '在集群外',
    'Inside this cluster': '在此集群内',
    'Where the workers run': 'Worker 运行的位置',
    'Mutual TLS': '双向 TLS',
    'Server TLS': '服务器 TLS',
    'This coordinator uses its own certificates, peer.cert_file and peer.key_file, so it has no built-in CA for invites. Add the worker with your own certificates and no invite, as server TLS describes, and mutual TLS when the coordinator sets peer.client_ca_file. Or move every Dagu process to built-in TLS together.':
      '此协调器使用自己的证书（peer.cert_file 和 peer.key_file），因此没有用于邀请的内置 CA。请按服务器 TLS 的说明使用您自己的证书添加 Worker，不使用邀请；协调器设置了 peer.client_ca_file 时，请按双向 TLS 的说明操作。或者将所有 Dagu 进程一起迁移到内置 TLS。',
    'Move to built-in TLS': '迁移到内置 TLS',
    "Enter the address of the coordinator Service that workers reach: its load balancer, or for NodePort, a node's address and the assigned node port. The Service's own name works only inside the cluster.":
      '输入 Worker 可访问的协调器 Service 地址：其负载均衡器；如果使用 NodePort，则为节点地址和分配的节点端口。Service 自身的名称只在集群内有效。',
    '{count} workers joined with this invite.':
      '已有 {count} 个 Worker 通过此邀请加入。',
    '1 worker joined with this invite.': '已有 1 个 Worker 通过此邀请加入。',
    'Looking for the worker...': '正在查找 Worker...',
    'Run this on the worker host. From a fresh container, it shows whether the host reaches the address and what the coordinator answers:':
      '在 Worker 主机上运行以下命令。它在新的容器中显示该主机能否访问该地址以及协调器的响应：',
    'Run this on the worker host. From an empty data directory, it dials even after the worker joined, and shows whether the host reaches the address and what the coordinator answers:':
      '在 Worker 主机上运行以下命令。它使用空的数据目录，因此即使 Worker 已加入也会发起连接，并显示该主机能否访问该地址以及协调器的响应：',
    'Switch to the local server to add a worker.':
      '切换到本地服务器以添加 Worker。',
    'Copy the change': '复制修改内容',
    'Check again': '重新检查',
    'The coordinator cannot do this yet. Try again in a moment.':
      '协调器暂时无法执行此操作。请稍后重试。',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      '协调器在当前状态下无法执行此操作。其日志说明了原因；修复后请重试。',
    'Shown after step 1': '完成步骤 1 后显示',
    'The workers list could not be read, so the dialog cannot tell whether the worker joined.':
      '无法读取 Worker 列表，因此对话框无法判断 Worker 是否已加入。',
    'This invite was made for {address}. To use {next}, create another invite.':
      '此邀请是为 {address} 创建的。要使用 {next}，请创建另一个邀请。',
    'Label {position} sets a key an earlier label sets; keys compare without case.':
      '第 {position} 个标签设置的键已由前面的标签设置；键不区分大小写。',
    'Label {position} is empty. Write one key or key=value.':
      '第 {position} 个标签为空。请写一个 key 或 key=value。',
    'Label {position} holds an invite. The invite goes only into the commands.':
      '第 {position} 个标签中包含邀请。邀请只应放在命令中。',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} 是多播或广播地址。请输入一台机器的地址。',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      '参数 {name} 设置了 coordinator.host。在启动 Dagu 的地方将其设为 0.0.0.0：',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      '在 {file} 中，于 coordinator 块内将 host 设为 0.0.0.0；如果文件中没有该块，请添加。',
    'Enter host:port, such as 192.168.1.20:50055.':
      '请输入 host:port，例如 192.168.1.20:50055。',
    'The address holds an invite. The invite goes only into the commands.':
      '地址中包含邀请。邀请只应放在命令中。',
    'Address and port': '地址和端口',
    'Look for the worker again': '再次查找 Worker',
    'Label {position} sets {key}, which a worker sets from its own host.':
      '第 {position} 个标签设置了 {key}，而 Worker 会根据自身主机设置它。',
    'Label {position} breaks the worker label rule: a key takes letters, digits, -, _, and ., at most 63, and a value also /, at most 255, each starting with a letter or digit.':
      '第 {position} 个标签不符合 Worker 标签规则：键只能使用字母、数字、-、_ 和 .，最多 63 个字符；值还可以使用 /，最多 255 个字符；两者都必须以字母或数字开头。',
    'Save this as a file and apply it with kubectl apply -f to the cluster that runs the workers.':
      '将以下内容保存为文件，并用 kubectl apply -f 应用到运行 Worker 的集群。',
    'This server is a development build ({version}), so no published image matches it. Run a released Dagu to add a worker with Docker or Kubernetes, or use the Linux tab.':
      '此服务器是开发版本（{version}），没有与之匹配的已发布镜像。请运行已发布的 Dagu 版本来通过 Docker 或 Kubernetes 添加 Worker，或使用 Linux 选项卡。',
    'Installation guide': '安装指南',
    'Install Dagu on the worker host, then run this there.':
      '在 Worker 主机上安装 Dagu，然后在那里运行以下命令。',
    'The coordinator cannot create invites.': '协调器无法创建邀请。',
    'Copy the command': '复制命令',
    'How the worker runs': 'Worker 的运行方式',
    'Labels (optional)': '标签（可选）',
    Invite: '邀请',
    'Single use: one worker that keeps its data directory. Expires after 24 hours unless used.':
      '一次性：用于一个保留其数据目录的 Worker。未使用时 24 小时后过期。',
    'Reusable: any number of hosts. Lasts until deleted.':
      '可重复使用：任意数量的主机。删除前一直有效。',
    'Reusable: any number of containers, each with its own volume. Lasts until deleted.':
      '可重复使用：任意数量的容器，每个容器使用自己的卷。删除前一直有效。',
    'Reusable invite: pods are replaced and scaled, and each new pod joins with it. It lasts until deleted.':
      '可重复使用的邀请：Pod 会被替换和扩缩，每个新 Pod 都用它加入。删除前一直有效。',
    'Create invite': '创建邀请',
    'Single-use invite, expires {time}.': '一次性邀请，{time} 过期。',
    'Reusable invite, expires {time}.': '可重复使用的邀请，{time} 过期。',
    'Reusable invite, until deleted.': '可重复使用的邀请，删除前一直有效。',
    'The invite is shown only here. Closing the dialog keeps it.':
      '邀请只在此处显示。关闭对话框后邀请仍然保留。',
    'Waiting for the worker to join...': '正在等待 Worker 加入...',
    'No worker has joined yet. The usual causes:':
      '还没有 Worker 加入。常见原因：',
    'A firewall or security group closes port {port}.':
      '防火墙或安全组关闭了端口 {port}。',
    'The container that runs Dagu does not publish port {port}.':
      '运行 Dagu 的容器没有发布端口 {port}。',
    'The worker cannot reach {address}.': 'Worker 无法访问 {address}。',
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
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      '此服务器的 peer.token 与协调器的不同。请在两者上设置相同的 peer.token，然后重启它们。',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      '此服务器没有协调器的 peer token。请将此服务器的 peer.token 设为与协调器相同，然后重启它。',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      '没有正在运行或可访问的协调器。请启动一个（例如使用 dagu start-all），然后重新检查。',
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
    'Use this address': 'このアドレスを使う',
    'The coordinator now reports {address}.':
      'コーディネーターは現在 {address} を報告しています。',
    "The coordinator requires TLS. On this server, clear any peer.insecure: true or peer.skip_tls_verify: true, whether a flag, a variable, or the config file sets it. If this server has no usable CA certificate for the coordinator, switch it to built-in TLS: clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate. Then restart it. If these settings are in order, check that the network path to the coordinator passes TLS.":
      'コーディネーターは TLS を必要とします。このサーバーで peer.insecure: true と peer.skip_tls_verify: true を、フラグ、変数、設定ファイルのどれで設定されていてもすべて解除してください。このサーバーにコーディネーター用の使える CA 証明書がない場合は、組み込み TLS に切り替えてください。peer.cert_file、peer.key_file、peer.client_ca_file を解除してから、peer.ca_cert_file をコーディネーターの CA 証明書にします。その後再起動してください。これらの設定に問題がなければ、コーディネーターまでのネットワーク経路が TLS を通すか確認してください。',
    'Invite created. Its command is below.':
      '招待を作成しました。コマンドは下にあります。',
    'Creating invite…': '招待を作成しています…',
    'Check that worker pods can reach this address. Workers in another namespace need the namespace-qualified Service address, such as dagu-coordinator.<namespace>.svc:50055.':
      'ワーカー Pod がこのアドレスに到達できることを確認してください。別の名前空間のワーカーには、dagu-coordinator.<namespace>.svc:50055 のような名前空間付きの Service アドレスが必要です。',
    "Run this with the same kubectl context and namespace you applied the manifest with. The worker's log names the cause:":
      'マニフェストを適用したときと同じ kubectl のコンテキストと名前空間でこれを実行してください。ワーカーのログに原因が示されます:',
    'Run this on the worker host, which needs Docker. For another worker on the same host, change the volume dagu-worker to one of its own and use a new invite, or a reusable one.':
      'ワーカーホストでこれを実行してください。ホストには Docker が必要です。同じホストで別のワーカーを動かす場合は、ボリューム dagu-worker をそのワーカー専用のものに変え、新しい招待か再利用可能な招待を使ってください。',
    "Enter the coordinator Service's address and port. Workers in another namespace need its namespace-qualified name, such as dagu-coordinator.<namespace>.svc:50055.":
      'コーディネーター Service のアドレスとポートを入力してください。別の名前空間のワーカーには、dagu-coordinator.<namespace>.svc:50055 のような名前空間付きの名前が必要です。',
    'Diagnostic command copied': '診断コマンドをコピーしました',
    'Command copied': 'コマンドをコピーしました',
    'Copy the diagnostic command': '診断コマンドをコピー',
    "Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload, then restart it. A coordinator in its own pod shares its storage with the scheduler and the web UI, so all three need a volume they can each write; worker pods keep their own. With the Helm chart, use deploymentMode: distributed, which sets the variable. Move the data to a ReadWriteMany claim, then set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany before you upgrade.":
      'コーディネーターのワークロードで DAGU_COORDINATOR_HOST を 0.0.0.0 にし、再起動してください。独自の Pod で動くコーディネーターはスケジューラーおよび Web UI とストレージを共有するため、3 つとも書き込めるボリュームが必要です。ワーカー Pod は自分のボリュームを使います。Helm チャートでは deploymentMode: distributed を使ってください。これがこの変数を設定します。データを ReadWriteMany のクレームに移し、アップグレードの前に persistence.existingClaim をそのクレームに、persistence.accessMode を ReadWriteMany にしてください。',
    'Outside the cluster': 'クラスターの外',
    'Inside this cluster': 'このクラスター内',
    'Where the workers run': 'ワーカーを動かす場所',
    'Mutual TLS': '相互 TLS',
    'Server TLS': 'サーバー TLS',
    'This coordinator uses its own certificates, peer.cert_file and peer.key_file, so it has no built-in CA for invites. Add the worker with your own certificates and no invite, as server TLS describes, and mutual TLS when the coordinator sets peer.client_ca_file. Or move every Dagu process to built-in TLS together.':
      'このコーディネーターは独自の証明書 (peer.cert_file と peer.key_file) を使っているため、招待用の組み込み CA がありません。サーバー TLS の説明に従い、招待を使わずに独自の証明書でワーカーを追加してください。コーディネーターが peer.client_ca_file を設定している場合は相互 TLS の説明に従ってください。または、すべての Dagu プロセスを一緒に組み込み TLS に移行してください。',
    'Move to built-in TLS': '組み込み TLS に移行',
    "Enter the address of the coordinator Service that workers reach: its load balancer, or for NodePort, a node's address and the assigned node port. The Service's own name works only inside the cluster.":
      'ワーカーが到達できるコーディネーター Service のアドレスを入力してください。ロードバランサー、または NodePort ならノードのアドレスと割り当てられたノードポートです。Service 自体の名前はクラスター内でのみ使えます。',
    '{count} workers joined with this invite.':
      'この招待で {count} 台のワーカーが参加しました。',
    '1 worker joined with this invite.':
      'この招待で 1 台のワーカーが参加しました。',
    'Looking for the worker...': 'ワーカーを探しています...',
    'Run this on the worker host. From a fresh container, it shows whether the host reaches the address and what the coordinator answers:':
      'ワーカーのホストでこれを実行してください。新しいコンテナで、ホストがアドレスに届くかとコーディネーターの応答を表示します:',
    'Run this on the worker host. From an empty data directory, it dials even after the worker joined, and shows whether the host reaches the address and what the coordinator answers:':
      'ワーカーのホストでこれを実行してください。空のデータディレクトリを使うので、ワーカーが参加した後でも接続し、ホストがアドレスに届くかとコーディネーターの応答を表示します:',
    'Switch to the local server to add a worker.':
      'ワーカーを追加するにはローカルサーバーに切り替えてください。',
    'Copy the change': '変更をコピー',
    'Check again': '再確認',
    'The coordinator cannot do this yet. Try again in a moment.':
      'コーディネーターはまだこれを実行できません。少し待ってからもう一度試してください。',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'コーディネーターは現在の状態ではこれを実行できません。理由はそのログにあります。修正してからもう一度試してください。',
    'Shown after step 1': 'ステップ 1 の後に表示',
    'The workers list could not be read, so the dialog cannot tell whether the worker joined.':
      'ワーカーの一覧を読めなかったため、ワーカーが参加したかどうか分かりません。',
    'This invite was made for {address}. To use {next}, create another invite.':
      'この招待は {address} 用に作成されました。{next} を使うには、別の招待を作成してください。',
    'Label {position} sets a key an earlier label sets; keys compare without case.':
      'ラベル {position} は前のラベルと同じキーを設定しています。キーは大文字と小文字を区別しません。',
    'Label {position} is empty. Write one key or key=value.':
      'ラベル {position} が空です。key か key=value を 1 つ書いてください。',
    'Label {position} holds an invite. The invite goes only into the commands.':
      'ラベル {position} に招待が含まれています。招待はコマンドにだけ入れます。',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} はマルチキャストまたはブロードキャストのアドレスです。1 台のマシンのアドレスを入力してください。',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'フラグ {name} が coordinator.host を設定しています。Dagu を起動する場所で 0.0.0.0 を指定してください:',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      '{file} の coordinator ブロック内で host を 0.0.0.0 にしてください。ブロックがなければ追加してください。',
    'Enter host:port, such as 192.168.1.20:50055.':
      '192.168.1.20:50055 のように host:port を入力してください。',
    'The address holds an invite. The invite goes only into the commands.':
      'アドレスに招待が含まれています。招待はコマンドにだけ入れます。',
    'Address and port': 'アドレスとポート',
    'Look for the worker again': 'ワーカーをもう一度探す',
    'Label {position} sets {key}, which a worker sets from its own host.':
      'ラベル {position} は {key} を設定していますが、これはワーカーが自身のホストから設定します。',
    'Label {position} breaks the worker label rule: a key takes letters, digits, -, _, and ., at most 63, and a value also /, at most 255, each starting with a letter or digit.':
      'ラベル {position} はワーカーのラベル規則に反しています: キーは英字、数字、-、_、. で 63 文字まで、値はそれに加えて / も使え 255 文字まで、どちらも英字か数字で始めます。',
    'Save this as a file and apply it with kubectl apply -f to the cluster that runs the workers.':
      'これをファイルに保存し、ワーカーを実行するクラスターに kubectl apply -f で適用してください。',
    'This server is a development build ({version}), so no published image matches it. Run a released Dagu to add a worker with Docker or Kubernetes, or use the Linux tab.':
      'このサーバーは開発ビルド ({version}) のため、対応する公開イメージがありません。Docker や Kubernetes でワーカーを追加するにはリリース版の Dagu を実行するか、Linux タブを使ってください。',
    'Installation guide': 'インストールガイド',
    'Install Dagu on the worker host, then run this there.':
      'ワーカーのホストに Dagu をインストールし、そこでこれを実行してください。',
    'The coordinator cannot create invites.':
      'コーディネーターは招待を作成できません。',
    'Copy the command': 'コマンドをコピー',
    'How the worker runs': 'ワーカーの実行方法',
    'Labels (optional)': 'ラベル (任意)',
    Invite: '招待',
    'Single use: one worker that keeps its data directory. Expires after 24 hours unless used.':
      '1 回限り: データディレクトリを保持する 1 台のワーカー用。使われなければ 24 時間後に期限切れ。',
    'Reusable: any number of hosts. Lasts until deleted.':
      '再利用可能: 任意の数のホスト用。削除するまで有効。',
    'Reusable: any number of containers, each with its own volume. Lasts until deleted.':
      '再利用可能: それぞれ専用のボリュームを持つ任意の数のコンテナ用。削除するまで有効。',
    'Reusable invite: pods are replaced and scaled, and each new pod joins with it. It lasts until deleted.':
      '再利用可能な招待: Pod は置き換えやスケールが起こり、新しい Pod はそれぞれこの招待で参加します。削除するまで有効です。',
    'Create invite': '招待を作成',
    'Single-use invite, expires {time}.': '1 回限りの招待、{time} に期限切れ。',
    'Reusable invite, expires {time}.': '再利用可能な招待、{time} に期限切れ。',
    'Reusable invite, until deleted.': '再利用可能な招待、削除するまで有効。',
    'The invite is shown only here. Closing the dialog keeps it.':
      '招待はここにだけ表示されます。ダイアログを閉じても招待は残ります。',
    'Waiting for the worker to join...': 'ワーカーの参加を待っています...',
    'No worker has joined yet. The usual causes:':
      'まだワーカーが参加していません。よくある原因:',
    'A firewall or security group closes port {port}.':
      'ファイアウォールかセキュリティグループがポート {port} を閉じている。',
    'The container that runs Dagu does not publish port {port}.':
      'Dagu を実行するコンテナがポート {port} を公開していない。',
    'The worker cannot reach {address}.': 'ワーカーから {address} に届かない。',
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
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      'このサーバーの peer.token はコーディネーターのものと異なります。両方に同じ peer.token を設定し、再起動してください。',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      'このサーバーにはコーディネーター用の peer token がありません。このサーバーの peer.token をコーディネーターと同じにして、再起動してください。',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      '実行中または到達可能なコーディネーターがありません。dagu start-all などで起動してから再確認してください。',
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
    'Use this address': 'Diese Adresse verwenden',
    'The coordinator now reports {address}.':
      'Der Koordinator meldet jetzt {address}.',
    "The coordinator requires TLS. On this server, clear any peer.insecure: true or peer.skip_tls_verify: true, whether a flag, a variable, or the config file sets it. If this server has no usable CA certificate for the coordinator, switch it to built-in TLS: clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate. Then restart it. If these settings are in order, check that the network path to the coordinator passes TLS.":
      'Der Koordinator verlangt TLS. Entfernen Sie auf diesem Server jedes peer.insecure: true und peer.skip_tls_verify: true, ob ein Flag, eine Variable oder die Konfigurationsdatei es setzt. Hat dieser Server kein nutzbares CA-Zertifikat für den Koordinator, stellen Sie ihn auf eingebautes TLS um: Entfernen Sie peer.cert_file, peer.key_file und peer.client_ca_file und setzen Sie dann peer.ca_cert_file auf das CA-Zertifikat des Koordinators. Starten Sie ihn dann neu. Sind diese Einstellungen in Ordnung, prüfen Sie, ob der Netzwerkpfad zum Koordinator TLS durchlässt.',
    'Invite created. Its command is below.':
      'Einladung erstellt. Ihr Befehl steht unten.',
    'Creating invite…': 'Einladung wird erstellt…',
    'Check that worker pods can reach this address. Workers in another namespace need the namespace-qualified Service address, such as dagu-coordinator.<namespace>.svc:50055.':
      'Prüfen Sie, dass Worker-Pods diese Adresse erreichen. Worker in einem anderen Namespace brauchen die Service-Adresse mit Namespace, etwa dagu-coordinator.<namespace>.svc:50055.',
    "Run this with the same kubectl context and namespace you applied the manifest with. The worker's log names the cause:":
      'Führen Sie dies mit demselben kubectl-Kontext und Namespace aus, mit dem Sie das Manifest angewendet haben. Das Log des Workers nennt die Ursache:',
    'Run this on the worker host, which needs Docker. For another worker on the same host, change the volume dagu-worker to one of its own and use a new invite, or a reusable one.':
      'Führen Sie dies auf dem Worker-Host aus, der Docker braucht. Für einen weiteren Worker auf demselben Host ändern Sie das Volume dagu-worker in ein eigenes und verwenden eine neue oder eine wiederverwendbare Einladung.',
    "Enter the coordinator Service's address and port. Workers in another namespace need its namespace-qualified name, such as dagu-coordinator.<namespace>.svc:50055.":
      'Geben Sie Adresse und Port des Koordinator-Service ein. Worker in einem anderen Namespace brauchen seinen Namen mit Namespace, etwa dagu-coordinator.<namespace>.svc:50055.',
    'Diagnostic command copied': 'Diagnosebefehl kopiert',
    'Command copied': 'Befehl kopiert',
    'Copy the diagnostic command': 'Diagnosebefehl kopieren',
    "Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload, then restart it. A coordinator in its own pod shares its storage with the scheduler and the web UI, so all three need a volume they can each write; worker pods keep their own. With the Helm chart, use deploymentMode: distributed, which sets the variable. Move the data to a ReadWriteMany claim, then set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany before you upgrade.":
      'Setzen Sie DAGU_COORDINATOR_HOST auf dem Workload des Koordinators auf 0.0.0.0 und starten Sie ihn neu. Ein Koordinator in einem eigenen Pod teilt seinen Speicher mit dem Scheduler und der Web-UI, daher brauchen alle drei ein Volume, in das jeder schreiben kann; Worker-Pods behalten ihr eigenes. Mit dem Helm-Chart nutzen Sie deploymentMode: distributed, das die Variable setzt. Verschieben Sie die Daten in einen ReadWriteMany-Claim und setzen Sie vor dem Upgrade persistence.existingClaim darauf und persistence.accessMode auf ReadWriteMany.',
    'Outside the cluster': 'Außerhalb des Clusters',
    'Inside this cluster': 'In diesem Cluster',
    'Where the workers run': 'Wo die Worker laufen',
    'Mutual TLS': 'Mutual TLS',
    'Server TLS': 'Server-TLS',
    'This coordinator uses its own certificates, peer.cert_file and peer.key_file, so it has no built-in CA for invites. Add the worker with your own certificates and no invite, as server TLS describes, and mutual TLS when the coordinator sets peer.client_ca_file. Or move every Dagu process to built-in TLS together.':
      'Dieser Koordinator nutzt eigene Zertifikate, peer.cert_file und peer.key_file, und hat daher keine eingebaute CA für Einladungen. Fügen Sie den Worker mit Ihren eigenen Zertifikaten und ohne Einladung hinzu, wie Server-TLS beschreibt, und wie Mutual TLS beschreibt, wenn der Koordinator peer.client_ca_file setzt. Oder stellen Sie alle Dagu-Prozesse gemeinsam auf eingebautes TLS um.',
    'Move to built-in TLS': 'Auf eingebautes TLS umstellen',
    "Enter the address of the coordinator Service that workers reach: its load balancer, or for NodePort, a node's address and the assigned node port. The Service's own name works only inside the cluster.":
      'Geben Sie die Adresse des Koordinator-Service ein, die Worker erreichen: seinen Load Balancer oder bei NodePort die Adresse eines Knotens und den zugewiesenen Knotenport. Der Name des Service selbst funktioniert nur im Cluster.',
    '{count} workers joined with this invite.':
      '{count} Worker sind mit dieser Einladung beigetreten.',
    '1 worker joined with this invite.':
      '1 Worker ist mit dieser Einladung beigetreten.',
    'Looking for the worker...': 'Suche nach dem Worker...',
    'Run this on the worker host. From a fresh container, it shows whether the host reaches the address and what the coordinator answers:':
      'Führen Sie dies auf dem Worker-Host aus. In einem frischen Container zeigt es, ob der Host die Adresse erreicht und was der Koordinator antwortet:',
    'Run this on the worker host. From an empty data directory, it dials even after the worker joined, and shows whether the host reaches the address and what the coordinator answers:':
      'Führen Sie dies auf dem Worker-Host aus. Mit einem leeren Datenverzeichnis wählt es sich auch nach dem Beitritt des Workers ein und zeigt, ob der Host die Adresse erreicht und was der Koordinator antwortet:',
    'Switch to the local server to add a worker.':
      'Wechseln Sie zum lokalen Server, um einen Worker hinzuzufügen.',
    'Copy the change': 'Änderung kopieren',
    'Check again': 'Erneut prüfen',
    'The coordinator cannot do this yet. Try again in a moment.':
      'Der Koordinator kann dies noch nicht tun. Versuchen Sie es gleich noch einmal.',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'Der Koordinator kann dies in seinem jetzigen Zustand nicht tun. Sein Log nennt den Grund; beheben Sie ihn und versuchen Sie es erneut.',
    'Shown after step 1': 'Erscheint nach Schritt 1',
    'The workers list could not be read, so the dialog cannot tell whether the worker joined.':
      'Die Worker-Liste konnte nicht gelesen werden, daher kann der Dialog nicht sagen, ob der Worker beigetreten ist.',
    'This invite was made for {address}. To use {next}, create another invite.':
      'Diese Einladung wurde für {address} erstellt. Um {next} zu verwenden, erstellen Sie eine weitere Einladung.',
    'Label {position} sets a key an earlier label sets; keys compare without case.':
      'Label {position} setzt einen Schlüssel, den ein früheres Label setzt; Schlüssel werden ohne Groß- und Kleinschreibung verglichen.',
    'Label {position} is empty. Write one key or key=value.':
      'Label {position} ist leer. Schreiben Sie einen key oder key=value.',
    'Label {position} holds an invite. The invite goes only into the commands.':
      'Label {position} enthält eine Einladung. Die Einladung gehört nur in die Befehle.',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} ist eine Multicast- oder Broadcast-Adresse. Geben Sie die Adresse eines einzelnen Rechners ein.',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'Das Flag {name} setzt coordinator.host. Geben Sie ihm dort, wo Dagu startet, 0.0.0.0:',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      'Setzen Sie in {file} host innerhalb des coordinator-Blocks auf 0.0.0.0, und fügen Sie den Block hinzu, falls die Datei keinen hat.',
    'Enter host:port, such as 192.168.1.20:50055.':
      'Geben Sie host:port ein, etwa 192.168.1.20:50055.',
    'The address holds an invite. The invite goes only into the commands.':
      'Die Adresse enthält eine Einladung. Die Einladung gehört nur in die Befehle.',
    'Address and port': 'Adresse und Port',
    'Look for the worker again': 'Erneut nach dem Worker suchen',
    'Label {position} sets {key}, which a worker sets from its own host.':
      'Label {position} setzt {key}, das ein Worker selbst aus seinem Host setzt.',
    'Label {position} breaks the worker label rule: a key takes letters, digits, -, _, and ., at most 63, and a value also /, at most 255, each starting with a letter or digit.':
      'Label {position} verletzt die Label-Regel des Workers: Ein Schlüssel besteht aus Buchstaben, Ziffern, -, _ und ., höchstens 63, ein Wert auch aus /, höchstens 255, beide beginnen mit einem Buchstaben oder einer Ziffer.',
    'Save this as a file and apply it with kubectl apply -f to the cluster that runs the workers.':
      'Speichern Sie dies als Datei und wenden Sie es mit kubectl apply -f auf den Cluster an, in dem die Worker laufen.',
    'This server is a development build ({version}), so no published image matches it. Run a released Dagu to add a worker with Docker or Kubernetes, or use the Linux tab.':
      'Dieser Server ist ein Entwicklungs-Build ({version}), daher passt kein veröffentlichtes Image zu ihm. Betreiben Sie eine veröffentlichte Dagu-Version, um einen Worker mit Docker oder Kubernetes hinzuzufügen, oder nutzen Sie den Linux-Tab.',
    'Installation guide': 'Installationsanleitung',
    'Install Dagu on the worker host, then run this there.':
      'Installieren Sie Dagu auf dem Worker-Host und führen Sie dies dort aus.',
    'The coordinator cannot create invites.':
      'Der Koordinator kann keine Einladungen erstellen.',
    'Copy the command': 'Befehl kopieren',
    'How the worker runs': 'Wie der Worker läuft',
    'Labels (optional)': 'Labels (optional)',
    Invite: 'Einladung',
    'Single use: one worker that keeps its data directory. Expires after 24 hours unless used.':
      'Einmalig: ein Worker, der sein Datenverzeichnis behält. Läuft nach 24 Stunden ab, wenn unbenutzt.',
    'Reusable: any number of hosts. Lasts until deleted.':
      'Wiederverwendbar: beliebig viele Hosts. Gilt bis zum Löschen.',
    'Reusable: any number of containers, each with its own volume. Lasts until deleted.':
      'Wiederverwendbar: beliebig viele Container, jeder mit eigenem Volume. Gilt bis zum Löschen.',
    'Reusable invite: pods are replaced and scaled, and each new pod joins with it. It lasts until deleted.':
      'Wiederverwendbare Einladung: Pods werden ersetzt und skaliert, und jeder neue Pod tritt mit ihr bei. Sie gilt bis zum Löschen.',
    'Create invite': 'Einladung erstellen',
    'Single-use invite, expires {time}.':
      'Einmalige Einladung, läuft {time} ab.',
    'Reusable invite, expires {time}.':
      'Wiederverwendbare Einladung, läuft {time} ab.',
    'Reusable invite, until deleted.':
      'Wiederverwendbare Einladung, gilt bis zum Löschen.',
    'The invite is shown only here. Closing the dialog keeps it.':
      'Die Einladung erscheint nur hier. Das Schließen des Dialogs behält sie.',
    'Waiting for the worker to join...':
      'Warten auf den Beitritt des Workers...',
    'No worker has joined yet. The usual causes:':
      'Noch kein Worker ist beigetreten. Die üblichen Ursachen:',
    'A firewall or security group closes port {port}.':
      'Eine Firewall oder Sicherheitsgruppe sperrt Port {port}.',
    'The container that runs Dagu does not publish port {port}.':
      'Der Container, in dem Dagu läuft, veröffentlicht Port {port} nicht.',
    'The worker cannot reach {address}.':
      'Der Worker erreicht {address} nicht.',
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
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      'Das peer.token dieses Servers ist nicht das des Koordinators. Setzen Sie auf beiden dasselbe peer.token und starten Sie sie neu.',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      'Dieser Server hat kein Peer-Token für den Koordinator. Setzen Sie peer.token auf diesem Server auf das des Koordinators und starten Sie ihn neu.',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      'Kein Koordinator läuft oder ist erreichbar. Starten Sie einen, etwa mit dagu start-all, und prüfen Sie dann erneut.',
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
    'Use this address': 'Utiliser cette adresse',
    'The coordinator now reports {address}.':
      'Le coordinateur indique maintenant {address}.',
    "The coordinator requires TLS. On this server, clear any peer.insecure: true or peer.skip_tls_verify: true, whether a flag, a variable, or the config file sets it. If this server has no usable CA certificate for the coordinator, switch it to built-in TLS: clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate. Then restart it. If these settings are in order, check that the network path to the coordinator passes TLS.":
      'Le coordinateur exige TLS. Sur ce serveur, retirez tout peer.insecure : true ou peer.skip_tls_verify : true, qu’une option, une variable ou le fichier de configuration le définisse. Si ce serveur n’a pas de certificat de CA utilisable pour le coordinateur, passez-le au TLS intégré : retirez peer.cert_file, peer.key_file et peer.client_ca_file, puis définissez peer.ca_cert_file sur le certificat de CA du coordinateur. Puis redémarrez-le. Si ces réglages sont en ordre, vérifiez que le chemin réseau vers le coordinateur laisse passer TLS.',
    'Invite created. Its command is below.':
      'Invitation créée. Sa commande se trouve ci-dessous.',
    'Creating invite…': 'Création de l’invitation…',
    'Check that worker pods can reach this address. Workers in another namespace need the namespace-qualified Service address, such as dagu-coordinator.<namespace>.svc:50055.':
      'Vérifiez que les pods des workers atteignent cette adresse. Les workers d’un autre espace de noms ont besoin de l’adresse du Service qualifiée par l’espace de noms, par exemple dagu-coordinator.<namespace>.svc:50055.',
    "Run this with the same kubectl context and namespace you applied the manifest with. The worker's log names the cause:":
      'Exécutez ceci avec le même contexte kubectl et le même espace de noms que pour appliquer le manifeste. Le journal du worker indique la cause :',
    'Run this on the worker host, which needs Docker. For another worker on the same host, change the volume dagu-worker to one of its own and use a new invite, or a reusable one.':
      'Exécutez ceci sur l’hôte du worker, qui a besoin de Docker. Pour un autre worker sur le même hôte, remplacez le volume dagu-worker par un volume qui lui est propre et utilisez une nouvelle invitation, ou une invitation réutilisable.',
    "Enter the coordinator Service's address and port. Workers in another namespace need its namespace-qualified name, such as dagu-coordinator.<namespace>.svc:50055.":
      'Saisissez l’adresse et le port du Service du coordinateur. Les workers d’un autre espace de noms ont besoin de son nom qualifié par l’espace de noms, par exemple dagu-coordinator.<namespace>.svc:50055.',
    'Diagnostic command copied': 'Commande de diagnostic copiée',
    'Command copied': 'Commande copiée',
    'Copy the diagnostic command': 'Copier la commande de diagnostic',
    "Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload, then restart it. A coordinator in its own pod shares its storage with the scheduler and the web UI, so all three need a volume they can each write; worker pods keep their own. With the Helm chart, use deploymentMode: distributed, which sets the variable. Move the data to a ReadWriteMany claim, then set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany before you upgrade.":
      'Définissez DAGU_COORDINATOR_HOST à 0.0.0.0 sur la charge de travail du coordinateur, puis redémarrez-la. Un coordinateur dans son propre pod partage son stockage avec le planificateur et l’interface web, donc tous trois ont besoin d’un volume où chacun peut écrire ; les pods des workers gardent le leur. Avec le chart Helm, utilisez deploymentMode : distributed, qui définit la variable. Déplacez les données vers une demande ReadWriteMany, puis définissez persistence.existingClaim sur celle-ci et persistence.accessMode à ReadWriteMany avant la mise à jour.',
    'Outside the cluster': 'Hors du cluster',
    'Inside this cluster': 'Dans ce cluster',
    'Where the workers run': 'Où s’exécutent les workers',
    'Mutual TLS': 'TLS mutuel',
    'Server TLS': 'TLS serveur',
    'This coordinator uses its own certificates, peer.cert_file and peer.key_file, so it has no built-in CA for invites. Add the worker with your own certificates and no invite, as server TLS describes, and mutual TLS when the coordinator sets peer.client_ca_file. Or move every Dagu process to built-in TLS together.':
      'Ce coordinateur utilise ses propres certificats, peer.cert_file et peer.key_file, et n’a donc pas de CA intégrée pour les invitations. Ajoutez le worker avec vos propres certificats et sans invitation, comme le décrit TLS serveur, et TLS mutuel quand le coordinateur définit peer.client_ca_file. Ou passez tous les processus Dagu ensemble au TLS intégré.',
    'Move to built-in TLS': 'Passer au TLS intégré',
    "Enter the address of the coordinator Service that workers reach: its load balancer, or for NodePort, a node's address and the assigned node port. The Service's own name works only inside the cluster.":
      'Saisissez l’adresse du Service du coordinateur que les workers atteignent : son équilibreur de charge, ou pour NodePort, l’adresse d’un nœud et le port de nœud attribué. Le nom du Service ne fonctionne qu’à l’intérieur du cluster.',
    '{count} workers joined with this invite.':
      '{count} workers ont rejoint avec cette invitation.',
    '1 worker joined with this invite.':
      '1 worker a rejoint avec cette invitation.',
    'Looking for the worker...': 'Recherche du worker…',
    'Run this on the worker host. From a fresh container, it shows whether the host reaches the address and what the coordinator answers:':
      'Exécutez ceci sur l’hôte du worker. Dans un nouveau conteneur, cela montre si l’hôte joint l’adresse et ce que répond le coordinateur :',
    'Run this on the worker host. From an empty data directory, it dials even after the worker joined, and shows whether the host reaches the address and what the coordinator answers:':
      'Exécutez ceci sur l’hôte du worker. Avec un répertoire de données vide, cela se connecte même après que le worker a rejoint, et montre si l’hôte joint l’adresse et ce que répond le coordinateur :',
    'Switch to the local server to add a worker.':
      'Passez au serveur local pour ajouter un worker.',
    'Copy the change': 'Copier la modification',
    'Check again': 'Revérifier',
    'The coordinator cannot do this yet. Try again in a moment.':
      'Le coordinateur ne peut pas encore le faire. Réessayez dans un instant.',
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.':
      'Le coordinateur ne peut pas le faire dans son état actuel. Son journal en donne la raison ; corrigez-la, puis réessayez.',
    'Shown after step 1': 'Affiché après l’étape 1',
    'The workers list could not be read, so the dialog cannot tell whether the worker joined.':
      'La liste des workers n’a pas pu être lue, donc la boîte de dialogue ne peut pas dire si le worker a rejoint.',
    'This invite was made for {address}. To use {next}, create another invite.':
      'Cette invitation a été créée pour {address}. Pour utiliser {next}, créez une autre invitation.',
    'Label {position} sets a key an earlier label sets; keys compare without case.':
      'Le label {position} définit une clé qu’un label précédent définit déjà ; les clés se comparent sans tenir compte de la casse.',
    'Label {position} is empty. Write one key or key=value.':
      'Le label {position} est vide. Écrivez une clé ou une paire key=value.',
    'Label {position} holds an invite. The invite goes only into the commands.':
      'Le label {position} contient une invitation. L’invitation va seulement dans les commandes.',
    '{host} is a multicast or broadcast address. Enter the address of one machine.':
      '{host} est une adresse multicast ou de diffusion. Saisissez l’adresse d’une seule machine.',
    'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:':
      'L’option {name} définit coordinator.host. Donnez-lui 0.0.0.0 là où Dagu démarre :',
    'In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.':
      'Dans {file}, définissez host à 0.0.0.0 dans le bloc coordinator, en ajoutant le bloc si le fichier n’en a pas.',
    'Enter host:port, such as 192.168.1.20:50055.':
      'Saisissez host:port, par exemple 192.168.1.20:50055.',
    'The address holds an invite. The invite goes only into the commands.':
      'L’adresse contient une invitation. L’invitation va seulement dans les commandes.',
    'Address and port': 'Adresse et port',
    'Look for the worker again': 'Chercher à nouveau le worker',
    'Label {position} sets {key}, which a worker sets from its own host.':
      'Le label {position} définit {key}, que le worker définit d’après son propre hôte.',
    'Label {position} breaks the worker label rule: a key takes letters, digits, -, _, and ., at most 63, and a value also /, at most 255, each starting with a letter or digit.':
      'Le label {position} enfreint la règle des labels du worker : une clé prend des lettres, des chiffres, -, _ et ., 63 au plus, et une valeur aussi /, 255 au plus, chacune commençant par une lettre ou un chiffre.',
    'Save this as a file and apply it with kubectl apply -f to the cluster that runs the workers.':
      'Enregistrez ceci dans un fichier et appliquez-le avec kubectl apply -f au cluster qui exécute les workers.',
    'This server is a development build ({version}), so no published image matches it. Run a released Dagu to add a worker with Docker or Kubernetes, or use the Linux tab.':
      'Ce serveur est une version de développement ({version}), donc aucune image publiée ne lui correspond. Utilisez une version publiée de Dagu pour ajouter un worker avec Docker ou Kubernetes, ou utilisez l’onglet Linux.',
    'Installation guide': 'Guide d’installation',
    'Install Dagu on the worker host, then run this there.':
      'Installez Dagu sur l’hôte du worker, puis exécutez ceci sur cet hôte.',
    'The coordinator cannot create invites.':
      'Le coordinateur ne peut pas créer d’invitations.',
    'Copy the command': 'Copier la commande',
    'How the worker runs': 'Comment le worker s’exécute',
    'Labels (optional)': 'Labels (facultatif)',
    Invite: 'Invitation',
    'Single use: one worker that keeps its data directory. Expires after 24 hours unless used.':
      'Usage unique : un worker qui garde son répertoire de données. Expire après 24 heures si elle n’est pas utilisée.',
    'Reusable: any number of hosts. Lasts until deleted.':
      'Réutilisable : autant d’hôtes que voulu. Valable jusqu’à sa suppression.',
    'Reusable: any number of containers, each with its own volume. Lasts until deleted.':
      'Réutilisable : autant de conteneurs que voulu, chacun avec son volume. Valable jusqu’à sa suppression.',
    'Reusable invite: pods are replaced and scaled, and each new pod joins with it. It lasts until deleted.':
      'Invitation réutilisable : les pods sont remplacés et mis à l’échelle, et chaque nouveau pod rejoint avec elle. Elle est valable jusqu’à sa suppression.',
    'Create invite': 'Créer une invitation',
    'Single-use invite, expires {time}.':
      'Invitation à usage unique, expire le {time}.',
    'Reusable invite, expires {time}.':
      'Invitation réutilisable, expire le {time}.',
    'Reusable invite, until deleted.':
      'Invitation réutilisable, valable jusqu’à sa suppression.',
    'The invite is shown only here. Closing the dialog keeps it.':
      'L’invitation n’est affichée qu’ici. Fermer la boîte de dialogue la conserve.',
    'Waiting for the worker to join...': 'En attente du worker…',
    'No worker has joined yet. The usual causes:':
      'Aucun worker n’a encore rejoint. Les causes habituelles :',
    'A firewall or security group closes port {port}.':
      'Un pare-feu ou un groupe de sécurité ferme le port {port}.',
    'The container that runs Dagu does not publish port {port}.':
      'Le conteneur qui exécute Dagu ne publie pas le port {port}.',
    'The worker cannot reach {address}.':
      'Le worker ne peut pas joindre {address}.',
    '{host} is not one host. Enter an IP address without a zone, or a DNS name.':
      '{host} n’est pas un hôte unique. Saisissez une adresse IP sans zone, ou un nom DNS.',
    '{host} is a link-local address, which another network does not route. Enter another address of the machine.':
      '{host} est une adresse lien-local qu’un autre réseau ne route pas. Saisissez une autre adresse de la machine.',
    'Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again.':
      'Chaque modification nécessite un redémarrage de Dagu. Cette boîte de dialogue vérifie à nouveau quand le serveur revient ; sinon, utilisez Revérifier.',
    'This coordinator did not report. Start or upgrade it, then check again.':
      'Ce coordinateur n’a pas répondu. Démarrez-le ou mettez-le à jour, puis vérifiez à nouveau.',
    'The server refused the address. Check it and try again.':
      'Le serveur a refusé l’adresse. Vérifiez-la et réessayez.',
    'The coordinator refused this server. Its log names the reason.':
      'Le coordinateur a refusé ce serveur. Son journal en donne la raison.',
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.":
      'Le peer.token de ce serveur n’est pas celui du coordinateur. Définissez le même peer.token sur les deux, puis redémarrez-les.',
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.":
      'Ce serveur n’a pas de jeton pair pour le coordinateur. Donnez à peer.token sur ce serveur la valeur de celui du coordinateur, puis redémarrez-le.',
    'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.':
      'Aucun coordinateur ne fonctionne ou n’est joignable. Démarrez-en un, par exemple avec dagu start-all, puis vérifiez à nouveau.',
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
