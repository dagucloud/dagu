// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { findFrenchTextProblems } from '../frenchText';

// The problems of French texts, each keyed by its English text.
function problems(texts: Readonly<Record<string, string>>): string[] {
  return Object.entries(texts).flatMap(([english, value]) =>
    findFrenchTextProblems('t08', english, value, english)
  );
}

describe('findFrenchTextProblems', () => {
  // A time or a URL keeps its colon without a space.
  it('accepts valid French texts', () => {
    expect(
      problems({
        'Run {count} steps': 'Exécuter {count} étapes',
        'Cancel the run?': 'Annuler l’exécution\u202f?',
        'Failed: {message}': 'Échec\u00a0: {message}',
        'Delete "{name}"?': 'Supprimer «\u00a0{name}\u00a0»\u00a0?',
        'Loading…': 'Chargement…',
        Options: 'Options',
        YAML: 'YAML',
        'Starts at 12:30 on {day}': 'Démarre à 12:30 le {day}',
        'Open {path}': 'Ouvrir https://dagu.sh/{path}',
      })
    ).toEqual([]);
  });

  // A call site joins these texts to other text, so they keep the spaces at
  // their ends, and a no-break space may open or close them next to high
  // punctuation or a guillemet.
  it('accepts fragments that keep the spaces at their ends', () => {
    expect(
      problems({
        'Queued ': 'En file d’attente ',
        ' (custom)': ' (personnalisé)',
        '? This cannot be undone.': '\u00a0? Cette action est irréversible.',
        'Add "': 'Ajouter «\u00a0',
        '" will be removed.': '\u00a0» sera supprimé.',
      })
    ).toEqual([]);
  });

  it('rejects an empty value', () => {
    expect(problems({ Run: ' ' })).toEqual(['t08: empty fr value for "Run"']);
  });

  it('rejects a text that changes the spaces at its ends', () => {
    expect(
      problems({
        'Queued ': 'En file d’attente',
        Stop: 'Arrêter ',
        Run: '\u00a0Exécuter',
      })
    ).toEqual([
      't08: fr value of "Queued " changes the spaces at its ends',
      't08: fr value of "Stop" changes the spaces at its ends',
      't08: fr value of "Run" changes the spaces at its ends',
    ]);
  });

  it('rejects a placeholder that is dropped or renamed', () => {
    expect(
      problems({
        '{count} runs': 'Plusieurs exécutions',
        'Run {name} at {time}': 'Exécuter {nom} à {time}',
      })
    ).toEqual([
      't08: fr placeholders of "{count} runs" differ from en',
      't08: fr placeholders of "Run {name} at {time}" differ from en',
    ]);
  });

  it('rejects English text that is not technical', () => {
    expect(
      problems({ 'Run now': 'Run now', 'Show YAML': 'Show YAML', DAG: 'DAG' })
    ).toEqual([
      't08: "Run now" is not translated in fr',
      't08: "Show YAML" is not translated in fr',
    ]);
  });

  // A line must not start with the punctuation, so the space before it is a
  // no-break space, and French never leaves the space out.
  it('rejects high punctuation without a no-break space before it', () => {
    expect(
      problems({
        'Failed: {message}': 'Échec : {message}',
        'Cancel the run?': 'Annuler l’exécution?',
        '? This cannot be undone.': '? Cette action est irréversible.',
        'Done!': 'Terminé\u00a0!',
      })
    ).toEqual([
      't08: no no-break space before ":" in "Failed: {message}"',
      't08: no no-break space before "?" in "Cancel the run?"',
      't08: no no-break space before "?" in "? This cannot be undone."',
    ]);
  });

  it('rejects guillemets without no-break spaces inside', () => {
    expect(
      problems({
        'Open "{name}"': 'Ouvrir «{name}»',
        'Close "{name}"': 'Fermer « {name} »',
      })
    ).toEqual([
      't08: no no-break space inside « » in "Open "{name}""',
      't08: no no-break space inside « » in "Close "{name}""',
    ]);
  });

  it('rejects straight apostrophes, English quotes and three dots', () => {
    expect(
      problems({
        "Can't run": "Impossible d'exécuter",
        'Run "{name}"': 'Exécuter "{name}"',
        'Stop “{name}”': 'Arrêter “{name}”',
        'Loading...': 'Chargement...',
      })
    ).toEqual([
      't08: straight apostrophe in "Can\'t run"; write ’',
      't08: English quotes in "Run "{name}""; write « »',
      't08: English quotes in "Stop “{name}”"; write « »',
      't08: three dots in "Loading..."; write …',
    ]);
  });
});
