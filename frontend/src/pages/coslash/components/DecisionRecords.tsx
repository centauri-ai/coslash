import type { DecisionRecord } from '@/pages/coslash/lib/session';

export function DecisionRecords({ records }: { records: DecisionRecord[] }) {
  return (
    <div className="flex flex-col gap-3 pt-2">
      {records.map((record, index) => (
        <div key={index} className="text-xs">
          <div>{record.text}</div>
          <div className="text-coslash-muted pt-1">
            {record.status !== 'decided' && record.status !== 'applied' ? `${record.status} · ` : ''}
            {record.decidedBy?.replace('_', ' ')} · {record.cited ? 'Cited' : 'No cited turn'}
          </div>
          {record.why && <div className="pt-1">Why: {record.why}</div>}
          {record.alternative && <div className="pt-1">Alternative: {record.alternative}</div>}
          {(record.evidence ?? []).length > 0 && (
            <details className="pt-2">
              <summary className="cursor-pointer">Cited input</summary>
              {record.evidence!.map((entry, i) => (
                <div key={i} className="border-coslash-line border-l-2 p-2">
                  <div className="text-coslash-muted">
                    Turn {entry.turn} · {entry.category}
                  </div>
                  <div className="whitespace-pre-wrap">{entry.text}</div>
                </div>
              ))}
            </details>
          )}
        </div>
      ))}
    </div>
  );
}
