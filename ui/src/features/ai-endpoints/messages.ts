// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

// The static messages of the AI endpoints: the Endpoints tab of the AI page
// and the Endpoint choice of the agent.run form. i18n/staticMessages.ts merges them.
export default defineMessages({
  en: {
    '(none)': '(none)',
    'Optional. The step uses the key of the endpoint.':
      'Optional. The step uses the key of the endpoint.',
    'No endpoint is available for this DAG.':
      'No endpoint is available for this DAG.',
    'Add one on the AI page': 'Add one on the AI page',
    'The server does not know the endpoint "{name}", or this DAG may not use it. The step fails when it starts.':
      'The server does not know the endpoint "{name}", or this DAG may not use it. The step fails when it starts.',
    'The endpoint "{name}" does not have the protocol anthropic, which the CLI claude needs. The step fails when it starts.':
      'The endpoint "{name}" does not have the protocol anthropic, which the CLI claude needs. The step fails when it starts.',
    Endpoints: 'Endpoints',
    Preset: 'Preset',
    'Add endpoint': 'Add endpoint',
    'Add an endpoint': 'Add an endpoint',
    'Edit {name}': 'Edit {name}',
    'Delete endpoint?': 'Delete endpoint?',
    'Delete endpoint {name}': 'Delete endpoint {name}',
    'Delete "{name}"? A step that names this endpoint fails until you create it again. The key is deleted too.':
      'Delete "{name}"? A step that names this endpoint fails until you create it again. The key is deleted too.',
    'Rotate the key of {name}': 'Rotate the key of {name}',
    'Rotate key': 'Rotate key',
    'New key': 'New key',
    'Key (optional)': 'Key (optional)',
    'No key': 'No key',
    'Key updated': 'Key updated',
    Allowed: 'Allowed',
    'Allowed workspaces': 'Allowed workspaces',
    'Allowed DAGs': 'Allowed DAGs',
    'All DAGs': 'All DAGs',
    '{count} DAGs': '{count} DAGs',
    '{count} workspaces': '{count} workspaces',
    'Workspace {name}': 'Workspace {name}',
    Protocol: 'Protocol',
    'Anthropic (CLI claude)': 'Anthropic (CLI claude)',
    OpenAI: 'OpenAI',
    'Models URL': 'Models URL',
    'Optional. The page that lists the models.':
      'Optional. The page that lists the models.',
    'Remove {name}': 'Remove {name}',
    'Type a name': 'Type a name',
    Suggestions: 'Suggestions',
    'No endpoints yet': 'No endpoints yet',
    "Couldn't load the endpoints": "Couldn't load the endpoints",
    "Couldn't load the presets of the endpoints.":
      "Couldn't load the presets of the endpoints.",
    'An endpoint holds the address and the key of a model API. A step names it, and the server keeps the key.':
      'An endpoint holds the address and the key of a model API. A step names it, and the server keeps the key.',
    'An endpoint holds the address and the key of a model API. A step names it. The step never sees the key.':
      'An endpoint holds the address and the key of a model API. A step names it. The step never sees the key.',
    'A step writes this name in its endpoint key. You cannot change it after you save.':
      'A step writes this name in its endpoint key. You cannot change it after you save.',
    'A local model can use http. The name localhost is the host of the coordinator, not of the worker.':
      'A local model can use http. The name localhost is the host of the coordinator, not of the worker.',
    'This URL uses http. The key goes over the network with no encryption.':
      'This URL uses http. The key goes over the network with no encryption.',
    'A local model needs no key. The server never shows a key after you save.':
      'A local model needs no key. The server never shows a key after you save.',
    'The server never shows the key again. It shows the first characters and a fingerprint.':
      'The server never shows the key again. It shows the first characters and a fingerprint.',
    'To change the key, use "Rotate key" in the list.':
      'To change the key, use "Rotate key" in the list.',
    'The new key replaces the old key. The server never shows a key after you save.':
      'The new key replaces the old key. The server never shows a key after you save.',
    'A step of a DAG in this list can use the endpoint.':
      'A step of a DAG in this list can use the endpoint.',
    'A DAG with no workspace matches only All workspaces.':
      'A DAG with no workspace matches only All workspaces.',
    'No agent step can use this endpoint yet.':
      'No agent step can use this endpoint yet.',
    'The CLI claude of an agent.run step uses this protocol.':
      'The CLI claude of an agent.run step uses this protocol.',
    'No step uses this protocol yet.': 'No step uses this protocol yet.',
    'The endpoint was not saved.': 'The endpoint was not saved.',
    'The endpoint was not deleted.': 'The endpoint was not deleted.',
    'The key was not saved.': 'The key was not saved.',
    'Only an administrator can manage the AI endpoints.':
      'Only an administrator can manage the AI endpoints.',
    'An endpoint with this name exists in this scope.':
      'An endpoint with this name exists in this scope.',
    'The workspace of the scope does not exist.':
      'The workspace of the scope does not exist.',
    'The endpoint does not exist. Reload the page.':
      'The endpoint does not exist. Reload the page.',
    'The server does not store AI endpoints.':
      'The server does not store AI endpoints.',
    'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.':
      'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.',
    'This preset needs a key.': 'This preset needs a key.',
    'The key is empty.': 'The key is empty.',
    'The key is too long. The limit is 8192 bytes.':
      'The key is too long. The limit is 8192 bytes.',
    'The key has a control character. Paste it again.':
      'The key has a control character. Paste it again.',
    'The key is not valid text.': 'The key is not valid text.',
    'Only a local model and Custom accept http. Use https.':
      'Only a local model and Custom accept http. Use https.',
    'The URL must start with https.': 'The URL must start with https.',
    'The URL is not valid.': 'The URL is not valid.',
    'The URL must not have user information, a query or a fragment.':
      'The URL must not have user information, a query or a fragment.',
    'This preset needs a base URL.': 'This preset needs a base URL.',
    'The form anthropic-oauth is valid with the Anthropic protocol only.':
      'The form anthropic-oauth is valid with the Anthropic protocol only.',
    'Choose an authentication form.': 'Choose an authentication form.',
    'Only Custom takes a models URL.': 'Only Custom takes a models URL.',
    'Choose at least one workspace, or all workspaces.':
      'Choose at least one workspace, or all workspaces.',
    'Choose at least one DAG, or all DAGs.':
      'Choose at least one DAG, or all DAGs.',
    'The value * must be the only item of its list.':
      'The value * must be the only item of its list.',
    'The description has more than 200 characters.':
      'The description has more than 200 characters.',
    'This preset has no such protocol.': 'This preset has no such protocol.',
    'Give the base URL.': 'Give the base URL.',
    'The URL must start with https. Only a local model and Custom accept http.':
      'The URL must start with https. Only a local model and Custom accept http.',
    'Choose at least one name, or All.': 'Choose at least one name, or All.',
    'Give the key.': 'Give the key.',
    'No model list': 'No model list',
    '1 model': '1 model',
    '{count} models': '{count} models',
    'Reading the models…': 'Reading the models…',
    'Last good read': 'Last good read',
    'Not read yet': 'Not read yet',
    'No good read yet': 'No good read yet',
    'The last read failed: {error}': 'The last read failed: {error}',
    'A token of a subscription can lack the right to list models. Type the model name in the step.':
      'A token of a subscription can lack the right to list models. Type the model name in the step.',
    'Reload the models of {name}': 'Reload the models of {name}',
    'The models were not reloaded.': 'The models were not reloaded.',
    'The models of {name}': 'The models of {name}',
    "Couldn't load the models.": "Couldn't load the models.",
    'Search the models': 'Search the models',
    '{shown} of {count} models': '{shown} of {count} models',
    'No model matches.': 'No model matches.',
    'This endpoint has no model list. Type the name of the model.':
      'This endpoint has no model list. Type the name of the model.',
    'Reload the list on the AI page': 'Reload the list on the AI page',
    'Not in the list of this endpoint. The step fails when it starts, unless a refresh of the list finds it.':
      'Not in the list of this endpoint. The step fails when it starts, unless a refresh of the list finds it.',
    'The model "{model}" is not in the list of the endpoint "{name}". The step fails when it starts, unless a refresh of the list finds it.':
      'The model "{model}" is not in the list of the endpoint "{name}". The step fails when it starts, unless a refresh of the list finds it.',
    'The CLI that runs the step. Change it in the form.':
      'The CLI that runs the step. Change it in the form.',
    'Model {index}': 'Model {index}',
    'Models of the endpoint': 'Models of the endpoint',
    '(choose a model)': '(choose a model)',
    'Select an endpoint': 'Select an endpoint',
    'chat.completion needs an endpoint or a provider; add an endpoint on the AI page and name it here':
      'chat.completion needs an endpoint or a provider; add an endpoint on the AI page and name it here',
    'chat.completion needs a model; choose one that the endpoint lists':
      'chat.completion needs a model; choose one that the endpoint lists',
    'chat.completion needs the name of an endpoint':
      'chat.completion needs the name of an endpoint',
    'Optional. Default uses the level of the model.':
      'Optional. Default uses the level of the model.',
    'Optional. Default uses the level of the CLI.':
      'Optional. Default uses the level of the CLI.',
    'agent.run needs a model; choose one that the endpoint lists':
      'agent.run needs a model; choose one that the endpoint lists',
    '{provider} (provider)': '{provider} (provider)',
    'The provider "{provider}" is deprecated: the step calls a model API with its own address and key. Pick an endpoint: the server keeps its key.':
      'The provider "{provider}" is deprecated: the step calls a model API with its own address and key. Pick an endpoint: the server keeps its key.',
    'agent.run needs an endpoint; add one on the AI page and name it here':
      'agent.run needs an endpoint; add one on the AI page and name it here',
    'agent.run needs the name of an endpoint':
      'agent.run needs the name of an endpoint',
    'agent.run needs a provider; it names the CLI provider':
      'agent.run needs a provider; it names the CLI provider',
    'endpoint "{name}" does not exist; add it on the AI page or choose another':
      'endpoint "{name}" does not exist; add it on the AI page or choose another',
    'endpoint must be a block with name and model':
      'endpoint must be a block with name and model',
    'Required. The step uses the key of the endpoint.':
      'Required. The step uses the key of the endpoint.',
    'Search the endpoints': 'Search the endpoints',
    'No endpoint matches.': 'No endpoint matches.',
    Effort: 'Effort',
  },
  'zh-CN': {
    '(none)': '（无）',
    'Optional. The step uses the key of the endpoint.':
      '可选。步骤使用该端点的密钥。',
    'No endpoint is available for this DAG.': '此 DAG 没有可用的端点。',
    'Add one on the AI page': '在 AI 页面添加',
    'The server does not know the endpoint "{name}", or this DAG may not use it. The step fails when it starts.':
      '服务器不认识端点“{name}”，或此 DAG 不能使用它。步骤启动时会失败。',
    'The endpoint "{name}" does not have the protocol anthropic, which the CLI claude needs. The step fails when it starts.':
      '端点“{name}”不是 CLI claude 所需的 anthropic 协议。步骤启动时会失败。',
    Endpoints: '端点',
    Preset: '预设',
    'Add endpoint': '添加端点',
    'Add an endpoint': '添加端点',
    'Edit {name}': '编辑 {name}',
    'Delete endpoint?': '删除端点？',
    'Delete endpoint {name}': '删除端点 {name}',
    'Delete "{name}"? A step that names this endpoint fails until you create it again. The key is deleted too.':
      '删除“{name}”？在重新创建之前，引用该端点的步骤会失败。密钥也会被删除。',
    'Rotate the key of {name}': '轮换 {name} 的密钥',
    'Rotate key': '轮换密钥',
    'New key': '新密钥',
    'Key (optional)': '密钥（可选）',
    'No key': '无密钥',
    'Key updated': '密钥更新时间',
    Allowed: '允许使用',
    'Allowed workspaces': '允许的工作区',
    'Allowed DAGs': '允许的 DAG',
    'All DAGs': '所有 DAG',
    '{count} DAGs': '{count} 个 DAG',
    '{count} workspaces': '{count} 个工作区',
    'Workspace {name}': '工作区 {name}',
    Protocol: '协议',
    'Anthropic (CLI claude)': 'Anthropic（CLI claude）',
    OpenAI: 'OpenAI',
    'Models URL': '模型列表 URL',
    'Optional. The page that lists the models.': '可选。列出模型的页面。',
    'Remove {name}': '移除 {name}',
    'Type a name': '输入名称',
    Suggestions: '建议',
    'No endpoints yet': '还没有端点',
    "Couldn't load the endpoints": '无法加载端点',
    "Couldn't load the presets of the endpoints.": '无法加载端点预设。',
    'An endpoint holds the address and the key of a model API. A step names it, and the server keeps the key.':
      '端点保存模型 API 的地址和密钥。步骤只引用它的名称，密钥由服务器保管。',
    'An endpoint holds the address and the key of a model API. A step names it. The step never sees the key.':
      '端点保存模型 API 的地址和密钥。步骤引用它的名称，但看不到密钥。',
    'A step writes this name in its endpoint key. You cannot change it after you save.':
      '步骤在其 endpoint 键中写入此名称。保存后不能更改。',
    'A local model can use http. The name localhost is the host of the coordinator, not of the worker.':
      '本地模型可以使用 http。名称 localhost 指协调器所在的主机，而不是 worker。',
    'This URL uses http. The key goes over the network with no encryption.':
      '此 URL 使用 http。密钥将不加密地在网络上传输。',
    'A local model needs no key. The server never shows a key after you save.':
      '本地模型不需要密钥。保存后服务器不会再显示密钥。',
    'The server never shows the key again. It shows the first characters and a fingerprint.':
      '服务器不会再次显示密钥，只显示开头的字符和指纹。',
    'To change the key, use "Rotate key" in the list.':
      '要更改密钥，请使用列表中的“轮换密钥”。',
    'The new key replaces the old key. The server never shows a key after you save.':
      '新密钥会替换旧密钥。保存后服务器不会再显示密钥。',
    'A step of a DAG in this list can use the endpoint.':
      '此列表中 DAG 的步骤可以使用该端点。',
    'A DAG with no workspace matches only All workspaces.':
      '没有工作区的 DAG 只匹配“全部工作区”。',
    'No agent step can use this endpoint yet.':
      '目前没有智能体步骤可以使用此端点。',
    'The CLI claude of an agent.run step uses this protocol.':
      'agent.run 步骤的 CLI claude 使用此协议。',
    'No step uses this protocol yet.': '目前没有步骤使用此协议。',
    'The endpoint was not saved.': '端点未保存。',
    'The endpoint was not deleted.': '端点未删除。',
    'The key was not saved.': '密钥未保存。',
    'Only an administrator can manage the AI endpoints.':
      '只有管理员可以管理 AI 端点。',
    'An endpoint with this name exists in this scope.':
      '此范围内已存在同名端点。',
    'The workspace of the scope does not exist.': '该范围的工作区不存在。',
    'The endpoint does not exist. Reload the page.': '端点不存在。请刷新页面。',
    'The server does not store AI endpoints.': '服务器不保存 AI 端点。',
    'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.':
      '名称必须以小写字母开头。可使用小写字母、数字和 -。最多 64 个字符。',
    'This preset needs a key.': '此预设需要密钥。',
    'The key is empty.': '密钥为空。',
    'The key is too long. The limit is 8192 bytes.':
      '密钥太长。上限为 8192 字节。',
    'The key has a control character. Paste it again.':
      '密钥含有控制字符。请重新粘贴。',
    'The key is not valid text.': '密钥不是有效的文本。',
    'Only a local model and Custom accept http. Use https.':
      '只有本地模型和自定义接受 http。请使用 https。',
    'The URL must start with https.': 'URL 必须以 https 开头。',
    'The URL is not valid.': 'URL 无效。',
    'The URL must not have user information, a query or a fragment.':
      'URL 不能包含用户信息、查询参数或片段。',
    'This preset needs a base URL.': '此预设需要基础 URL。',
    'The form anthropic-oauth is valid with the Anthropic protocol only.':
      'anthropic-oauth 仅适用于 Anthropic 协议。',
    'Choose an authentication form.': '请选择认证方式。',
    'Only Custom takes a models URL.': '只有自定义接受模型列表 URL。',
    'Choose at least one workspace, or all workspaces.':
      '请至少选择一个工作区，或选择所有工作区。',
    'Choose at least one DAG, or all DAGs.':
      '请至少选择一个 DAG，或选择所有 DAG。',
    'The value * must be the only item of its list.':
      '值 * 必须是列表中的唯一项。',
    'The description has more than 200 characters.': '说明超过 200 个字符。',
    'This preset has no such protocol.': '此预设没有该协议。',
    'Give the base URL.': '请填写基础 URL。',
    'The URL must start with https. Only a local model and Custom accept http.':
      'URL 必须以 https 开头。只有本地模型和自定义接受 http。',
    'Choose at least one name, or All.': '请至少选择一个名称，或选择“全部”。',
    'Give the key.': '请填写密钥。',
    'No model list': '无模型列表',
    '1 model': '1 个模型',
    '{count} models': '{count} 个模型',
    'Reading the models…': '正在读取模型…',
    'Last good read': '上次成功读取',
    'Not read yet': '尚未读取',
    'No good read yet': '尚无成功的读取',
    'The last read failed: {error}': '上次读取失败：{error}',
    'A token of a subscription can lack the right to list models. Type the model name in the step.':
      '订阅令牌可能无权列出模型。请在步骤中直接输入模型名称。',
    'Reload the models of {name}': '重新加载 {name} 的模型',
    'The models were not reloaded.': '未能重新加载模型。',
    'The models of {name}': '{name} 的模型',
    "Couldn't load the models.": '无法加载模型。',
    'Search the models': '搜索模型',
    '{shown} of {count} models': '{count} 个模型中的 {shown} 个',
    'No model matches.': '没有匹配的模型。',
    'This endpoint has no model list. Type the name of the model.':
      '此端点没有模型列表。请输入模型名称。',
    'Reload the list on the AI page': '在 AI 页面重新加载列表',
    'Not in the list of this endpoint. The step fails when it starts, unless a refresh of the list finds it.':
      '不在此端点的列表中。步骤启动时会失败，除非刷新列表后找到它。',
    'The model "{model}" is not in the list of the endpoint "{name}". The step fails when it starts, unless a refresh of the list finds it.':
      '模型“{model}”不在端点“{name}”的列表中。步骤启动时会失败，除非刷新列表后找到它。',
    'The CLI that runs the step. Change it in the form.':
      '运行该步骤的 CLI。请在表单中更改。',
    'Model {index}': '模型 {index}',
    'Models of the endpoint': '端点的模型',
    '(choose a model)': '（选择模型）',
    'Select an endpoint': '选择端点',
    'chat.completion needs an endpoint or a provider; add an endpoint on the AI page and name it here':
      'chat.completion 需要端点或提供方；请在 AI 页面添加端点，并在此处填写其名称',
    'chat.completion needs a model; choose one that the endpoint lists':
      'chat.completion 需要模型；请选择该端点列出的模型',
    'chat.completion needs the name of an endpoint':
      'chat.completion 需要端点的名称',
    'Optional. Default uses the level of the model.':
      '可选。默认使用模型的等级。',
    'Optional. Default uses the level of the CLI.':
      '可选。默认使用 CLI 的等级。',
    'agent.run needs a model; choose one that the endpoint lists':
      'agent.run 需要模型；请选择该端点列出的模型',
    '{provider} (provider)': '{provider}（提供方）',
    'The provider "{provider}" is deprecated: the step calls a model API with its own address and key. Pick an endpoint: the server keeps its key.':
      '提供方“{provider}”已弃用：步骤用自己的地址和密钥调用模型 API。请选择端点：密钥由服务器保管。',
    'agent.run needs an endpoint; add one on the AI page and name it here':
      'agent.run 需要端点；请在 AI 页面添加端点，并在此处填写其名称',
    'agent.run needs the name of an endpoint': 'agent.run 需要端点的名称',
    'agent.run needs a provider; it names the CLI provider':
      'agent.run 需要提供方；填写运行该步骤的 CLI 提供方',
    'endpoint "{name}" does not exist; add it on the AI page or choose another':
      '端点“{name}”不存在；请在 AI 页面添加，或选择其他端点',
    'endpoint must be a block with name and model':
      'endpoint 必须是含 name 和 model 的块',
    'Required. The step uses the key of the endpoint.':
      '必填。步骤使用该端点的密钥。',
    'Search the endpoints': '搜索端点',
    'No endpoint matches.': '没有匹配的端点。',
    Effort: '推理强度',
  },
  ja: {
    '(none)': '（なし）',
    'Optional. The step uses the key of the endpoint.':
      '任意です。ステップはエンドポイントのキーを使います。',
    'No endpoint is available for this DAG.':
      'この DAG が使えるエンドポイントはありません。',
    'Add one on the AI page': 'AI ページで追加',
    'The server does not know the endpoint "{name}", or this DAG may not use it. The step fails when it starts.':
      'サーバーはエンドポイント「{name}」を知らないか、この DAG は使えません。ステップは開始時に失敗します。',
    'The endpoint "{name}" does not have the protocol anthropic, which the CLI claude needs. The step fails when it starts.':
      'エンドポイント「{name}」は、CLI claude が必要とする anthropic プロトコルではありません。ステップは開始時に失敗します。',
    Endpoints: 'エンドポイント',
    Preset: 'プリセット',
    'Add endpoint': 'エンドポイントを追加',
    'Add an endpoint': 'エンドポイントを追加',
    'Edit {name}': '{name} を編集',
    'Delete endpoint?': 'エンドポイントを削除しますか？',
    'Delete endpoint {name}': 'エンドポイント {name} を削除',
    'Delete "{name}"? A step that names this endpoint fails until you create it again. The key is deleted too.':
      '「{name}」を削除しますか？再作成するまで、このエンドポイントを指定するステップは失敗します。キーも削除されます。',
    'Rotate the key of {name}': '{name} のキーをローテーション',
    'Rotate key': 'キーをローテーション',
    'New key': '新しいキー',
    'Key (optional)': 'キー（任意）',
    'No key': 'キーなし',
    'Key updated': 'キーの更新日時',
    Allowed: '使用を許可',
    'Allowed workspaces': '許可するワークスペース',
    'Allowed DAGs': '許可する DAG',
    'All DAGs': 'すべての DAG',
    '{count} DAGs': '{count} 件の DAG',
    '{count} workspaces': '{count} 件のワークスペース',
    'Workspace {name}': 'ワークスペース {name}',
    Protocol: 'プロトコル',
    'Anthropic (CLI claude)': 'Anthropic（CLI claude）',
    OpenAI: 'OpenAI',
    'Models URL': 'モデル一覧の URL',
    'Optional. The page that lists the models.':
      '任意です。モデルを一覧するページです。',
    'Remove {name}': '{name} を削除',
    'Type a name': '名前を入力',
    Suggestions: '候補',
    'No endpoints yet': 'エンドポイントはまだありません',
    "Couldn't load the endpoints": 'エンドポイントを読み込めませんでした',
    "Couldn't load the presets of the endpoints.":
      'エンドポイントのプリセットを読み込めませんでした。',
    'An endpoint holds the address and the key of a model API. A step names it, and the server keeps the key.':
      'エンドポイントはモデル API のアドレスとキーを保持します。ステップは名前だけを指定し、キーはサーバーが保管します。',
    'An endpoint holds the address and the key of a model API. A step names it. The step never sees the key.':
      'エンドポイントはモデル API のアドレスとキーを保持します。ステップは名前を指定しますが、キーは見えません。',
    'A step writes this name in its endpoint key. You cannot change it after you save.':
      'ステップは endpoint キーにこの名前を書きます。保存後は変更できません。',
    'A local model can use http. The name localhost is the host of the coordinator, not of the worker.':
      'ローカルモデルは http を使えます。localhost はワーカーではなく、コーディネーターのホストを指します。',
    'This URL uses http. The key goes over the network with no encryption.':
      'この URL は http です。キーは暗号化されずにネットワークを通ります。',
    'A local model needs no key. The server never shows a key after you save.':
      'ローカルモデルにキーは不要です。保存後、サーバーはキーを表示しません。',
    'The server never shows the key again. It shows the first characters and a fingerprint.':
      'サーバーはキーを再表示しません。先頭の文字とフィンガープリントだけを表示します。',
    'To change the key, use "Rotate key" in the list.':
      'キーを変更するには、一覧の「キーをローテーション」を使います。',
    'The new key replaces the old key. The server never shows a key after you save.':
      '新しいキーが古いキーを置き換えます。保存後、サーバーはキーを表示しません。',
    'A step of a DAG in this list can use the endpoint.':
      'この一覧の DAG のステップがエンドポイントを使えます。',
    'A DAG with no workspace matches only All workspaces.':
      'ワークスペースのない DAG は「すべてのワークスペース」にだけ一致します。',
    'No agent step can use this endpoint yet.':
      '今のところ、このエンドポイントを使えるエージェントステップはありません。',
    'The CLI claude of an agent.run step uses this protocol.':
      'agent.run ステップの CLI claude がこのプロトコルを使います。',
    'No step uses this protocol yet.':
      '今のところ、このプロトコルを使うステップはありません。',
    'The endpoint was not saved.': 'エンドポイントは保存されませんでした。',
    'The endpoint was not deleted.': 'エンドポイントは削除されませんでした。',
    'The key was not saved.': 'キーは保存されませんでした。',
    'Only an administrator can manage the AI endpoints.':
      'AI エンドポイントを管理できるのは管理者だけです。',
    'An endpoint with this name exists in this scope.':
      'この範囲に同じ名前のエンドポイントが既にあります。',
    'The workspace of the scope does not exist.':
      '範囲のワークスペースが存在しません。',
    'The endpoint does not exist. Reload the page.':
      'エンドポイントが存在しません。ページを再読み込みしてください。',
    'The server does not store AI endpoints.':
      'サーバーは AI エンドポイントを保存しません。',
    'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.':
      '名前の先頭は小文字の英字にします。小文字の英字、数字、- を使えます。上限は 64 文字です。',
    'This preset needs a key.': 'このプリセットにはキーが必要です。',
    'The key is empty.': 'キーが空です。',
    'The key is too long. The limit is 8192 bytes.':
      'キーが長すぎます。上限は 8192 バイトです。',
    'The key has a control character. Paste it again.':
      'キーに制御文字があります。貼り付け直してください。',
    'The key is not valid text.': 'キーが有効な文字列ではありません。',
    'Only a local model and Custom accept http. Use https.':
      'http を使えるのはローカルモデルとカスタムだけです。https を使ってください。',
    'The URL must start with https.': 'URL は https で始めてください。',
    'The URL is not valid.': 'URL が正しくありません。',
    'The URL must not have user information, a query or a fragment.':
      'URL にユーザー情報、クエリ、フラグメントは使えません。',
    'This preset needs a base URL.':
      'このプリセットにはベース URL が必要です。',
    'The form anthropic-oauth is valid with the Anthropic protocol only.':
      'anthropic-oauth は Anthropic プロトコルでだけ使えます。',
    'Choose an authentication form.': '認証方式を選んでください。',
    'Only Custom takes a models URL.':
      'モデル一覧の URL を指定できるのはカスタムだけです。',
    'Choose at least one workspace, or all workspaces.':
      'ワークスペースを 1 つ以上選ぶか、すべてを選んでください。',
    'Choose at least one DAG, or all DAGs.':
      'DAG を 1 つ以上選ぶか、すべてを選んでください。',
    'The value * must be the only item of its list.':
      '値 * は一覧の唯一の項目にしてください。',
    'The description has more than 200 characters.':
      '説明が 200 文字を超えています。',
    'This preset has no such protocol.':
      'このプリセットにそのプロトコルはありません。',
    'Give the base URL.': 'ベース URL を入力してください。',
    'The URL must start with https. Only a local model and Custom accept http.':
      'URL は https で始めてください。http を使えるのはローカルモデルとカスタムだけです。',
    'Choose at least one name, or All.':
      '名前を 1 つ以上選ぶか、「すべて」を選んでください。',
    'Give the key.': 'キーを入力してください。',
    'No model list': 'モデル一覧なし',
    '1 model': 'モデル 1 件',
    '{count} models': 'モデル {count} 件',
    'Reading the models…': 'モデルを読み込み中…',
    'Last good read': '最後に成功した読み込み',
    'Not read yet': '未読み込み',
    'No good read yet': '成功した読み込みはまだありません',
    'The last read failed: {error}': '前回の読み込みに失敗しました: {error}',
    'A token of a subscription can lack the right to list models. Type the model name in the step.':
      'サブスクリプションのトークンには、モデルを一覧する権限がない場合があります。ステップにモデル名を直接入力してください。',
    'Reload the models of {name}': '{name} のモデルを再読み込み',
    'The models were not reloaded.': 'モデルを再読み込みできませんでした。',
    'The models of {name}': '{name} のモデル',
    "Couldn't load the models.": 'モデルを読み込めませんでした。',
    'Search the models': 'モデルを検索',
    '{shown} of {count} models': 'モデル {count} 件中 {shown} 件',
    'No model matches.': '一致するモデルはありません。',
    'This endpoint has no model list. Type the name of the model.':
      'このエンドポイントにモデル一覧はありません。モデル名を入力してください。',
    'Reload the list on the AI page': 'AI ページで一覧を再読み込み',
    'Not in the list of this endpoint. The step fails when it starts, unless a refresh of the list finds it.':
      'このエンドポイントの一覧にありません。一覧を更新して見つからない限り、ステップは開始時に失敗します。',
    'The model "{model}" is not in the list of the endpoint "{name}". The step fails when it starts, unless a refresh of the list finds it.':
      'モデル「{model}」はエンドポイント「{name}」の一覧にありません。一覧を更新して見つからない限り、ステップは開始時に失敗します。',
    'The CLI that runs the step. Change it in the form.':
      'ステップを実行する CLI です。フォームで変更します。',
    'Model {index}': 'モデル {index}',
    'Models of the endpoint': 'エンドポイントのモデル',
    '(choose a model)': '（モデルを選択）',
    'Select an endpoint': 'エンドポイントを選択',
    'chat.completion needs an endpoint or a provider; add an endpoint on the AI page and name it here':
      'chat.completion にはエンドポイントまたはプロバイダーが必要です。AI ページで追加し、ここで名前を指定してください',
    'chat.completion needs a model; choose one that the endpoint lists':
      'chat.completion にはモデルが必要です。エンドポイントが一覧するモデルを選んでください',
    'chat.completion needs the name of an endpoint':
      'chat.completion にはエンドポイントの名前が必要です',
    'Optional. Default uses the level of the model.':
      '任意です。既定ではモデルのレベルを使います。',
    'Optional. Default uses the level of the CLI.':
      '任意です。既定では CLI のレベルを使います。',
    'agent.run needs a model; choose one that the endpoint lists':
      'agent.run にはモデルが必要です。エンドポイントが一覧するモデルを選んでください',
    '{provider} (provider)': '{provider}（プロバイダー）',
    'The provider "{provider}" is deprecated: the step calls a model API with its own address and key. Pick an endpoint: the server keeps its key.':
      'プロバイダー「{provider}」は非推奨です。ステップは独自のアドレスとキーでモデル API を呼び出します。エンドポイントを選んでください。キーはサーバーが保管します。',
    'agent.run needs an endpoint; add one on the AI page and name it here':
      'agent.run にはエンドポイントが必要です。AI ページで追加し、ここで名前を指定してください',
    'agent.run needs the name of an endpoint':
      'agent.run にはエンドポイントの名前が必要です',
    'agent.run needs a provider; it names the CLI provider':
      'agent.run にはプロバイダーが必要です。ステップを実行する CLI のプロバイダーを指定してください',
    'endpoint "{name}" does not exist; add it on the AI page or choose another':
      'エンドポイント「{name}」は存在しません。AI ページで追加するか、別のエンドポイントを選んでください',
    'endpoint must be a block with name and model':
      'endpoint は name と model を持つブロックにしてください',
    'Required. The step uses the key of the endpoint.':
      '必須です。ステップはエンドポイントのキーを使います。',
    'Search the endpoints': 'エンドポイントを検索',
    'No endpoint matches.': '一致するエンドポイントはありません。',
    Effort: '推論の強度',
  },
});
