// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The runtime profile section of the Overview tab in the Workflow panel.
export default defineMessages({
  en: {
    'Manage profiles': 'Manage profiles',
    'A profile sets environment variables for every run of this workflow. A run can pick another profile in the Start dialog.':
      'A profile sets environment variables for every run of this workflow. A run can pick another profile in the Start dialog.',
    Applies: 'Applies',
    'This profile is disabled. A run cannot use it.':
      'This profile is disabled. A run cannot use it.',
    'The details of this profile are not available.':
      'The details of this profile are not available.',
    Variables: 'Variables',
    '{variables} variables, {secrets} secrets':
      '{variables} variables, {secrets} secrets',
    'Secret value hidden': 'Secret value hidden',
    'overrides {layers}': 'overrides {layers}',
    'Global variables are visible to administrators only.':
      'Global variables are visible to administrators only.',
    'The defaults of the workspace are not available to your role.':
      'The defaults of the workspace are not available to your role.',
    'Yes: the default profile of this workflow':
      'Yes: the default profile of this workflow',
    'Yes: the default profile of the workspace {workspace}':
      'Yes: the default profile of the workspace {workspace}',
    'No profile applies to this workflow':
      'No profile applies to this workflow',
    'Global environment': 'Global environment',
    'Workspace defaults': 'Workspace defaults',
  },
  'zh-CN': {
    'Manage profiles': '管理配置档案',
    'A profile sets environment variables for every run of this workflow. A run can pick another profile in the Start dialog.':
      '配置档案会为此工作流的每次运行设置环境变量。运行时可在启动对话框中选择其他配置档案。',
    Applies: '是否生效',
    'This profile is disabled. A run cannot use it.':
      '此配置档案已停用，运行无法使用它。',
    'The details of this profile are not available.':
      '无法获取此配置档案的详细信息。',
    Variables: '变量',
    '{variables} variables, {secrets} secrets':
      '{variables} 个变量，{secrets} 个密钥',
    'Secret value hidden': '密钥值已隐藏',
    'overrides {layers}': '覆盖 {layers}',
    'Global variables are visible to administrators only.':
      '全局变量仅管理员可见。',
    'The defaults of the workspace are not available to your role.':
      '你的角色无法查看工作区的默认值。',
    'Yes: the default profile of this workflow': '是：此工作流的默认配置档案',
    'Yes: the default profile of the workspace {workspace}':
      '是：工作区 {workspace} 的默认配置档案',
    'No profile applies to this workflow': '此工作流没有生效的配置档案',
    'Global environment': '全局环境',
    'Workspace defaults': '工作区默认值',
  },
  ja: {
    'Manage profiles': 'プロファイルを管理',
    'A profile sets environment variables for every run of this workflow. A run can pick another profile in the Start dialog.':
      'プロファイルは、このワークフローの実行ごとに環境変数を設定します。実行時に開始ダイアログで別のプロファイルを選べます。',
    Applies: '適用',
    'This profile is disabled. A run cannot use it.':
      'このプロファイルは無効です。実行では使えません。',
    'The details of this profile are not available.':
      'このプロファイルの詳細を取得できません。',
    Variables: '変数',
    '{variables} variables, {secrets} secrets':
      '変数 {variables} 件、シークレット {secrets} 件',
    'Secret value hidden': 'シークレットの値は非表示です',
    'overrides {layers}': '{layers} を上書き',
    'Global variables are visible to administrators only.':
      'グローバル変数は管理者のみ表示されます。',
    'The defaults of the workspace are not available to your role.':
      'ワークスペースの既定値は、あなたのロールでは表示できません。',
    'Yes: the default profile of this workflow':
      'はい：このワークフローの既定プロファイル',
    'Yes: the default profile of the workspace {workspace}':
      'はい：ワークスペース {workspace} の既定プロファイル',
    'No profile applies to this workflow':
      'このワークフローに適用されるプロファイルはありません',
    'Global environment': 'グローバル環境',
    'Workspace defaults': 'ワークスペースの既定値',
  },
  de: {
    'Manage profiles': 'Profile verwalten',
    'A profile sets environment variables for every run of this workflow. A run can pick another profile in the Start dialog.':
      'Ein Profil setzt Umgebungsvariablen für jeden Lauf dieses Ablaufs. Ein Lauf kann im Start-Dialog ein anderes Profil wählen.',
    Applies: 'Gilt',
    'This profile is disabled. A run cannot use it.':
      'Dieses Profil ist abgeschaltet. Ein Lauf kann es nicht verwenden.',
    'The details of this profile are not available.':
      'Die Details dieses Profils sind nicht verfügbar.',
    Variables: 'Variablen',
    '{variables} variables, {secrets} secrets':
      '{variables} Variablen, {secrets} Geheimnisse',
    'Secret value hidden': 'Geheimer Wert verborgen',
    'overrides {layers}': 'überschreibt {layers}',
    'Global variables are visible to administrators only.':
      'Globale Variablen sind nur für Administratoren sichtbar.',
    'The defaults of the workspace are not available to your role.':
      'Die Vorgaben des Arbeitsbereichs sind für deine Rolle nicht verfügbar.',
    'Yes: the default profile of this workflow':
      'Ja: das Standardprofil dieses Ablaufs',
    'Yes: the default profile of the workspace {workspace}':
      'Ja: das Standardprofil des Arbeitsbereichs {workspace}',
    'No profile applies to this workflow': 'Für diesen Ablauf gilt kein Profil',
    'Global environment': 'Globale Umgebung',
    'Workspace defaults': 'Vorgaben des Arbeitsbereichs',
  },
  fr: {
    'Manage profiles': 'Gérer les profils',
    'A profile sets environment variables for every run of this workflow. A run can pick another profile in the Start dialog.':
      'Un profil définit des variables d’environnement pour chaque exécution de ce workflow. Une exécution peut choisir un autre profil dans la boîte de dialogue de démarrage.',
    Applies: 'S’applique',
    'This profile is disabled. A run cannot use it.':
      'Ce profil est désactivé. Aucune exécution ne peut l’utiliser.',
    'The details of this profile are not available.':
      'Les détails de ce profil ne sont pas disponibles.',
    Variables: 'Variables',
    '{variables} variables, {secrets} secrets':
      'Variables\u00a0: {variables}, secrets\u00a0: {secrets}',
    'Secret value hidden': 'Valeur secrète masquée',
    'overrides {layers}': 'remplace {layers}',
    'Global variables are visible to administrators only.':
      'Les variables globales ne sont visibles que par les administrateurs.',
    'The defaults of the workspace are not available to your role.':
      'Les valeurs par défaut de l’espace de travail ne sont pas accessibles à votre rôle.',
    'Yes: the default profile of this workflow':
      'Oui\u00a0: le profil par défaut de ce workflow',
    'Yes: the default profile of the workspace {workspace}':
      'Oui\u00a0: le profil par défaut de l’espace de travail {workspace}',
    'No profile applies to this workflow':
      'Aucun profil ne s’applique à ce workflow',
    'Global environment': 'Environnement global',
    'Workspace defaults': 'Valeurs par défaut de l’espace de travail',
  },
});
