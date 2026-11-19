// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The agent step in the step editor: the untrusted mark, and the keys
// `untrusted` and `secrets` of a step.
export default defineMessages({
  en: {
    Untrusted: 'Untrusted',
    'Untrusted step: it gets no secret and no DAGU_ variable.':
      'Untrusted step: it gets no secret and no DAGU_ variable.',
    'A step with a CLI provider is always untrusted.':
      'A step with a CLI provider is always untrusted.',
    'The step gets no secret and no DAGU_ variable.':
      'The step gets no secret and no DAGU_ variable.',
    'Untrusted by default: the DAG has an agent step.':
      'Untrusted by default: the DAG has an agent step.',
    'Trusted: the key untrusted is false.':
      'Trusted: the key untrusted is false.',
    'step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets':
      'step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets',
    'restart_policy cannot be combined with schedule':
      'restart_policy cannot be combined with schedule',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy cannot be combined with retry_policy',
    'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration':
      'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration',
    Security: 'Security',
    'No secret yet': 'No secret yet',
    'Remove secret {name}': 'Remove secret {name}',
    'Add a secret': 'Add a secret',
    'Once a step lists secrets, each step gets only the secrets that it lists.':
      'Once a step lists secrets, each step gets only the secrets that it lists.',
  },
  'zh-CN': {
    Untrusted: '不受信任',
    'Untrusted step: it gets no secret and no DAGU_ variable.':
      '不受信任的步骤：它不获得任何密钥，也不获得 DAGU_ 变量。',
    'A step with a CLI provider is always untrusted.':
      '使用 CLI 提供方的步骤始终不受信任。',
    'The step gets no secret and no DAGU_ variable.':
      '该步骤不获得任何密钥，也不获得 DAGU_ 变量。',
    'Untrusted by default: the DAG has an agent step.':
      '默认不受信任：该 DAG 有智能体步骤。',
    'Trusted: the key untrusted is false.': '受信任：键 untrusted 为 false。',
    'step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets':
      '步骤列出了密钥，但在含有 agent.run 步骤的 DAG 中默认不受信任；请添加 untrusted: false 以便以进程用户身份运行，或删除 secrets',
    'restart_policy cannot be combined with schedule':
      'restart_policy 不能与 schedule 同时使用',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy 不能与 retry_policy 同时使用',
    'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration':
      '缺少 restart_wait_sec：请在文件中将其设为 1 或更大，或在基础配置中设置',
    Security: '安全',
    'No secret yet': '尚无密钥',
    'Remove secret {name}': '移除密钥 {name}',
    'Add a secret': '添加密钥',
    'Once a step lists secrets, each step gets only the secrets that it lists.':
      '一旦某个步骤列出密钥，每个步骤只获得其所列出的密钥。',
  },
  ja: {
    Untrusted: '信頼されていません',
    'Untrusted step: it gets no secret and no DAGU_ variable.':
      '信頼されないステップ: シークレットも DAGU_ 変数も受け取りません。',
    'A step with a CLI provider is always untrusted.':
      'CLI プロバイダーのステップは常に信頼されません。',
    'The step gets no secret and no DAGU_ variable.':
      'このステップはシークレットも DAGU_ 変数も受け取りません。',
    'Untrusted by default: the DAG has an agent step.':
      '既定で信頼されません: この DAG にはエージェントステップがあります。',
    'Trusted: the key untrusted is false.':
      '信頼されます: キー untrusted は false です。',
    'step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets':
      'ステップが secrets を列挙していますが、agent.run ステップを持つ DAG では既定で信頼されません。プロセスのユーザーとして実行するには untrusted: false を追加するか、secrets を削除してください',
    'restart_policy cannot be combined with schedule':
      'restart_policy は schedule と併用できません',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy は retry_policy と併用できません',
    'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration':
      'restart_wait_sec がありません。ファイルで 1 以上を設定するか、ベース設定で設定してください',
    Security: 'セキュリティ',
    'No secret yet': 'シークレットはまだありません',
    'Remove secret {name}': 'シークレット {name} を削除',
    'Add a secret': 'シークレットを追加',
    'Once a step lists secrets, each step gets only the secrets that it lists.':
      'いずれかのステップがシークレットを列挙すると、各ステップは列挙されたシークレットだけを受け取ります。',
  },
});
