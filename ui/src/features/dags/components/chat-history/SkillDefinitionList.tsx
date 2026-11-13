// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronRight, BookOpen } from 'lucide-react';
import { useState } from 'react';
import { components } from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';

type SkillDefinition = components['schemas']['SkillDefinition'];

/**
 * The skills of a step, as the effective catalog saw them: a collapsible
 * group with the name and the description of each skill. The tool
 * `load_skill` is in the list of tools, with its source.
 */
export function SkillDefinitionList({ skills }: { skills: SkillDefinition[] }) {
  const [open, setOpen] = useState(false);
  if (skills.length === 0) return null;
  return (
    <div className="border-b" data-testid="skill-definitions">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2 px-2 py-1 hover:bg-accent/50 text-left"
        type="button"
        aria-expanded={open}
      >
        <ChevronRight
          className={cn(
            'h-3 w-3 shrink-0 transition-transform',
            open && 'rotate-90'
          )}
        />
        <BookOpen className="h-3 w-3 text-purple-500" />
        <span className="text-xs font-medium">
          <I18nText text="Skills ({count})" values={{ count: skills.length }} />
        </span>
      </button>
      {open && (
        <div className="px-4 pb-2 space-y-2">
          {skills.map((skill) => (
            <div
              key={skill.name}
              data-testid="skill-definition"
              className="text-xs border-l-2 border-l-purple-500 pl-2 py-0.5"
            >
              <div className="flex min-w-0 items-center gap-1.5">
                <span className="min-w-0 truncate font-mono font-medium text-purple-500">
                  {skill.name}
                </span>
              </div>
              {skill.description && (
                <div className="text-muted-foreground">{skill.description}</div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
