// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The conditions of a step that change the flow on the canvas (continue_on,
// preconditions).
export default defineMessages({
  en: {
    'if {step} failed': 'if {step} failed',
    'if {step} succeeded': 'if {step} succeeded',
    'if {step} exits with {code}': 'if {step} exits with {code}',
    'if {step} does not exit with {code}':
      'if {step} does not exit with {code}',
    'if {step}: {test}': 'if {step}: {test}',
    'Precondition: {text}': 'Precondition: {text}',
    'continues on failure': 'continues on failure',
    'continues if skipped': 'continues if skipped',
    'continues on exit code {codes}': 'continues on exit code {codes}',
    'continues on matching output': 'continues on matching output',
    'The run goes on with the steps after this one.':
      'The run goes on with the steps after this one.',
    'The step then counts as a success.': 'The step then counts as a success.',
    'Runs only if:': 'Runs only if:',
  },
  'zh-CN': {
    'if {step} failed': '如果 {step} 失败',
    'if {step} succeeded': '如果 {step} 成功',
    'if {step} exits with {code}': '如果 {step} 的退出码是 {code}',
    'if {step} does not exit with {code}': '如果 {step} 的退出码不是 {code}',
    'if {step}: {test}': '如果 {step}：{test}',
    'Precondition: {text}': '前置条件：{text}',
    'continues on failure': '失败后继续',
    'continues if skipped': '跳过后继续',
    'continues on exit code {codes}': '退出码为 {codes} 时继续',
    'continues on matching output': '输出匹配时继续',
    'The run goes on with the steps after this one.':
      '运行会继续执行此步骤之后的步骤。',
    'The step then counts as a success.': '此步骤随后视为成功。',
    'Runs only if:': '仅在以下条件成立时运行：',
  },
  ja: {
    'if {step} failed': '{step} が失敗した場合',
    'if {step} succeeded': '{step} が成功した場合',
    'if {step} exits with {code}': '{step} の終了コードが {code} の場合',
    'if {step} does not exit with {code}':
      '{step} の終了コードが {code} 以外の場合',
    'if {step}: {test}': '{step}: {test} の場合',
    'Precondition: {text}': '事前条件: {text}',
    'continues on failure': '失敗しても続行',
    'continues if skipped': 'スキップ時も続行',
    'continues on exit code {codes}': '終了コード {codes} で続行',
    'continues on matching output': '出力が一致したら続行',
    'The run goes on with the steps after this one.':
      '実行はこのステップの後のステップへ進みます。',
    'The step then counts as a success.':
      'その場合、ステップは成功と見なされます。',
    'Runs only if:': '次の条件のときだけ実行:',
  },
  de: {
    'if {step} failed': 'wenn {step} fehlgeschlagen ist',
    'if {step} succeeded': 'wenn {step} erfolgreich war',
    'if {step} exits with {code}': 'wenn {step} mit {code} endet',
    'if {step} does not exit with {code}': 'wenn {step} nicht mit {code} endet',
    'if {step}: {test}': 'wenn {step}: {test}',
    'Precondition: {text}': 'Vorbedingung: {text}',
    'continues on failure': 'macht bei Fehler weiter',
    'continues if skipped': 'macht bei Überspringen weiter',
    'continues on exit code {codes}': 'macht bei Exit-Code {codes} weiter',
    'continues on matching output': 'macht bei passender Ausgabe weiter',
    'The run goes on with the steps after this one.':
      'Der Lauf geht mit den Schritten danach weiter.',
    'The step then counts as a success.':
      'Der Schritt gilt dann als erfolgreich.',
    'Runs only if:': 'Läuft nur, wenn:',
  },
});
