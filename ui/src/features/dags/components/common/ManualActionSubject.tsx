// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { I18nText } from '@/i18n/I18nText';

type Props = {
  name?: string;
  id?: string;
  className?: string;
};

export function ManualActionSubject({ name, id, className }: Props) {
  const label = name || id;
  if (!label) {
    return null;
  }
  if (!id) {
    return <span className={className}>{label}</span>;
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={className} tabIndex={0}>
          {label}
        </span>
      </TooltipTrigger>
      <TooltipContent><I18nText text={"Subject ID:"} /> {id}</TooltipContent>
    </Tooltip>
  );
}
