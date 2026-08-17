import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { assertOneOf } from '@/pages/coslash/lib/narrow';
import { getVendor, VENDOR_KEYS, type VendorKey } from '@/pages/coslash/lib/session';
import { TIME_WINDOW_VALUES, TIME_WINDOWS, type TimeWindow } from '@/pages/coslash/lib/time-window';

export type AgentVendor = 'all' | VendorKey;
export type ViewMode = 'list' | 'board';

const AGENT_VENDORS = ['all', ...VENDOR_KEYS] as const satisfies readonly AgentVendor[];
const VIEW_MODES = ['list', 'board'] as const satisfies readonly ViewMode[];

export function AgentVendorFilterTabMenu({
  value,
  vendors,
  onValueChange,
}: {
  value: AgentVendor;
  vendors: readonly VendorKey[];
  onValueChange: (value: AgentVendor) => void;
}) {
  return (
    <Tabs value={value} onValueChange={(next) => onValueChange(assertOneOf(next, AGENT_VENDORS))}>
      <TabsList>
        <TabsTrigger value="all" className="text-xs font-semibold">
          <span>All vendors</span>
        </TabsTrigger>
        {vendors.map((vendor) => (
          <TabsTrigger key={vendor} value={vendor} className="text-xs font-semibold">
            <span>{getVendor(vendor).label}</span>
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
  return (
    <Tabs value={value} onValueChange={(next) => onValueChange(assertOneOf(next, TIME_WINDOW_VALUES))}>
      <TabsList>
        {TIME_WINDOWS.map((window) => (
          <TabsTrigger key={window.value} value={window.value} className="text-xs font-semibold">
            <span>{window.label}</span>
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}
