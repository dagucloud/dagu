// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

const ATTACHMENT_NAME_PATTERN = /^[a-zA-Z0-9_][a-zA-Z0-9_. -]*$/;
const WINDOWS_RESERVED_NAME = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?$/i;
const RESERVED_EXTENSIONS = new Set(['.md', '.yaml', '.yml']);
const MAX_ATTACHMENT_NAME_LENGTH = 128;

let generatedNameSequence = 0;

// Mirrors the attachment-name contract enforced by the Wiki API.
function isValidAttachmentName(name: string): boolean {
  if (
    name.length === 0 ||
    name.length > MAX_ATTACHMENT_NAME_LENGTH ||
    !ATTACHMENT_NAME_PATTERN.test(name) ||
    name.endsWith(' ') ||
    name.endsWith('.') ||
    WINDOWS_RESERVED_NAME.test(name)
  ) {
    return false;
  }
  const extensionStart = name.lastIndexOf('.');
  const extension =
    extensionStart < 0 ? '' : name.slice(extensionStart).toLowerCase();
  return !RESERVED_EXTENSIONS.has(extension);
}

function attachmentExtension(contentType: string): string {
  const subtype = contentType.split('/')[1]?.split(/[+;]/)[0]?.toLowerCase();
  if (
    !subtype ||
    subtype.length > 16 ||
    !/^[a-z0-9]+$/.test(subtype) ||
    RESERVED_EXTENSIONS.has(`.${subtype}`)
  ) {
    return 'bin';
  }
  return subtype;
}

export function attachmentUploadName(
  file: Pick<File, 'name' | 'type'>
): string {
  if (isValidAttachmentName(file.name)) return file.name;
  generatedNameSequence += 1;
  return `pasted-${Date.now()}-${generatedNameSequence}.${attachmentExtension(file.type)}`;
}
