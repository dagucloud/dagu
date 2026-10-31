// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { ReferenceCandidate, ReferenceInputProps } from '../../contracts';
import { ReferenceInput } from '../ReferenceInput';

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
