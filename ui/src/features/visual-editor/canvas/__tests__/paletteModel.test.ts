// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { ActionCatalogEntry } from '../../contracts';
import { catalogSample } from '../../testing/fixtures';
import {
  filterCatalog,
  flattenGroups,
  groupCatalog,
  groupRanked,
  matchRank,
  MatchRank,
  rankCatalog,
  requiredKeys,
  templateYaml,
} from '../paletteModel';

const names = (entries: ActionCatalogEntry[]) => entries.map((e) => e.name);

describe('filterCatalog', () => {
  it('returns all entries for an empty query', () => {
    expect(filterCatalog(catalogSample, '')).toEqual(catalogSample);
    expect(filterCatalog(catalogSample, '   ')).toEqual(catalogSample);
  });

  it('matches the title without case', () => {
    expect(names(filterCatalog(catalogSample, 'ROUTER'))).toEqual([
      'router.route',
    ]);
  });

  it('matches the name', () => {
    expect(names(filterCatalog(catalogSample, 'http.req'))).toEqual([
      'http.request',
    ]);
  });

  it('matches the namespace', () => {
    expect(names(filterCatalog(catalogSample, 'custom'))).toEqual([
      'send-slack',
    ]);
  });

  it('matches the description', () => {
    expect(names(filterCatalog(catalogSample, 'shell'))).toEqual(['run']);
  });

  it('needs every term to match', () => {
    expect(names(filterCatalog(catalogSample, 'json filter'))).toEqual([
      'jq.filter',
    ]);
    expect(filterCatalog(catalogSample, 'json router')).toEqual([]);
  });

  it('keeps the catalog order', () => {
    const result = filterCatalog(catalogSample, 'a');
    expect(names(result)).toEqual(
      names(catalogSample.filter((e) => result.includes(e)))
    );
  });
});

describe('groupCatalog', () => {
  it('puts core first and custom last', () => {
    const groups = groupCatalog([...catalogSample].reverse());
    expect(groups.map((g) => g.namespace)).toEqual([
      'core',
      'http',
      'jq',
      'router',
      'custom',
    ]);
  });

  it('keeps the entry order in a group', () => {
    const request = catalogSample.find((e) => e.name === 'http.request');
    if (!request) {
      throw new Error('fixture changed');
    }
    const extra: ActionCatalogEntry = {
      ...request,
      name: 'http.download',
      title: 'Download',
    };
    const groups = groupCatalog([request, extra]);
    expect(names(groups[0]?.entries ?? [])).toEqual([
      'http.request',
      'http.download',
    ]);
  });

  it('returns no group for no entry', () => {
    expect(groupCatalog([])).toEqual([]);
  });
});

describe('flattenGroups', () => {
  it('lists entries in the visible order', () => {
    const flat = flattenGroups(groupCatalog([...catalogSample].reverse()));
    expect(names(flat)).toEqual([
      'run',
      'http.request',
      'jq.filter',
      'router.route',
      'send-slack',
    ]);
  });
});

// parity:test-ve-palette
describe('rankCatalog', () => {
  const make = (
    name: string,
    title: string,
    description?: string
  ): ActionCatalogEntry => ({
    name,
    namespace: name.split('.')[0] ?? name,
    title,
    description,
    source: 'builtin',
    template: { action: name },
  });
  const exact = make('log', 'Write');
  const prefix = make('log.write', 'Write a line');
  const titleWord = make('audit.append', 'Append a log entry');
  const alias = make('file.write', 'File write');
  const text = make('echo', 'Echo', 'Print a line to the log.');
  const other = make('noop', 'Nothing');
  const aliases = { logfile: ['file.write'] };

  it('ranks the exact name, a name prefix, a title word, an alias, then the text', () => {
    // The catalog order is the reverse of the rank order.
    const catalog = [other, text, alias, titleWord, prefix, exact];
    expect(names(rankCatalog(catalog, 'log', aliases))).toEqual([
      'log',
      'log.write',
      'audit.append',
      'file.write',
      'echo',
    ]);
  });

  it('gives each kind of match its rank', () => {
    expect(matchRank(exact, 'LOG', aliases)).toBe(MatchRank.exactName);
    expect(matchRank(prefix, 'log', aliases)).toBe(MatchRank.namePrefix);
    expect(matchRank(titleWord, 'log', aliases)).toBe(MatchRank.titleWord);
    expect(matchRank(alias, 'log', aliases)).toBe(MatchRank.alias);
    expect(matchRank(text, 'log', aliases)).toBe(MatchRank.text);
    expect(matchRank(other, 'log', aliases)).toBeNull();
    expect(matchRank(other, '  ', aliases)).toBeNull();
  });

  it('needs each word of the query to start a word of the title', () => {
    const request = catalogSample.find((e) => e.name === 'http.request')!;
    expect(matchRank(request, 'http req')).toBe(MatchRank.titleWord);
    expect(matchRank(request, 'req http')).toBe(MatchRank.titleWord);
    expect(matchRank(request, 'quest')).toBe(MatchRank.text);
  });

  it('finds an action by an alias only with the alias map', () => {
    expect(rankCatalog([alias], 'logf')).toEqual([]);
    expect(rankCatalog([alias], 'logf', aliases)).toEqual([alias]);
  });

  it('keeps the catalog order for entries of the same rank', () => {
    const a = make('http.get', 'Get');
    const b = make('http.post', 'Post');
    expect(names(rankCatalog([b, a], 'http'))).toEqual([
      'http.post',
      'http.get',
    ]);
  });

  it('returns all entries for an empty query', () => {
    expect(rankCatalog(catalogSample, ' ')).toEqual(catalogSample);
  });

  it('leaves filterCatalog as it was, for the canvas palette', () => {
    expect(names(filterCatalog([other, text, prefix], 'log'))).toEqual([
      'echo',
      'log.write',
    ]);
  });
});

describe('groupRanked', () => {
  it('puts the group of the best match first and keeps the entry order', () => {
    const ranked = rankCatalog(catalogSample, 'filter');
    expect(groupRanked(ranked).map((g) => g.namespace)).toEqual(['jq']);
    const run = catalogSample[0]!;
    const jq = catalogSample[2]!;
    const request = catalogSample[1]!;
    expect(
      groupRanked([jq, run, request]).map((g) => [
        g.namespace,
        names(g.entries),
      ])
    ).toEqual([
      ['jq', ['jq.filter']],
      ['core', ['run']],
      ['http', ['http.request']],
    ]);
  });
});

describe('the details of an entry', () => {
  const byName = (name: string) => catalogSample.find((e) => e.name === name)!;

  it('lists the keys that the template must have', () => {
    expect(requiredKeys(byName('run'))).toEqual(['run']);
    expect(requiredKeys(byName('http.request'))).toEqual(['method', 'url']);
    expect(requiredKeys(byName('send-slack'))).toEqual([]);
  });

  it('prints the YAML of the template', () => {
    expect(templateYaml(byName('http.request'))).toBe(
      'action: http.request\nwith:\n  method: GET\n  url: ""\n'
    );
    expect(templateYaml(byName('run'))).toBe('run: ""\n');
  });
});
