import { ChevronRightIcon } from 'lucide-react';
import type { SessionDetail } from '@/pages/coslash/lib/session';

export function SkillsSection({ skills }: { skills?: SessionDetail['skills'] }) {
  const seen = new Set<string>();
  const entries = (skills || []).filter((skill) => {
    const key = JSON.stringify([skill.name, skill.content]);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  if (!entries.length) return null;
  return (
    <details className="group/skills pt-4">
      <summary className="text-coslash-muted flex cursor-pointer list-none items-center gap-2 text-[10px] font-semibold tracking-wider [&::-webkit-details-marker]:hidden">
        <ChevronRightIcon className="size-3 group-open/skills:rotate-90" />
        <span>{`${entries.length} ${entries.length === 1 ? 'SKILL' : 'SKILLS'}`}</span>
        <div className="flex-1 border-b" />
      </summary>
      <div className="flex min-w-0 flex-col gap-2 pt-2">
        {entries.map((skill, index) => (
          <details key={index} className="bg-coslash-soft min-w-0 rounded-lg p-3">
            <summary className="text-coslash-ink cursor-pointer text-xs font-medium wrap-break-word">
              {skill.name}
            </summary>
            <pre className="text-coslash-ink max-h-72 overflow-auto pt-2 font-mono text-xs leading-relaxed wrap-break-word whitespace-pre-wrap">
              {skill.content || 'Content unavailable.'}
            </pre>
          </details>
        ))}
      </div>
    </details>
  );
}
