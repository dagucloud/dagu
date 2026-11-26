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
    'Retries the step up to {limit} times.':
      'Retries the step up to {limit} times.',
    'Retries the step once.': 'Retries the step once.',
    'Retries the step.': 'Retries the step.',
    'Waits {interval} between the attempts.':
      'Waits {interval} between the attempts.',
    'Waits {interval} before the second attempt, then longer each time.':
      'Waits {interval} before the second attempt, then longer each time.',
    'Retries only after the exit code {codes}.':
      'Retries only after the exit code {codes}.',
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
    'Retries the step up to {limit} times.': '此步骤最多重试 {limit} 次。',
    'Retries the step once.': '此步骤最多重试 1 次。',
    'Retries the step.': '此步骤会重试。',
    'Waits {interval} between the attempts.': '两次尝试之间等待 {interval}。',
    'Waits {interval} before the second attempt, then longer each time.':
      '第二次尝试前等待 {interval}，之后每次更长。',
    'Retries only after the exit code {codes}.':
      '仅在退出码为 {codes} 时重试。',
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
    'Retries the step up to {limit} times.':
      'このステップを最大 {limit} 回再試行します。',
    'Retries the step once.': 'このステップを最大 1 回再試行します。',
    'Retries the step.': 'このステップを再試行します。',
    'Waits {interval} between the attempts.':
      '試行の間に {interval} 待ちます。',
    'Waits {interval} before the second attempt, then longer each time.':
      '2 回目の試行の前に {interval} 待ち、その後は毎回長くなります。',
    'Retries only after the exit code {codes}.':
      '終了コードが {codes} のときだけ再試行します。',
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
    'Retries the step up to {limit} times.':
      'Wiederholt den Schritt bis zu {limit}-mal.',
    'Retries the step once.': 'Wiederholt den Schritt höchstens einmal.',
    'Retries the step.': 'Wiederholt den Schritt.',
    'Waits {interval} between the attempts.':
      'Wartet {interval} zwischen den Versuchen.',
    'Waits {interval} before the second attempt, then longer each time.':
      'Wartet {interval} vor dem zweiten Versuch, danach jedes Mal länger.',
    'Retries only after the exit code {codes}.':
      'Wiederholt nur bei Exit-Code {codes}.',
  },
  fr: {
    'if {step} failed': 'si {step} a échoué',
    'if {step} succeeded': 'si {step} a réussi',
    'if {step} exits with {code}': 'si {step} se termine avec le code {code}',
    'if {step} does not exit with {code}':
      'si {step} ne se termine pas avec le code {code}',
    'if {step}: {test}': 'si {step}\u00a0: {test}',
    'Precondition: {text}': 'Précondition\u00a0: {text}',
    'continues on failure': 'continue en cas d’échec',
    'continues if skipped': 'continue si l’étape est ignorée',
    'continues on exit code {codes}':
      'continue en cas de code de sortie {codes}',
    'continues on matching output': 'continue si la sortie correspond',
    'The run goes on with the steps after this one.':
      'L’exécution se poursuit avec les étapes suivantes.',
    'The step then counts as a success.':
      'L’étape est alors considérée comme réussie.',
    'Runs only if:': 'S’exécute uniquement si\u00a0:',
    'Retries the step up to {limit} times.':
      'Réessaie l’étape jusqu’à {limit} fois.',
    'Retries the step once.': 'Réessaie l’étape une fois.',
    'Retries the step.': 'Réessaie l’étape.',
    'Waits {interval} between the attempts.':
      'Attend {interval} entre les tentatives.',
    'Waits {interval} before the second attempt, then longer each time.':
      'Attend {interval} avant la deuxième tentative, puis plus longtemps à chaque fois.',
    'Retries only after the exit code {codes}.':
      'Réessaie uniquement en cas de code de sortie {codes}.',
  },
});
