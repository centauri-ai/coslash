import { Badge } from '@/components/ui/badge';

export function MachineBadge({ label }: { label: string }) {
  return (
    <Badge variant="secondary" className="text-coslash-muted shrink-0 text-xs font-semibold">
      {label}
    </Badge>
  );
}
