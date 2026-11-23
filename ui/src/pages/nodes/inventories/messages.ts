// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

/** The static messages of the Nodes page's Inventories view. */
export const inventoryMessages = defineMessages({
  en: {
    Inventories: 'Inventories',
    'New inventory': 'New inventory',
    'Edit inventory': 'Edit inventory',
    'Edit JSON': 'Edit JSON',
    'Delete inventory': 'Delete inventory',
    'Delete {name} and its {count} nodes? Their trusted host keys are forgotten, and steps no longer match them.':
      'Delete {name} and its {count} nodes? Their trusted host keys are forgotten, and steps no longer match them.',
    "Couldn't read the inventory.": "Couldn't read the inventory.",
    'Defaults fill the fields a node leaves unset. Credentials are key paths and secret refs, never values.':
      'Defaults fill the fields a node leaves unset. Credentials are key paths and secret refs, never values.',
    'Default user': 'Default user',
    'Default port': 'Default port',
    'Default labels': 'Default labels',
    'Refuse host keys that are neither pinned nor trusted':
      'Refuse host keys that are neither pinned nor trusted',
    'The default port must be a whole number.':
      'The default port must be a whole number.',
    'Give a key file or a key secret, not both.':
      'Give a key file or a key secret, not both.',
    'Someone changed this inventory after you opened it. Load it as it is now, then make your change again.':
      'Someone changed this inventory after you opened it. Load it as it is now, then make your change again.',
    'Load current version': 'Load current version',
    'Edit {name} as JSON': 'Edit {name} as JSON',
    'The record that dagu nodes export prints. Removing or renaming a node forgets its trusted host key.':
      'The record that dagu nodes export prints. Removing or renaming a node forgets its trusted host key.',
    'Inventory JSON': 'Inventory JSON',
    'The text is not valid JSON.': 'The text is not valid JSON.',
    'An inventory is one JSON object.': 'An inventory is one JSON object.',
    'The server tests the connection before the node is added. An unknown host key is trusted only once the node is added.':
      'The server tests the connection before the node is added. An unknown host key is trusted only once the node is added.',
    'The host key of {hop} {node} is unknown: {fingerprint}':
      'The host key of {hop} {node} is unknown: {fingerprint}',
    'Trust this key and test again': 'Trust this key and test again',
    'Add without testing': 'Add without testing',
    'The port must be a whole number.': 'The port must be a whole number.',
  },
  'zh-CN': {
    Inventories: '清单',
    'New inventory': '新建清单',
    'Edit inventory': '编辑清单',
    'Edit JSON': '编辑 JSON',
    'Delete inventory': '删除清单',
    'Delete {name} and its {count} nodes? Their trusted host keys are forgotten, and steps no longer match them.':
      '删除 {name} 及其 {count} 个节点？它们已信任的主机密钥将被清除，步骤也不再匹配它们。',
    "Couldn't read the inventory.": '无法读取清单。',
    'Defaults fill the fields a node leaves unset. Credentials are key paths and secret refs, never values.':
      '默认值会填充节点未设置的字段。凭据为密钥路径和密钥引用，绝不是明文值。',
    'Default user': '默认用户',
    'Default port': '默认端口',
    'Default labels': '默认标签',
    'Refuse host keys that are neither pinned nor trusted':
      '拒绝既未固定也未信任的主机密钥',
    'The default port must be a whole number.': '默认端口必须是整数。',
    'Give a key file or a key secret, not both.':
      '请提供密钥文件或密钥引用，不能同时提供。',
    'Someone changed this inventory after you opened it. Load it as it is now, then make your change again.':
      '你打开此清单后，有人修改了它。请加载当前版本，然后重新进行修改。',
    'Load current version': '加载当前版本',
    'Edit {name} as JSON': '以 JSON 编辑 {name}',
    'The record that dagu nodes export prints. Removing or renaming a node forgets its trusted host key.':
      '即 dagu nodes export 输出的记录。删除或重命名节点会清除其已信任的主机密钥。',
    'Inventory JSON': '清单 JSON',
    'The text is not valid JSON.': '文本不是有效的 JSON。',
    'An inventory is one JSON object.': '一个清单是一个 JSON 对象。',
    'The server tests the connection before the node is added. An unknown host key is trusted only once the node is added.':
      '添加节点前，服务器会测试连接。未知的主机密钥只在节点添加后才会被信任。',
    'The host key of {hop} {node} is unknown: {fingerprint}':
      '{hop} {node} 的主机密钥未知：{fingerprint}',
    'Trust this key and test again': '信任此密钥并重新测试',
    'Add without testing': '不测试直接添加',
    'The port must be a whole number.': '端口必须是整数。',
  },
  ja: {
    Inventories: 'インベントリ',
    'New inventory': '新しいインベントリ',
    'Edit inventory': 'インベントリを編集',
    'Edit JSON': 'JSON を編集',
    'Delete inventory': 'インベントリを削除',
    'Delete {name} and its {count} nodes? Their trusted host keys are forgotten, and steps no longer match them.':
      '{name} とその {count} 個のノードを削除しますか？信頼済みのホスト鍵は破棄され、ステップはこれらのノードに一致しなくなります。',
    "Couldn't read the inventory.": 'インベントリを読み込めませんでした。',
    'Defaults fill the fields a node leaves unset. Credentials are key paths and secret refs, never values.':
      'デフォルトはノードが設定していない項目を補います。認証情報は鍵のパスとシークレット参照で、値そのものは保存しません。',
    'Default user': 'デフォルトのユーザー',
    'Default port': 'デフォルトのポート',
    'Default labels': 'デフォルトのラベル',
    'Refuse host keys that are neither pinned nor trusted':
      '固定も信頼もされていないホスト鍵を拒否する',
    'The default port must be a whole number.':
      'デフォルトのポートは整数で指定してください。',
    'Give a key file or a key secret, not both.':
      '鍵ファイルか鍵のシークレットのどちらか一方を指定してください。',
    'Someone changed this inventory after you opened it. Load it as it is now, then make your change again.':
      '開いた後にこのインベントリが変更されました。現在の内容を読み込んでから、もう一度変更してください。',
    'Load current version': '現在の内容を読み込む',
    'Edit {name} as JSON': '{name} を JSON で編集',
    'The record that dagu nodes export prints. Removing or renaming a node forgets its trusted host key.':
      'dagu nodes export が出力するレコードです。ノードを削除または名前変更すると、信頼済みのホスト鍵は破棄されます。',
    'Inventory JSON': 'インベントリの JSON',
    'The text is not valid JSON.': '有効な JSON ではありません。',
    'An inventory is one JSON object.':
      'インベントリは 1 つの JSON オブジェクトです。',
    'The server tests the connection before the node is added. An unknown host key is trusted only once the node is added.':
      'ノードを追加する前に、サーバーが接続をテストします。不明なホスト鍵は、ノードを追加した後にのみ信頼されます。',
    'The host key of {hop} {node} is unknown: {fingerprint}':
      '{hop} {node} のホスト鍵は不明です：{fingerprint}',
    'Trust this key and test again': 'この鍵を信頼して再テスト',
    'Add without testing': 'テストせずに追加',
    'The port must be a whole number.': 'ポートは整数で指定してください。',
  },
});
