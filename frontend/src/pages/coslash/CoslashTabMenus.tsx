import { ChevronDownIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { type AgentVendor } from '@/pages/coslash/lib/board-filters';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { assertOneOf } from '@/pages/coslash/lib/narrow';
import { getVendor, LOCAL_SOURCE_ID, VENDOR_KEYS, type VendorKey } from '@/pages/coslash/lib/session';
import { ALL_REPOSITORIES, type SessionLibraryFilters } from '@/pages/coslash/lib/session-library';
import { TIME_WINDOW_VALUES, TIME_WINDOWS, type TimeWindow } from '@/pages/coslash/lib/time-window';

export type { AgentVendor };
export type ViewMode = 'list' | 'board';
export const ALL_MACHINES = 'all-machines';

const AGENT_VENDORS = ['all', ...VENDOR_KEYS] as const satisfies readonly AgentVendor[];
const VIEW_MODES = ['list', 'board'] as const satisfies readonly ViewMode[];

export function isMachineFilterValue(value: string, machines: readonly MachineFact[]) {
  return (
    value === ALL_MACHINES ||
    value === LOCAL_SOURCE_ID ||
    machines.some((machine) => machine.sourceId === value)
  );
}

export function AgentVendorFilterTabMenu({
  value,
  vendors,
  onValueChange,
}: {
  value: AgentVendor;
  vendors: readonly VendorKey[];
  onValueChange: (value: AgentVendor) => void;
}) {
  const selectedLabel = value === 'all' ? 'All vendors' : getVendor(value).label;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="group text-xs font-semibold">
          {selectedLabel}
          <ChevronDownIcon className="transition-transform group-data-[state=open]:rotate-180" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuRadioGroup
          value={value}
          onValueChange={(next) => onValueChange(assertOneOf(next, AGENT_VENDORS))}
        >
          <DropdownMenuRadioItem value="all" className="text-xs font-semibold">
            <span>All vendors</span>
          </DropdownMenuRadioItem>
          {vendors.map((vendor) => (
            <DropdownMenuRadioItem key={vendor} value={vendor} className="text-xs font-semibold">
              <span>{getVendor(vendor).label}</span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function MachineFilterTabMenu({
  value,
  machines,
  onValueChange,
}: {
  value: string;
  machines: readonly MachineFact[];
  onValueChange: (value: string) => void;
}) {
  return (
    <Tabs
      value={value}
      onValueChange={(next) => {
        if (isMachineFilterValue(next, machines)) {
          onValueChange(next);
        }
      }}
    >
      <TabsList>
        <TabsTrigger value={ALL_MACHINES} className="text-xs font-semibold">
          <span>All machines</span>
        </TabsTrigger>
        <TabsTrigger value={LOCAL_SOURCE_ID} className="text-xs font-semibold">
          <span>Local</span>
        </TabsTrigger>
        {machines.map((machine) => (
          <TabsTrigger key={machine.sourceId} value={machine.sourceId} className="text-xs font-semibold">
            <span>{machine.label}</span>
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}

export function ViewingModeTabMenu({
  value,
  onValueChange,
}: {
  value: ViewMode;
  onValueChange: (value: ViewMode) => void;
}) {
  return (
    <Tabs value={value} onValueChange={(next) => onValueChange(assertOneOf(next, VIEW_MODES))}>
      <TabsList>
        <TabsTrigger value="list" className="text-xs font-semibold">
          <span>List</span>
        </TabsTrigger>
        <TabsTrigger value="board" className="text-xs font-semibold">
          <span>Board</span>
        </TabsTrigger>
      </TabsList>
    </Tabs>
  );
}

export function TimeWindowFilterTabMenu({
  value,
  onValueChange,
}: {
  value: TimeWindow;
  onValueChange: (value: TimeWindow) => void;
}) {
  const selectedLabel = TIME_WINDOWS.find((window) => window.value === value)?.label ?? value;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="group text-xs font-semibold">
          {selectedLabel}
          <ChevronDownIcon className="transition-transform group-data-[state=open]:rotate-180" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuRadioGroup
          value={value}
          onValueChange={(next) => onValueChange(assertOneOf(next, TIME_WINDOW_VALUES))}
        >
          {TIME_WINDOWS.map((window) => (
            <DropdownMenuRadioItem key={window.value} value={window.value} className="text-xs font-semibold">
              <span>{window.label}</span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function RepositoryFilterDropdownMenu({
  value,
  repositories,
  onValueChange,
}: {
  value: string;
  repositories: readonly string[];
  onValueChange: (value: string) => void;
}) {
  const selectedLabel = value === ALL_REPOSITORIES ? 'All repositories' : value;
  const repositoryValues = [ALL_REPOSITORIES, ...repositories];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="group max-w-40 text-xs font-semibold"
          aria-label={`Repository: ${selectedLabel}`}
          title={selectedLabel}
        >
          <span className="min-w-0 truncate">{selectedLabel}</span>
          <ChevronDownIcon className="transition-transform group-data-[state=open]:rotate-180" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="w-72 max-w-[var(--radix-dropdown-menu-content-available-width)]">
        <DropdownMenuRadioGroup
          value={value}
          onValueChange={(next) => onValueChange(assertOneOf(next, repositoryValues))}
        >
          <DropdownMenuRadioItem value={ALL_REPOSITORIES} className="text-xs font-semibold">
            <span className="min-w-0 truncate" title="All repositories">
              All repositories
            </span>
          </DropdownMenuRadioItem>
          {repositories.map((repository) => (
            <DropdownMenuRadioItem key={repository} value={repository} className="text-xs font-semibold">
              <span className="min-w-0 truncate" title={repository}>
                {repository}
              </span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const SHARE_STATES = [
  { value: 'all', label: 'All share states' },
  { value: 'eligible', label: 'Eligible' },
  { value: 'private', label: 'Private' },
  { value: 'running', label: 'Running' },
  { value: 'incomplete', label: 'Incomplete' },
  { value: 'stale', label: 'Stale' },
  { value: 'offline', label: 'Offline' },
  { value: 'failed', label: 'Failed' },
  { value: 'deleted', label: 'Deleted' },
] as const satisfies readonly { value: SessionLibraryFilters['shareState']; label: string }[];

export function ShareStateFilterDropdownMenu({
  value,
  onValueChange,
}: {
  value: SessionLibraryFilters['shareState'];
  onValueChange: (value: SessionLibraryFilters['shareState']) => void;
}) {
  const selectedLabel = SHARE_STATES.find((state) => state.value === value)?.label ?? value;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="group text-xs font-semibold"
          aria-label={`Share state: ${selectedLabel}`}
        >
          {selectedLabel}
          <ChevronDownIcon className="transition-transform group-data-[state=open]:rotate-180" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuRadioGroup
          value={value}
          onValueChange={(next) =>
            onValueChange(
              assertOneOf(
                next,
                SHARE_STATES.map((state) => state.value),
              ),
            )
          }
        >
          {SHARE_STATES.map((state) => (
            <DropdownMenuRadioItem key={state.value} value={state.value} className="text-xs font-semibold">
              <span>{state.label}</span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
