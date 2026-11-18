import { ChevronDown } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { boardGroupByLabel, type BoardRowGroupBy } from '@/pages/coslash/lib/session-grouping';

export function BoardGroupByMenu<T extends BoardRowGroupBy>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="border-coslash-line bg-coslash-surface text-coslash-muted hover:bg-coslash-soft text-meta flex min-h-8 cursor-pointer items-center gap-1.5 rounded-[9px] border px-2.5 whitespace-nowrap [&>svg]:size-3"
          aria-label={`${label} grouped by ${boardGroupByLabel(value)}`}
        >
          {label}
          <span className="text-coslash-ink font-[550]">{boardGroupByLabel(value)}</span>
          <ChevronDown aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="coslash-shell min-w-40">
        <DropdownMenuRadioGroup value={value} onValueChange={(next) => onChange(next as T)}>
          {options.map((option) => (
            <DropdownMenuRadioItem key={option.value} value={option.value} className="text-meta">
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
