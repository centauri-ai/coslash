import type { ReactNode } from 'react';
import { ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { InlineSpinner } from '@/pages/coslash/components/session-list/InlineSpinner';
import { statusDot, styles } from '@/pages/coslash/components/session-list/styles';
import type { StatusKey } from '@/pages/coslash/lib/session';
import type { FacetKey } from '@/pages/coslash/lib/session-filter';

export type FacetOption = {
  id: string;
  label: string;
  count: number;
  selected: boolean;
  onClick: () => void;
  context?: string;
  title?: string;
  highlight?: string;
  status?: StatusKey;
  icon?: ReactNode;
  indicator?: ReactNode;
};
export type FacetSection = { id: FacetKey; label: string; options: FacetOption[] };

export function FacetRow({
  label,
  context,
  title,
  highlight,
  count,
  selected,
  icon,
  indicator,
  status,
  onClick,
}: FacetOption) {
  const index = highlight ? label.toLowerCase().indexOf(highlight) : -1;
  return (
    <div
      className={cn(styles.facet, 'relative gap-1.5', {
        'bg-coslash-tint text-coslash-accent-ink font-semibold': selected,
      })}
    >
      {/* Stretched over the row, so the whole row still toggles the facet. */}
      <button
        type="button"
        className="flex min-w-0 cursor-pointer items-center gap-[9px] text-left after:absolute after:inset-0 [&>svg]:size-3.5 [&>svg]:shrink-0"
        title={title}
        aria-pressed={selected}
        onClick={onClick}
      >
        {status ? <span className={cn('size-[7px] shrink-0 rounded-full', statusDot(status))} /> : icon}
        <span className="min-w-0">
          <span className="block truncate">
            {index < 0 ? (
              label
            ) : (
              <>
                {label.slice(0, index)}
                <mark className="bg-coslash-tint text-coslash-accent-ink">
                  {label.slice(index, index + highlight!.length)}
                </mark>
                {label.slice(index + highlight!.length)}
              </>
            )}
          </span>
          {context && (
            <span className="text-coslash-muted text-meta block truncate" title={context}>
              {context}
            </span>
          )}
        </span>
      </button>
      {indicator && <span className="relative flex">{indicator}</span>}
      <span className="text-meta ml-auto tabular-nums">{count}</span>
    </div>
  );
}

export function FacetRows({ options }: { options: FacetOption[] }) {
  return options.map((option) => <FacetRow key={option.id} {...option} />);
}

export function SidebarSection({
  id,
  label,
  open,
  isLoading,
  onToggle,
  headerClassName = 'flex items-center',
  headerExtra,
  children,
}: {
  id: FacetKey;
  label: string;
  open: boolean;
  isLoading: boolean;
  onToggle: () => void;
  headerClassName?: string;
  headerExtra?: ReactNode;
  children: ReactNode;
}) {
  const contentId = `coslash-${id}`;
  return (
    <div className="pb-3.5">
      <div className={headerClassName}>
        <button
          className={styles.sideHeading}
          aria-expanded={open}
          aria-controls={contentId}
          onClick={onToggle}
        >
          <ChevronRight className={cn('size-[13px] transition-transform', { 'rotate-90': open })} />
          <span className="flex flex-1 items-center justify-between">
            {label}
            {isLoading && <InlineSpinner />}
          </span>
        </button>
        {headerExtra}
      </div>
      {open && <div id={contentId}>{children}</div>}
    </div>
  );
}
