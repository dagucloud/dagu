// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReferenceCandidate, ReferenceInputProps } from '../../contracts';
import { ReferenceInput } from '../ReferenceInput';

// The layer of a highlighted field ends with a zero-width space, so that a
// last line break takes a line.
const END = String.fromCharCode(0x200b);

const candidates: ReferenceCandidate[] = [
  {
    token: '${params.API_URL}',
    label: 'API_URL',
    group: 'params',
    known: 'static',
  },
  {
    token: '${params.PAGE}',
    label: 'PAGE',
    group: 'params',
    known: 'static',
  },
  {
    token: '${steps.fetch.outputs.body}',
    label: 'fetch body',
    group: 'steps',
    stepName: 'fetch',
    distance: 1,
    known: 'runtime',
  },
];

// A host that keeps the value, as the inspector does.
function Controlled({
  initial,
  onValue,
  ...rest
}: {
  initial: string;
  onValue?(value: string): void;
} & Partial<ReferenceInputProps>) {
  const [value, setValue] = useState(initial);
  return (
    <ReferenceInput
      value={value}
      onChange={(next) => {
        setValue(next);
        onValue?.(next);
      }}
      candidates={candidates}
      aria-label="field"
      {...rest}
    />
  );
}

function field() {
  return screen.getByLabelText('field') as
    | HTMLInputElement
    | HTMLTextAreaElement;
}

function focusAt(
  el: HTMLInputElement | HTMLTextAreaElement,
  start: number,
  end = start
) {
  act(() => {
    el.focus();
    el.setSelectionRange(start, end);
  });
  fireEvent.select(el);
}

describe('ReferenceInput', () => {
  describe('a candidate that wants to know', () => {
    const onPicked = vi.fn();
    const publishable: ReferenceCandidate = {
      token: '${steps.a.outputs.stdout}',
      label: 'a.stdout',
      group: 'steps',
      known: 'runtime',
      publish: { step: 'steps[0]' },
      onPicked,
    };

    beforeEach(() => onPicked.mockClear());

    it('hears a pick from typing', () => {
      render(<Controlled initial="" candidates={[publishable]} />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '${' } });
      fireEvent.keyDown(el, { key: 'Enter' });
      expect(onPicked).toHaveBeenCalledTimes(1);
      expect(el).toHaveValue('${steps.a.outputs.stdout}');
    });

    it('hears a pick from the button', () => {
      render(<Controlled initial="" candidates={[publishable]} />);
      fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
      fireEvent.click(screen.getByRole('option', { name: /a\.stdout/ }));
      expect(onPicked).toHaveBeenCalledTimes(1);
    });

    it('hears nothing when the list only closes', () => {
      render(<Controlled initial="" candidates={[publishable]} />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '${' } });
      fireEvent.keyDown(el, { key: 'Escape' });
      expect(onPicked).not.toHaveBeenCalled();
    });
  });

  it('renders an input and reports changes', () => {
    const onChange = vi.fn();
    render(
      <ReferenceInput
        value="a"
        onChange={onChange}
        candidates={[]}
        placeholder="ph"
        id="f"
        aria-label="field"
      />
    );
    const el = screen.getByLabelText('field');
    expect(el.tagName).toBe('INPUT');
    expect(el).toHaveValue('a');
    expect(el).toHaveAttribute('placeholder', 'ph');
    expect(el).toHaveAttribute('id', 'f');
    fireEvent.change(el, { target: { value: 'ab' } });
    expect(onChange).toHaveBeenCalledWith('ab');
  });

  it('renders a textarea when multiline', () => {
    const onChange = vi.fn();
    const onBlur = vi.fn();
    render(
      <ReferenceInput
        value="x"
        onChange={onChange}
        onBlur={onBlur}
        candidates={[]}
        multiline
        disabled
        aria-label="field"
      />
    );
    const el = screen.getByLabelText('field');
    expect(el.tagName).toBe('TEXTAREA');
    expect(el).toBeDisabled();
    fireEvent.blur(el);
    expect(onBlur).toHaveBeenCalled();
  });

  it('shows the given number of lines and lets the user drag it taller', () => {
    render(
      <ReferenceInput
        value=""
        onChange={vi.fn()}
        candidates={[]}
        multiline
        rows={10}
        resizable
        aria-label="field"
      />
    );
    const el = screen.getByLabelText('field');
    expect(el).toHaveAttribute('rows', '10');
    expect(el.className).toContain('resize-y');
  });

  it('keeps a multi-line field fixed by default', () => {
    render(
      <ReferenceInput
        value=""
        onChange={vi.fn()}
        candidates={[]}
        multiline
        aria-label="field"
      />
    );
    const el = screen.getByLabelText('field');
    expect(el).toHaveAttribute('rows', '3');
    expect(el.className).toContain('resize-none');
  });

  describe('insert button', () => {
    it('inserts the picked token at the caret and moves the caret after it', () => {
      const onValue = vi.fn();
      render(<Controlled initial="run  now" onValue={onValue} />);
      focusAt(field(), 4);
      fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
      const search = screen.getByRole('textbox', { name: 'Search references' });
      expect(search).toHaveFocus();
      fireEvent.click(screen.getByRole('option', { name: /PAGE/ }));
      expect(onValue).toHaveBeenCalledWith('run ${params.PAGE} now');
      expect(field()).toHaveValue('run ${params.PAGE} now');
      expect(field()).toHaveFocus();
      expect(field().selectionStart).toBe('run ${params.PAGE}'.length);
      expect(field().selectionEnd).toBe('run ${params.PAGE}'.length);
      expect(screen.queryByRole('listbox')).toBeNull();
    });

    it('replaces the selected text', () => {
      render(<Controlled initial="a OLD b" />);
      focusAt(field(), 2, 5);
      fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
      fireEvent.change(
        screen.getByRole('textbox', { name: 'Search references' }),
        {
          target: { value: 'body' },
        }
      );
      fireEvent.keyDown(
        screen.getByRole('textbox', { name: 'Search references' }),
        {
          key: 'Enter',
        }
      );
      expect(field()).toHaveValue('a ${steps.fetch.outputs.body} b');
    });

    it('works in a textarea and closes on Escape with the focus back in the field', () => {
      const onValue = vi.fn();
      render(<Controlled initial={'one\ntwo'} multiline onValue={onValue} />);
      expect(field().tagName).toBe('TEXTAREA');
      fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
      fireEvent.keyDown(
        screen.getByRole('textbox', { name: 'Search references' }),
        {
          key: 'Escape',
        }
      );
      expect(screen.queryByRole('listbox')).toBeNull();
      expect(field()).toHaveFocus();
      expect(onValue).not.toHaveBeenCalled();
    });

    it('puts the focus back in the field when the button closes the picker', () => {
      render(<Controlled initial="x" />);
      const button = screen.getByRole('button', { name: 'Insert reference' });
      fireEvent.click(button);
      expect(
        screen.getByRole('textbox', { name: 'Search references' })
      ).toHaveFocus();
      fireEvent.click(button);
      expect(screen.queryByRole('listbox')).toBeNull();
      expect(field()).toHaveFocus();
    });

    // The search box holds the focus: a click back in the field gives the
    // keys to the field again.
    it('closes when the focus goes back to the field', () => {
      render(<Controlled initial="" />);
      fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
      expect(
        screen.getByRole('textbox', { name: 'Search references' })
      ).toHaveFocus();
      act(() => field().focus());
      expect(screen.queryByRole('listbox')).toBeNull();
      fireEvent.change(field(), { target: { value: '${' } });
      expect(screen.getByRole('listbox')).toBeInTheDocument();
      expect(screen.queryByLabelText('Search references')).toBeNull();
    });

    it('does not open the picker when disabled', () => {
      render(<Controlled initial="x" disabled />);
      const button = screen.getByRole('button', { name: 'Insert reference' });
      expect(button).toBeDisabled();
      fireEvent.click(button);
      expect(screen.queryByRole('listbox')).toBeNull();
    });
  });

  describe('typing ${', () => {
    it('opens the picker and Enter replaces only the typed text', () => {
      const onValue = vi.fn();
      render(<Controlled initial="x  tail" onValue={onValue} />);
      const el = field();
      focusAt(el, 2);
      expect(screen.queryByRole('listbox')).toBeNull();
      fireEvent.change(el, { target: { value: 'x ${pa tail' } });
      act(() => el.setSelectionRange(6, 6));
      fireEvent.select(el);
      // The panel has no search box and the focus stays in the field.
      expect(screen.getByRole('listbox')).toBeInTheDocument();
      expect(screen.queryByLabelText('Search references')).toBeNull();
      expect(el).toHaveFocus();
      // A text field has no aria-expanded or aria-haspopup (axe aria-allowed-attr).
      expect(el).not.toHaveAttribute('aria-expanded');
      expect(el).not.toHaveAttribute('aria-haspopup');
      expect(el).toHaveAttribute(
        'aria-controls',
        screen.getByRole('listbox').id
      );
      expect(screen.getAllByRole('option')).toHaveLength(2);
      // No auto "}" and no reformat of the typed text.
      expect(onValue).toHaveBeenLastCalledWith('x ${pa tail');

      fireEvent.keyDown(el, { key: 'ArrowDown' });
      const active = screen
        .getAllByRole('option')
        .find((o) => o.getAttribute('aria-selected') === 'true');
      expect(el).toHaveAttribute('aria-activedescendant', active?.id);
      fireEvent.keyDown(el, { key: 'ArrowUp' });
      fireEvent.keyDown(el, { key: 'Enter' });
      expect(onValue).toHaveBeenLastCalledWith('x ${params.API_URL} tail');
      expect(screen.queryByRole('listbox')).toBeNull();
      expect(el.selectionStart).toBe('x ${params.API_URL}'.length);
    });

    it('replaces a closed reference through its "}" when the caret is inside it', () => {
      const onValue = vi.fn();
      const initial = 'curl ${params.API_URL}/x';
      render(<Controlled initial={initial} onValue={onValue} />);
      const el = field();
      focusAt(el, 'curl ${params.API'.length);
      expect(screen.getByRole('listbox')).toBeInTheDocument();
      fireEvent.keyDown(el, { key: 'Enter' });
      expect(onValue).toHaveBeenLastCalledWith('curl ${params.API_URL}/x');
      expect(el.selectionStart).toBe('curl ${params.API_URL}'.length);
    });

    it('replaces a closed reference through its closing brace', () => {
      const onValue = vi.fn();
      render(
        <Controlled initial="curl ${params.API_URL}/x" onValue={onValue} />
      );
      focusAt(field(), 'curl ${params.API'.length);
      fireEvent.click(screen.getByRole('option', { name: /API_URL/ }));
      expect(onValue).toHaveBeenLastCalledWith('curl ${params.API_URL}/x');
      expect(field().selectionStart).toBe('curl ${params.API_URL}'.length);
    });

    it('opens on a lone dollar and a click picks', () => {
      const onValue = vi.fn();
      render(<Controlled initial="" onValue={onValue} />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '$' } });
      expect(screen.getAllByRole('option')).toHaveLength(3);
      fireEvent.click(screen.getByRole('option', { name: /fetch body/ }));
      expect(onValue).toHaveBeenLastCalledWith('${steps.fetch.outputs.body}');
    });

    // `grep foo$` is a whole command: Enter must commit it, not pick a reference.
    it('leaves Enter to the host on a lone dollar', () => {
      const onValue = vi.fn();
      const onKeyDown = vi.fn();
      render(<Controlled initial="" onValue={onValue} onKeyDown={onKeyDown} />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: 'grep foo$' } });
      expect(screen.getByRole('listbox')).toBeInTheDocument();
      expect(el).not.toHaveAttribute('aria-activedescendant');
      fireEvent.keyDown(el, { key: 'Enter' });
      expect(onKeyDown).toHaveBeenCalledTimes(1);
      expect(onValue).toHaveBeenLastCalledWith('grep foo$');
    });

    it('picks on a lone dollar once an arrow key chose an option', () => {
      const onValue = vi.fn();
      const onKeyDown = vi.fn();
      render(<Controlled initial="" onValue={onValue} onKeyDown={onKeyDown} />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '$' } });
      fireEvent.keyDown(el, { key: 'ArrowDown' });
      fireEvent.keyDown(el, { key: 'Enter' });
      expect(onKeyDown).not.toHaveBeenCalled();
      expect(onValue).toHaveBeenLastCalledWith('${steps.fetch.outputs.body}');
    });

    it('activates the first option once the brace follows the dollar', () => {
      render(<Controlled initial="" />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '$' } });
      expect(el).not.toHaveAttribute('aria-activedescendant');
      fireEvent.change(el, { target: { value: '${' } });
      expect(el).toHaveAttribute(
        'aria-activedescendant',
        screen.getAllByRole('option')[0]?.id
      );
    });

    it('closes on Escape and stays closed for that reference', () => {
      const onValue = vi.fn();
      render(<Controlled initial="" onValue={onValue} />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '${' } });
      expect(screen.getByRole('listbox')).toBeInTheDocument();
      fireEvent.keyDown(el, { key: 'Escape' });
      expect(screen.queryByRole('listbox')).toBeNull();
      fireEvent.change(el, { target: { value: '${p' } });
      expect(screen.queryByRole('listbox')).toBeNull();
      expect(field()).toHaveValue('${p');
    });

    it('closes when the reference is closed or the focus leaves', () => {
      const onBlur = vi.fn();
      render(<Controlled initial="" onBlur={onBlur} />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '${pa' } });
      expect(screen.getByRole('listbox')).toBeInTheDocument();
      fireEvent.change(el, { target: { value: '${pa}' } });
      expect(screen.queryByRole('listbox')).toBeNull();
      fireEvent.change(el, { target: { value: '${pa' } });
      expect(screen.getByRole('listbox')).toBeInTheDocument();
      fireEvent.blur(el);
      expect(screen.queryByRole('listbox')).toBeNull();
      expect(onBlur).toHaveBeenCalledTimes(1);
    });

    it('keeps Enter for the field when the list is empty', () => {
      render(<Controlled initial="" multiline />);
      const el = field();
      act(() => el.focus());
      fireEvent.change(el, { target: { value: '${zzz' } });
      expect(screen.getByText('No matching references')).toBeInTheDocument();
      expect(fireEvent.keyDown(el, { key: 'Enter' })).toBe(true);
    });

    it('never opens the picker when disabled', () => {
      render(<Controlled initial="${pa" disabled />);
      const el = field();
      fireEvent.focus(el);
      fireEvent.select(el);
      expect(screen.queryByRole('listbox')).toBeNull();
    });
  });

  describe('read view', () => {
    it('shows each reference token as a chip until the field is focused', () => {
      const text =
        'curl ${params.API_URL} ${steps.fetch.outputs.body} ${legacy}';
      const { container } = render(<Controlled initial={text} />);
      const chips = () =>
        Array.from(container.querySelectorAll('[data-reference-chip]'));
      expect(chips().map((c) => c.textContent)).toEqual([
        '${params.API_URL}',
        '${steps.fetch.outputs.body}',
        '${legacy}',
      ]);
      expect(chips()[0]).toHaveAttribute('data-reference-chip', 'canonical');
      const overlay = chips()[0]?.parentElement as HTMLElement;
      expect(overlay).toHaveAttribute('aria-hidden', 'true');
      expect(overlay.textContent).toBe(text);
      // The accessible value stays the plain text.
      expect(field()).toHaveValue(text);

      act(() => field().focus());
      expect(chips()).toHaveLength(0);
      expect(field()).toHaveValue(text);
      fireEvent.blur(field());
      expect(chips()).toHaveLength(3);
    });

    // A prompt field shows its references and Markdown marks in colour while
    // it is edited: the field draws no text itself, the layer does.
    it('draws a highlighted prompt through a layer, also when focused', () => {
      const text = '# Title\n- item ${params.PAGE}\nplain';
      const { container } = render(
        <Controlled initial={text} multiline highlight />
      );
      const layer = () =>
        container.querySelector('[aria-hidden="true"]:not(svg)');
      expect(layer()?.textContent).toBe(`${text}${END}`);
      expect(field().className).toContain('text-transparent');
      expect(field()).toHaveValue(text);
      expect(container.querySelectorAll('[data-reference-chip]')).toHaveLength(
        1
      );

      act(() => field().focus());
      expect(layer()?.textContent).toBe(`${text}${END}`);
      expect(container.querySelectorAll('[data-reference-chip]')).toHaveLength(
        0
      );
      expect(container.querySelectorAll('[data-reference-token]')).toHaveLength(
        1
      );
      expect(layer()?.querySelectorAll('.text-muted-foreground')).toHaveLength(
        2
      );
    });

    it('keeps the field itself for a text without the highlight', () => {
      const { container } = render(
        <Controlled initial={'# Title\nplain'} multiline />
      );
      expect(field().className).not.toContain('text-transparent');
      expect(
        container.querySelector('[aria-hidden="true"]:not(svg)')
      ).toBeNull();
    });

    // The text of a long textarea scrolls: the chips scroll with it.
    it('keeps the chips at the scroll offset of a textarea', () => {
      const text = Array(8).fill('a ${params.PAGE}').join('\n');
      const { container } = render(<Controlled initial={text} multiline />);
      const overlay = () =>
        container.querySelector('[data-reference-chip]')
          ?.parentElement as HTMLElement;
      field().scrollTop = 40;
      fireEvent.scroll(field());
      expect(overlay().scrollTop).toBe(40);
      act(() => field().focus());
      field().scrollTop = 60;
      fireEvent.blur(field());
      expect(overlay().scrollTop).toBe(60);
    });

    it('shows no overlay without tokens', () => {
      const { container } = render(<Controlled initial="plain ${ text" />);
      expect(
        container.querySelector('[aria-hidden="true"]:not(svg)')
      ).toBeNull();
    });
  });
});
