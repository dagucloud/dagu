// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { downloadBlob } from '@/lib/download';

// Canvas dimensions beyond this fail silently in several browsers.
const MAX_EXPORT_EDGE_PX = 8192;

/**
 * Resolves the export background from the on-screen card color so dark-theme
 * exports stay readable outside the app.
 */
export function graphExportBackground(): string {
  const value = getComputedStyle(document.documentElement)
    .getPropertyValue('--card')
    .trim();
  if (value) {
    return value;
  }
  return document.documentElement.classList.contains('dark')
    ? '#181b22'
    : '#ffffff';
}

/**
 * Serializes the rendered graph SVG for export: strips the on-screen zoom
 * transform, pins explicit dimensions from the viewBox, and paints an opaque
 * background rect. Mermaid embeds its styles inside the SVG, so the clone is
 * self-contained.
 */
export function serializeGraphSvg(
  svg: SVGSVGElement,
  background: string
): { blob: Blob; width: number; height: number } {
  const clone = svg.cloneNode(true) as SVGSVGElement;
  clone.style.transform = '';
  clone.style.transformOrigin = '';

  const viewBox = clone.viewBox.baseVal;
  const width =
    viewBox && viewBox.width > 0
      ? viewBox.width
      : svg.getBoundingClientRect().width || 800;
  const height =
    viewBox && viewBox.height > 0
      ? viewBox.height
      : svg.getBoundingClientRect().height || 600;
  clone.setAttribute('width', String(width));
  clone.setAttribute('height', String(height));

  const rect = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
  rect.setAttribute('x', String(viewBox?.x ?? 0));
  rect.setAttribute('y', String(viewBox?.y ?? 0));
  rect.setAttribute('width', String(width));
  rect.setAttribute('height', String(height));
  rect.setAttribute('fill', background);
  clone.insertBefore(rect, clone.firstChild);

  const markup = new XMLSerializer().serializeToString(clone);
  return {
    blob: new Blob([markup], { type: 'image/svg+xml' }),
    width,
    height,
  };
}

export function exportGraphSvg(svg: SVGSVGElement, baseName: string): void {
  const { blob } = serializeGraphSvg(svg, graphExportBackground());
  downloadBlob(blob, `${baseName}-graph.svg`);
}

export function exportGraphPng(
  svg: SVGSVGElement,
  baseName: string,
  pixelRatio = 2
): void {
  const background = graphExportBackground();
  const { blob, width, height } = serializeGraphSvg(svg, background);

  const scale = Math.min(
    pixelRatio,
    MAX_EXPORT_EDGE_PX / Math.max(width, height, 1)
  );
  const objectUrl = URL.createObjectURL(blob);
  const image = new Image();
  image.onload = () => {
    try {
      const canvas = document.createElement('canvas');
      canvas.width = Math.max(1, Math.round(width * scale));
      canvas.height = Math.max(1, Math.round(height * scale));
      const context = canvas.getContext('2d');
      if (!context) {
        return;
      }
      context.fillStyle = background;
      context.fillRect(0, 0, canvas.width, canvas.height);
      context.drawImage(image, 0, 0, canvas.width, canvas.height);
      canvas.toBlob((pngBlob) => {
        if (pngBlob) {
          downloadBlob(pngBlob, `${baseName}-graph.png`);
        }
      }, 'image/png');
    } catch (err) {
      console.error('Graph export failed:', err);
    } finally {
      URL.revokeObjectURL(objectUrl);
    }
  };
  image.onerror = () => {
    console.error('Graph export failed: could not rasterize the SVG');
    URL.revokeObjectURL(objectUrl);
  };
  image.src = objectUrl;
}
