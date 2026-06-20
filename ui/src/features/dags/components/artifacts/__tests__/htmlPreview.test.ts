// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  buildHTMLArtifactPreviewDocument,
  sanitizeHTMLArtifactPreview,
} from '../htmlPreview';

describe('htmlPreview', () => {
  const originalDOMParser = window.DOMParser;

  afterEach(() => {
    Object.defineProperty(window, 'DOMParser', {
      configurable: true,
      value: originalDOMParser,
    });
  });

  it('places CSP before full artifact content', () => {
    const documentHTML = buildHTMLArtifactPreviewDocument('OK');

    expect(documentHTML.indexOf('Content-Security-Policy')).toBeGreaterThan(-1);
    expect(documentHTML.indexOf('OK')).toBeGreaterThan(-1);
    expect(documentHTML.indexOf('Content-Security-Policy')).toBeLessThan(
      documentHTML.indexOf('OK')
    );
  });

  it('places CSP before resource markup that appears before the artifact head', () => {
    const documentHTML = buildHTMLArtifactPreviewDocument(
      '<img src="https://example.invalid/pixel.png"><head><title>artifact</title></head><body>OK</body>'
    );

    expect(documentHTML.indexOf('Content-Security-Policy')).toBeLessThan(
      documentHTML.indexOf('<img')
    );
  });

  it('neutralizes refresh, base, link, and area navigation markup', () => {
    const sanitized = sanitizeHTMLArtifactPreview(`
      <meta http-equiv="refresh" content="0;url=https://example.invalid">
      <base href="https://example.invalid/">
      <a href="/next" xlink:href="#icon">next</a>
      <area href="/map">
    `);
    const template = document.createElement('template');
    template.innerHTML = sanitized;

    expect(
      template.content.querySelector('meta[http-equiv="refresh"]')
    ).not.toBeInTheDocument();
    expect(
      template.content.querySelector('base[href]')
    ).not.toBeInTheDocument();

    const link = template.content.querySelector('a');
    expect(link).not.toBeNull();
    expect(link?.getAttribute('href')).toBeNull();
    expect(link?.getAttribute('xlink:href')).toBeNull();
    expect(link?.getAttribute('data-dagu-preview-href')).toBe('/next');
    expect(link?.getAttribute('data-dagu-preview-xlink-href')).toBe('#icon');
    expect(link?.getAttribute('aria-disabled')).toBe('true');

    const area = template.content.querySelector('area');
    expect(area).not.toBeNull();
    expect(area?.getAttribute('href')).toBeNull();
    expect(area?.getAttribute('data-dagu-preview-href')).toBe('/map');
    expect(area?.getAttribute('aria-disabled')).toBe('true');
  });

  it('neutralizes empty href targets', () => {
    const sanitized = sanitizeHTMLArtifactPreview('<a href="">empty</a>');
    const template = document.createElement('template');
    template.innerHTML = sanitized;
    const link = template.content.querySelector('a');

    expect(link?.getAttribute('href')).toBeNull();
    expect(link?.getAttribute('data-dagu-preview-href')).toBe('');
    expect(link?.getAttribute('aria-disabled')).toBe('true');
  });

  it('does not use DOMParser while building preview documents', () => {
    const domParserMock = vi.fn();
    Object.defineProperty(window, 'DOMParser', {
      configurable: true,
      value: domParserMock,
    });

    buildHTMLArtifactPreviewDocument('<section>OK</section>');

    expect(domParserMock).not.toHaveBeenCalled();
  });

  it('wraps fragments in a complete preview document', () => {
    const documentHTML = buildHTMLArtifactPreviewDocument(
      '<section>Fragment</section>'
    );

    expect(documentHTML.startsWith('<!doctype html>')).toBe(true);
    expect(documentHTML).toContain('<body><section>Fragment</section></body>');
  });

  it('escapes CSP attribute values', () => {
    const documentHTML = buildHTMLArtifactPreviewDocument(
      'OK',
      'default-src "none" & <x>'
    );

    expect(documentHTML).toContain(
      'content="default-src &quot;none&quot; &amp; &lt;x&gt;"'
    );
  });
});
