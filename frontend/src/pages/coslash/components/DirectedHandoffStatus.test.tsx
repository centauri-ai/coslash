import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { DirectedHandoffStatus } from '@/pages/coslash/components/DirectedHandoffStatus';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';

it('shows a completed review with empty output as no findings', () => {
  const handoff: DirectedHandoff = {
    id: 'review',
    sourceId: 'local',
    sourceAgent: 'claude',
    sourceSessionId: 'source',
    targetAgent: 'codex',
    kind: 'review',
    status: 'completed',
    createdAt: 1,
  };
  const markup = renderToStaticMarkup(<DirectedHandoffStatus handoff={handoff} />);
  expect(markup).toContain('Codex review complete');
  expect(markup).toContain('No findings.');
});
