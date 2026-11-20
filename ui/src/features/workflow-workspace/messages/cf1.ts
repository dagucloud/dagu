// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The form of a chat step in the step inspector: Model, System message,
// Prompt, Output and Options.
export default defineMessages({
  en: {
    'A ${...} reference': 'A ${...} reference',
    'A conversation that starts the model. The step sends the rows in order.':
      'A conversation that starts the model. The step sends the rows in order.',
    'A name from the list, or a ${...} reference.':
      'A name from the list, or a ${...} reference.',
    'API key variable': 'API key variable',
    'Add a message': 'Add a message',
    'Add a model': 'Add a model',
    'Base URL': 'Base URL',
    'Choose a provider from the list': 'Choose a provider from the list',
    'Enter a number from 0 to 1': 'Enter a number from 0 to 1',
    'Enter a number from 0 to 2': 'Enter a number from 0 to 2',
    'Enter a whole number, 1 or more': 'Enter a whole number, 1 or more',
    'Form of the prompt': 'Form of the prompt',
    'From 0 to 1. Another way to limit how varied the answers are.':
      'From 0 to 1. Another way to limit how varied the answers are.',
    'From 0 to 2. A low value gives steadier answers.':
      'From 0 to 2. A low value gives steadier answers.',
    'How many times the model can call tools before it must answer.':
      'How many times the model can call tools before it must answer.',
    'Instructions that set the role and the rules of the model. It runs before the prompt.':
      'Instructions that set the role and the rules of the model. It runs before the prompt.',
    'Leave it empty to use the LLM settings of the DAG.':
      'Leave it empty to use the LLM settings of the DAG.',
    'Maximum number of tokens': 'Maximum number of tokens',
    'Maximum number of tool rounds': 'Maximum number of tool rounds',
    'Message {index}': 'Message {index}',
    Messages: 'Messages',
    Model: 'Model',
    Models: 'Models',
    'Name of model {index}': 'Name of model {index}',
    'One prompt': 'One prompt',
    Options: 'Options',
    'Output format': 'Output format',
    'Provider of model {index}': 'Provider of model {index}',
    'Remove model {name}': 'Remove model {name}',
    'Require a specific output format': 'Require a specific output format',
    'Role of {name}': 'Role of {name}',
    'Stream the answer': 'Stream the answer',
    'System message': 'System message',
    Temperature: 'Temperature',
    'Text of {name}': 'Text of {name}',
    'The endpoint of the server. It replaces the default one.':
      'The endpoint of the server. It replaces the default one.',
    'Endpoint of model {index}': 'Endpoint of model {index}',
    'Optional. The endpoint gives the provider type and the key.':
      'Optional. The endpoint gives the provider type and the key.',
    'An endpoint holds its own base URL. Remove this key, or remove the endpoint.':
      'An endpoint holds its own base URL. Remove this key, or remove the endpoint.',
    'An endpoint holds its own key. Remove this key, or remove the endpoint.':
      'An endpoint holds its own key. Remove this key, or remove the endpoint.',
    'The first model runs first. The next ones run when it fails.':
      'The first model runs first. The next ones run when it fails.',
    'The longest answer that the model can write.':
      'The longest answer that the model can write.',
    'The model answers in the shape of a JSON schema, written as YAML. Only the listed properties are published.':
      'The model answers in the shape of a JSON schema, written as YAML. Only the listed properties are published.',
    'The name of the environment variable that holds the API key.':
      'The name of the environment variable that holds the API key.',
    'The name of the model. It can be a ${...} reference.':
      'The name of the model. It can be a ${...} reference.',
    'The output format must be a YAML mapping.':
      'The output format must be a YAML mapping.',
    'The request that you send to the model. Use + to insert a value from another step.':
      'The request that you send to the model. Use + to insert a value from another step.',
    'This step has a prompt and messages. The step uses the prompt, not the messages.':
      'This step has a prompt and messages. The step uses the prompt, not the messages.',
    'Top P': 'Top P',
    'Write the answer to the output while the model produces it.':
      'Write the answer to the output while the model produces it.',
  },
  'zh-CN': {
    'A ${...} reference': '${...} 引用',
    'A conversation that starts the model. The step sends the rows in order.':
      '用于启动模型的对话。步骤按顺序发送各行。',
    'A name from the list, or a ${...} reference.':
      '列表中的名称，或 ${...} 引用。',
    'API key variable': 'API 密钥变量',
    'Add a message': '添加消息',
    'Add a model': '添加模型',
    'Base URL': '基础 URL',
    'Choose a provider from the list': '从列表中选择提供方',
    'Enter a number from 0 to 1': '请输入 0 到 1 之间的数字',
    'Enter a number from 0 to 2': '请输入 0 到 2 之间的数字',
    'Enter a whole number, 1 or more': '请输入大于等于 1 的整数',
    'Form of the prompt': '提示词的形式',
    'From 0 to 1. Another way to limit how varied the answers are.':
      '0 到 1。限制回答多样性的另一种方式。',
    'From 0 to 2. A low value gives steadier answers.':
      '0 到 2。较低的值让回答更稳定。',
    'How many times the model can call tools before it must answer.':
      '模型必须回答之前最多可调用工具的轮数。',
    'Instructions that set the role and the rules of the model. It runs before the prompt.':
      '设定模型角色和规则的指令。它先于提示词生效。',
    'Leave it empty to use the LLM settings of the DAG.':
      '留空则使用 DAG 的 LLM 设置。',
    'Maximum number of tokens': '最大令牌数',
    'Maximum number of tool rounds': '工具调用最大轮数',
    'Message {index}': '消息 {index}',
    Messages: '消息',
    Model: '模型',
    Models: '模型列表',
    'Name of model {index}': '模型 {index} 的名称',
    'One prompt': '单个提示词',
    Options: '选项',
    'Output format': '输出格式',
    'Provider of model {index}': '模型 {index} 的提供方',
    'Remove model {name}': '移除模型 {name}',
    'Require a specific output format': '要求特定的输出格式',
    'Role of {name}': '{name} 的角色',
    'Stream the answer': '流式输出回答',
    'System message': '系统消息',
    Temperature: '温度',
    'Text of {name}': '{name} 的内容',
    'The endpoint of the server. It replaces the default one.':
      '服务器的端点。它会替换默认端点。',
    'Endpoint of model {index}': '模型 {index} 的端点',
    'Optional. The endpoint gives the provider type and the key.':
      '可选。端点提供提供方类型和密钥。',
    'An endpoint holds its own base URL. Remove this key, or remove the endpoint.':
      '端点自带基础 URL。请删除此键，或删除端点。',
    'An endpoint holds its own key. Remove this key, or remove the endpoint.':
      '端点自带密钥。请删除此键，或删除端点。',
    'The first model runs first. The next ones run when it fails.':
      '第一个模型先运行。失败时运行后面的模型。',
    'The longest answer that the model can write.': '模型可写的最长回答。',
    'The model answers in the shape of a JSON schema, written as YAML. Only the listed properties are published.':
      '模型按 JSON Schema 的形状回答，以 YAML 书写。只发布列出的属性。',
    'The name of the environment variable that holds the API key.':
      '保存 API 密钥的环境变量名称。',
    'The name of the model. It can be a ${...} reference.':
      '模型名称。可以是 ${...} 引用。',
    'The output format must be a YAML mapping.': '输出格式必须是 YAML 映射。',
    'The request that you send to the model. Use + to insert a value from another step.':
      '发送给模型的请求。用 + 插入其他步骤的值。',
    'This step has a prompt and messages. The step uses the prompt, not the messages.':
      '此步骤同时有提示词和消息。步骤使用提示词，不使用消息。',
    'Top P': 'Top P（核采样）',
    'Write the answer to the output while the model produces it.':
      '模型生成回答的同时将其写入输出。',
  },
  ja: {
    'A ${...} reference': '${...} 参照',
    'A conversation that starts the model. The step sends the rows in order.':
      'モデルを開始する会話です。ステップは行を順番に送ります。',
    'A name from the list, or a ${...} reference.':
      '一覧の名前、または ${...} 参照です。',
    'API key variable': 'API キーの変数',
    'Add a message': 'メッセージを追加',
    'Add a model': 'モデルを追加',
    'Base URL': 'ベース URL',
    'Choose a provider from the list': '一覧からプロバイダーを選ぶ',
    'Enter a number from 0 to 1': '0 から 1 の数値を入力してください',
    'Enter a number from 0 to 2': '0 から 2 の数値を入力してください',
    'Enter a whole number, 1 or more': '1 以上の整数を入力してください',
    'Form of the prompt': 'プロンプトの形式',
    'From 0 to 1. Another way to limit how varied the answers are.':
      '0 から 1。回答の多様さを制限する別の方法です。',
    'From 0 to 2. A low value gives steadier answers.':
      '0 から 2。小さい値ほど回答が安定します。',
    'How many times the model can call tools before it must answer.':
      'モデルが回答するまでにツールを呼べる回数です。',
    'Instructions that set the role and the rules of the model. It runs before the prompt.':
      'モデルの役割とルールを定める指示です。プロンプトより先に使われます。',
    'Leave it empty to use the LLM settings of the DAG.':
      '空のままにすると DAG の LLM 設定を使います。',
    'Maximum number of tokens': '最大トークン数',
    'Maximum number of tool rounds': 'ツール呼び出しの最大回数',
    'Message {index}': 'メッセージ {index}',
    Messages: 'メッセージ',
    Model: 'モデル',
    Models: 'モデル一覧',
    'Name of model {index}': 'モデル {index} の名前',
    'One prompt': '単一のプロンプト',
    Options: 'オプション',
    'Output format': '出力形式',
    'Provider of model {index}': 'モデル {index} のプロバイダー',
    'Remove model {name}': 'モデル {name} を削除',
    'Require a specific output format': '特定の出力形式を必須にする',
    'Role of {name}': '{name} のロール',
    'Stream the answer': '回答をストリーミングする',
    'System message': 'システムメッセージ',
    Temperature: '温度',
    'Text of {name}': '{name} のテキスト',
    'The endpoint of the server. It replaces the default one.':
      'サーバーのエンドポイントです。既定のものを置き換えます。',
    'Endpoint of model {index}': 'モデル {index} のエンドポイント',
    'Optional. The endpoint gives the provider type and the key.':
      '任意です。エンドポイントがプロバイダーの種類とキーを与えます。',
    'An endpoint holds its own base URL. Remove this key, or remove the endpoint.':
      'エンドポイントは自分のベース URL を持ちます。このキーかエンドポイントを削除してください。',
    'An endpoint holds its own key. Remove this key, or remove the endpoint.':
      'エンドポイントは自分のキーを持ちます。このキーかエンドポイントを削除してください。',
    'The first model runs first. The next ones run when it fails.':
      '最初のモデルが先に実行されます。失敗すると次のモデルが実行されます。',
    'The longest answer that the model can write.':
      'モデルが書ける最長の回答です。',
    'The model answers in the shape of a JSON schema, written as YAML. Only the listed properties are published.':
      'モデルは YAML で書いた JSON スキーマの形で回答します。列挙したプロパティだけが公開されます。',
    'The name of the environment variable that holds the API key.':
      'API キーを保持する環境変数の名前です。',
    'The name of the model. It can be a ${...} reference.':
      'モデルの名前です。${...} 参照も使えます。',
    'The output format must be a YAML mapping.':
      '出力形式は YAML のマッピングである必要があります。',
    'The request that you send to the model. Use + to insert a value from another step.':
      'モデルに送る依頼です。+ で他のステップの値を挿入できます。',
    'This step has a prompt and messages. The step uses the prompt, not the messages.':
      'このステップにはプロンプトとメッセージの両方があります。ステップはプロンプトを使い、メッセージは使いません。',
    'Top P': 'Top P（核サンプリング）',
    'Write the answer to the output while the model produces it.':
      'モデルが回答を生成する間、出力へ書き込みます。',
  },
});
