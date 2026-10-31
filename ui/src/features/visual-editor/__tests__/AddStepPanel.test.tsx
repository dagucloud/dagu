// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AddStepPanel, buildSections } from '../AddStepPanel';
import type { ActionCatalogEntry } from '../contracts';

const entry = (
  name: string,
  title: string,
  namespace: string,
  description?: string
): ActionCatalogEntry => ({
  name,
  title,
  namespace,
  description,
  source: namespace === 'core' ? 'core' : 'builtin',
  template: name === 'run' ? { run: '' } : { action: name },
});

const catalog = [
  entry('run', 'Shell command', 'core', 'Run a command in a shell.'),
  entry('dag.run', 'Run a sub-workflow', 'dag', 'Run another workflow.'),
  entry('http.request', 'HTTP request', 'http', 'Call a URL.'),
  entry('jq.filter', 'jq filter', 'jq', 'Filter JSON with jq.'),
  entry('archive.create', 'Create an archive', 'archive', 'Pack files.'),
];

function setup(props: Partial<React.ComponentProps<typeof AddStepPanel>> = {}) {
  const onPick = vi.fn();
  const onClose = vi.fn();
  const view = render(
    <AddStepPanel
      catalog={catalog}
      targetLabel="After fetch"
      recent={[]}
      onPick={onPick}
      onClose={onClose}
      {...props}
    />
  );
  const search = screen.getByRole('combobox', { name: 'Search actions' });
  return { onPick, onClose, search, unmount: view.unmount };
}

const TITLES = { recent: 'Recently used', popular: 'Popular' };

// parity:add-step-entry-points parity:add-step-panel parity:action-catalog
describe('buildSections', () => {
  // An action shows once: the popular ones leave their namespace.
  it('lists the popular actions, then the namespaces of the others', () => {
    const sections = buildSections(catalog, '', [], TITLES);
    expect(sections.map((s) => s.id)).toEqual(['popular', 'archive']);
    expect(sections[0]?.entries.map((e) => e.name)).toEqual([
      'run',
      'http.request',
      'dag.run',
      'jq.filter',
    ]);
  });

  it('puts the actions used lately first, and skips those that are gone', () => {
    const sections = buildSections(
      catalog,
      '',
      ['http.request', 'gone', 'run'],
      TITLES
    );
    expect(sections[0]).toMatchObject({ id: 'recent', title: 'Recently used' });
    expect(sections[1]?.id).toBe('popular');
    expect(sections[0]?.entries.map((e) => e.name)).toEqual([
      'http.request',
      'run',
    ]);
  });

  it('shows no recent section while the user searches', () => {
    const sections = buildSections(catalog, 'json', ['run'], TITLES);
    expect(sections.map((s) => s.id)).toEqual(['jq']);
  });
});

describe('AddStepPanel', () => {
  it('says where the step goes and gives focus to the search', () => {
    const { search } = setup();

    expect(
      screen.getByRole('heading', { name: 'Add a step' })
    ).toBeInTheDocument();
    expect(screen.getByText('After fetch')).toBeInTheDocument();
    expect(search).toHaveFocus();
  });

  it('shows each action with its title, description and name', () => {
    setup();
    const option = screen.getByRole('option', { name: /HTTP request/ });

    expect(within(option!).getByText('Call a URL.')).toBeInTheDocument();
    expect(within(option!).getByText('http.request')).toBeInTheDocument();
    expect(screen.getAllByRole('option')).toHaveLength(5);
  });

  // axe nested-interactive and duplicate option names.
  it('lists an action once and holds no control in an option', () => {
    setup();
    expect(
      screen.getAllByRole('option', { name: /Shell command/ })
    ).toHaveLength(1);
    for (const option of screen.getAllByRole('option')) {
      expect(option.querySelector('button, a, input, [tabindex]')).toBeNull();
    }
  });

  // A catalog text that is missing or repeats the action name gives no
  // information: a short line for the popular action replaces it.
  it('replaces a description that repeats the action name', () => {
    setup({
      catalog: [
        entry('http.request', 'HTTP request', 'http', 'http.request'),
        entry('log.write', 'Log write', 'log'),
        entry('custom.thing', 'Thing', 'custom', 'A custom thing.'),
        entry('custom.bare', 'Bare', 'custom'),
      ],
    });
    const http = screen.getAllByRole('option', { name: /HTTP request/ })[0]!;
    expect(
      within(http).getByText('Call a URL and keep the response.')
    ).toBeInTheDocument();
    const log = screen.getAllByRole('option', { name: /Log write/ })[0]!;
    expect(
      within(log).getByText('Write a message to the log.')
    ).toBeInTheDocument();
    // One line each, with the full text in the title.
    const line = within(log).getByText('Write a message to the log.');
    expect(line).toHaveClass('truncate');
    expect(line).toHaveAttribute('title', 'Write a message to the log.');
    const bare = screen.getByRole('option', { name: /Bare/ });
    expect(within(bare).getAllByText('custom.bare')).toHaveLength(2);
  });

  it('filters by words of the title, the name or the description', () => {
    const { search } = setup();

    fireEvent.change(search, { target: { value: 'json' } });
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(
      screen.getByRole('option', { name: /jq filter/ })
    ).toBeInTheDocument();
  });

  it('moves with the arrow keys and adds with Enter', () => {
    const { search, onPick } = setup();

    fireEvent.keyDown(search, { key: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    expect(screen.getAllByRole('option')[2]).toHaveAttribute(
      'aria-selected',
      'true'
    );
    fireEvent.keyDown(search, { key: 'Enter' });
    // Popular: run, http.request, dag.run ...
    expect(onPick).toHaveBeenCalledWith(catalog[1]);
  });

  it('does not go past the first or the last action', () => {
    const { search, onPick } = setup();

    fireEvent.keyDown(search, { key: 'ArrowUp' });
    for (let i = 0; i < 10; i++) {
      fireEvent.keyDown(search, { key: 'ArrowDown' });
    }
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(catalog[4]);
  });

  it('adds the first match of a search with Enter', () => {
    const { search, onPick } = setup();

    fireEvent.change(search, { target: { value: 'http' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(catalog[2]);
  });

  it('adds an action with a click', () => {
    const { onPick } = setup();

    fireEvent.click(
      screen.getAllByRole('option', { name: /Run a sub-workflow/ })[0]!
    );
    expect(onPick).toHaveBeenCalledWith(catalog[1]);
  });

  it('closes with Escape and with the button', () => {
    const { search, onClose } = setup();

    fireEvent.keyDown(search, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('shows the actions used lately above the others', () => {
    setup({ recent: ['jq.filter'] });

    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(5);
    expect(options[0]).toHaveTextContent('jq filter');
  });

  // 'echo' is no action name: the alias leads to the shell and the log.
  it('finds the shell command and the log step for echo', () => {
    const { search } = setup({
      catalog: [...catalog, entry('log.write', 'Log write', 'log', 'Log.')],
    });
    fireEvent.change(search, { target: { value: 'echo' } });
    const names = screen.getAllByRole('option').map((o) => o.textContent);
    expect(names.some((n) => n?.includes('Shell command'))).toBe(true);
    expect(names.some((n) => n?.includes('Log write'))).toBe(true);
  });

  it('offers a shell command, or all the actions, when nothing matches', () => {
    const { search, onPick } = setup();

    fireEvent.change(search, { target: { value: 'zzzz' } });
    expect(screen.getByText('No matching actions')).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: 'Add a shell command' })
    );
    expect(onPick).toHaveBeenCalledWith(catalog[0]);

    fireEvent.click(screen.getByRole('button', { name: 'Show all actions' }));
    expect(screen.getAllByRole('option')).toHaveLength(5);
    expect(search).toHaveValue('');
  });

  it('does not pick anything on Enter when nothing matches', () => {
    const { search, onPick } = setup();

    fireEvent.change(search, { target: { value: 'zzzz' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).not.toHaveBeenCalled();
    // The footer no longer promises Enter to add.
    expect(screen.getByText(/Nothing matches/)).toBeInTheDocument();
  });
});

const FAVOURITES_KEY = 'dagu.visualEditor.favouriteActions';

describe('buildSections with favourites', () => {
  it('lists the favourites first, then the recent and popular actions', () => {
    const sections = buildSections(
      catalog,
      '',
      ['run'],
      { ...TITLES, favourites: 'Favourites' },
      { favourites: ['jq.filter', 'gone'] }
    );
    expect(sections.map((s) => s.id)).toEqual([
      'favourites',
      'recent',
      'popular',
      'archive',
    ]);
    expect(sections[0]).toMatchObject({ title: 'Favourites' });
    expect(sections[0]?.entries.map((e) => e.name)).toEqual(['jq.filter']);
  });

  it('shows no favourites section while the user searches', () => {
    const sections = buildSections(catalog, 'json', [], TITLES, {
      favourites: ['jq.filter'],
    });
    expect(sections.map((s) => s.id)).toEqual(['jq']);
  });

  it('finds an action by an alias', () => {
    const sections = buildSections(catalog, 'curl', [], TITLES, {
      aliases: { curl: ['http.request'] },
    });
    expect(sections.flatMap((s) => s.entries)).toEqual([catalog[2]]);
  });
});

describe('AddStepPanel keys', () => {
  beforeEach(() => localStorage.clear());

  it('goes to the first and the last action with Home and End', () => {
    const { search } = setup();
    const options = () => screen.getAllByRole('option');

    fireEvent.keyDown(search, { key: 'End' });
    expect(options()[4]).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(search, { key: 'Home' });
    expect(options()[0]).toHaveAttribute('aria-selected', 'true');
    expect(search).toHaveAttribute('aria-activedescendant', options()[0]!.id);
  });

  it('keeps Shift+Home and Shift+End for the text of the search', () => {
    const { search } = setup();
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'End', shiftKey: true });
    expect(screen.getAllByRole('option')[1]).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('adds and stays open with Shift+Enter', () => {
    const { search, onPick, onClose } = setup();

    fireEvent.keyDown(search, { key: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'Enter', shiftKey: true });
    expect(onPick).toHaveBeenCalledWith(catalog[2], { stay: true });
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByTestId('ve-add-step')).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole('option')[0]!, { shiftKey: true });
    expect(onPick).toHaveBeenLastCalledWith(catalog[0], { stay: true });
  });

  it('adds with Enter with no option to stay', () => {
    const { search, onPick } = setup();
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(catalog[0]);
    expect(onPick.mock.calls[0]).toHaveLength(1);
  });

  it('leaves Enter and Escape to an input method while it composes', () => {
    const { search, onPick, onClose } = setup();
    fireEvent.change(search, { target: { value: 'json' } });
    fireEvent.keyDown(search, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(search, { key: 'Escape', isComposing: true });
    expect(onPick).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(search).toHaveValue('json');
  });

  it('clears a search with Escape before it closes', () => {
    const { search, onClose } = setup();

    fireEvent.change(search, { target: { value: 'json' } });
    fireEvent.keyDown(search, { key: 'Escape' });
    expect(search).toHaveValue('');
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getAllByRole('option')).toHaveLength(5);

    fireEvent.keyDown(search, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('AddStepPanel favourites', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it('marks an action with a star and lists it first, for the next time', () => {
    const { onPick, unmount } = setup();

    const jq = screen.getByRole('option', { name: /jq filter/ });
    fireEvent.click(within(jq).getByTitle('Add to favourites'));
    expect(onPick).not.toHaveBeenCalled();
    expect(JSON.parse(localStorage.getItem(FAVOURITES_KEY) ?? '[]')).toEqual([
      'jq.filter',
    ]);
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(5);
    expect(options[0]).toHaveTextContent('jq filter');
    expect(screen.getByText('Favourites')).toBeInTheDocument();

    unmount();
    setup();
    expect(screen.getAllByRole('option')[0]).toHaveTextContent('jq filter');
  });

  // The star is no button in an option: the keyboard has its own key.
  it('toggles the favourite of the active row with Alt+Shift+F', () => {
    const { search, onPick } = setup();
    fireEvent.keyDown(search, { key: 'End' });
    fireEvent.keyDown(search, {
      key: 'F',
      code: 'KeyF',
      altKey: true,
      shiftKey: true,
    });
    expect(onPick).not.toHaveBeenCalled();
    expect(JSON.parse(localStorage.getItem(FAVOURITES_KEY) ?? '[]')).toEqual([
      'archive.create',
    ]);
    expect(screen.getAllByRole('option')[0]).toHaveTextContent('archive');
    // The active row follows the action, so the same key undoes the toggle.
    expect(screen.getByRole('option', { selected: true })).toHaveTextContent(
      'Create an archive'
    );
    fireEvent.keyDown(search, {
      key: 'F',
      code: 'KeyF',
      altKey: true,
      shiftKey: true,
    });
    expect(JSON.parse(localStorage.getItem(FAVOURITES_KEY) ?? '[]')).toEqual(
      []
    );
  });

  it('removes the star with a second click', () => {
    localStorage.setItem(FAVOURITES_KEY, '["http.request"]');
    setup();

    const [first] = screen.getAllByRole('option');
    expect(first).toHaveTextContent('HTTP request');
    fireEvent.click(within(first!).getByTitle('Remove from favourites'));
    expect(localStorage.getItem(FAVOURITES_KEY)).toBe('[]');
    expect(screen.queryByText('Favourites')).not.toBeInTheDocument();
  });

  it('works when the browser refuses its storage', () => {
    const get = vi
      .spyOn(Storage.prototype, 'getItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    const set = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    try {
      setup();
      const jq = screen.getByRole('option', { name: /jq filter/ });
      fireEvent.click(within(jq).getByTitle('Add to favourites'));
      expect(screen.getAllByRole('option')[0]).toHaveTextContent('jq filter');
    } finally {
      get.mockRestore();
      set.mockRestore();
    }
  });
});

describe('AddStepPanel details strip', () => {
  beforeEach(() => localStorage.clear());

  it('shows the required keys and the YAML of the active action', () => {
    const custom: ActionCatalogEntry = {
      name: 'send-slack',
      title: 'Send Slack message',
      namespace: 'custom',
      source: 'custom',
      template: { action: 'send-slack', with: {} },
    };
    const request: ActionCatalogEntry = {
      ...catalog[2]!,
      template: { action: 'http.request', with: { method: 'GET', url: '' } },
    };
    const { search } = setup({ catalog: [catalog[0]!, request, custom] });
    const details = () =>
      within(screen.getByRole('region', { name: 'Details of the action' }));
    // The key names show as chips and again inside the coloured YAML.
    const chip = (name: string) =>
      details()
        .getAllByText(name)
        .find((el) => el.tagName === 'CODE');

    // The shell command is first.
    expect(details().getByText('Required keys')).toBeInTheDocument();
    expect(chip('run')).toBeInTheDocument();
    expect(screen.getByTestId('ve-add-step-yaml').textContent).toBe(
      'run: ""\n'
    );

    fireEvent.keyDown(search, { key: 'ArrowDown' });
    expect(chip('method')).toBeInTheDocument();
    expect(chip('url')).toBeInTheDocument();
    expect(screen.getByTestId('ve-add-step-yaml').textContent).toBe(
      'action: http.request\nwith:\n  method: GET\n  url: ""\n'
    );

    fireEvent.change(search, { target: { value: 'slack' } });
    expect(details().getByText('No required keys')).toBeInTheDocument();
    expect(screen.getByTestId('ve-add-step-yaml').textContent).toBe(
      'action: send-slack\nwith: {}\n'
    );
  });

  // The colours match the editor theme; a star marks the required lines.
  it('colours the YAML and marks the required keys', () => {
    const request: ActionCatalogEntry = {
      ...catalog[2]!,
      template: { action: 'http.request', with: { method: 'GET', url: '' } },
    };
    const { search } = setup({ catalog: [catalog[0]!, request] });
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    const pre = screen.getByTestId('ve-add-step-yaml');
    const key = Array.from(pre.querySelectorAll('span')).find(
      (span) => span.textContent === 'url' && span.children.length === 0
    )!;
    expect(key.className).toContain('text-[#9a5d0b]');
    expect(key.className).toContain('dark:text-[#e2a566]');
    expect(
      Array.from(pre.querySelectorAll('[data-required="true"]')).map(
        (line) => line.textContent
      )
    ).toEqual(['  method: GET', '  url: ""']);
  });

  it('shows no strip when nothing matches', () => {
    const { search } = setup();
    fireEvent.change(search, { target: { value: 'zzzz' } });
    expect(
      screen.queryByRole('region', { name: 'Details of the action' })
    ).not.toBeInTheDocument();
  });
});

describe('AddStepPanel launchers', () => {
  // The launcher nodes stay on the canvas: the list offers none.
  it('lists no launcher and finds none by search', () => {
    const { search } = setup();
    expect(screen.queryByTestId(/^ve-add-launcher-/)).toBeNull();
    expect(screen.queryByText('Launchers')).toBeNull();
    for (const word of ['cron', 'schedule', 'webhook', 'manual']) {
      fireEvent.change(search, { target: { value: word } });
      expect(screen.queryByTestId(/^ve-add-launcher-/)).toBeNull();
    }
  });
});

// The canvas draws a ghost step with the choice of the list.
describe('AddStepPanel onHoverEntry', () => {
  const rowOf = (name: string) =>
    screen
      .getAllByRole('option')
      .find((row) => within(row).queryByText(name) !== null)!;

  it('reports no choice until the user moves in the list', () => {
    const onHoverEntry = vi.fn();
    setup({ onHoverEntry });
    expect(onHoverEntry).toHaveBeenLastCalledWith(null);
    expect(onHoverEntry.mock.calls.every(([e]) => e === null)).toBe(true);
  });

  it('reports the row under the pointer at once', () => {
    const onHoverEntry = vi.fn();
    setup({ onHoverEntry });
    fireEvent.mouseEnter(rowOf('HTTP request'));
    expect(onHoverEntry).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: 'http.request' })
    );
    fireEvent.mouseEnter(rowOf('jq filter'));
    expect(onHoverEntry).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: 'jq.filter' })
    );
  });

  it('reports the row of the keyboard', () => {
    const onHoverEntry = vi.fn();
    const { search } = setup({ onHoverEntry });
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    expect(onHoverEntry).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: 'http.request' })
    );
  });

  it('reports the best match of a search', () => {
    const onHoverEntry = vi.fn();
    const { search } = setup({ onHoverEntry });
    fireEvent.change(search, { target: { value: 'jq' } });
    expect(onHoverEntry).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: 'jq.filter' })
    );
  });

  it('reports null when the panel closes', () => {
    const onHoverEntry = vi.fn();
    const { unmount } = setup({ onHoverEntry });
    fireEvent.mouseEnter(rowOf('HTTP request'));
    unmount();
    expect(onHoverEntry).toHaveBeenLastCalledWith(null);
  });
});
