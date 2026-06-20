// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export const HTML_ARTIFACT_PREVIEW_CSP = [
  "default-src 'none'",
  'img-src data: blob:',
  'media-src data: blob:',
  'font-src data:',
  "style-src 'unsafe-inline'",
  "script-src 'none'",
  "connect-src 'none'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-src 'none'",
].join('; ');

function escapeHTMLAttribute(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

export function sanitizeHTMLArtifactPreview(content: string): string {
  const template = document.createElement('template');
  template.innerHTML = content;

  template.content.querySelectorAll('meta[http-equiv]').forEach((meta) => {
    const httpEquiv = meta.getAttribute('http-equiv');
    if (httpEquiv?.trim().toLowerCase() === 'refresh') {
      meta.remove();
    }
  });

  template.content.querySelectorAll('base[href]').forEach((base) => {
    base.remove();
  });

  template.content.querySelectorAll('a, area').forEach((element) => {
    const href = element.getAttribute('href');
    const xlinkHref = element.getAttribute('xlink:href');
    if (href !== null) {
      element.setAttribute('data-dagu-preview-href', href);
      element.removeAttribute('href');
    }
    if (xlinkHref !== null) {
      element.setAttribute('data-dagu-preview-xlink-href', xlinkHref);
      element.removeAttribute('xlink:href');
    }
    if (href !== null || xlinkHref !== null) {
      element.setAttribute('aria-disabled', 'true');
    }
  });

  return template.innerHTML;
}

export function buildHTMLArtifactPreviewDocument(
  content: string,
  csp = HTML_ARTIFACT_PREVIEW_CSP
): string {
  const sanitizedContent = sanitizeHTMLArtifactPreview(content);
  const cspMeta = `<meta http-equiv="Content-Security-Policy" content="${escapeHTMLAttribute(csp)}">`;

  return `<!doctype html><html><head>${cspMeta}<meta charset="utf-8"></head><body>${sanitizedContent}</body></html>`;
}
