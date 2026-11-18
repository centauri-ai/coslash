export function SectionLabel({ title, note }: { title: string; note?: string }) {
  return (
    <div className="flex items-baseline gap-2 pb-2">
      <span className="text-brand text-xs font-bold tracking-widest">{title}</span>
      {note && <span className="text-coslash-muted text-xs">{note}</span>}
    </div>
  );
}

export function FieldLabel({ children }: { children: string }) {
  return <div className="text-coslash-muted pb-1 text-xs font-semibold tracking-wide">{children}</div>;
}
