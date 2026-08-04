import { describe, expect, it } from 'vitest';
import { sessionsEmptyStateCopy } from '@/pages/coslash/lib/page-copy';

describe('sessionsEmptyStateCopy', () => {
  it('uses first-run only when all sessions are empty', () => {
    expect(sessionsEmptyStateCopy({ hasSessions: false, searchTerm: '', timeWindow: 'all' })).toEqual({
      kind: 'first-run',
    });
    expect(sessionsEmptyStateCopy({ hasSessions: false, searchTerm: '', timeWindow: 'week' })).toEqual({
      kind: 'copy',
      title: 'No sessions in this window.',
      detail: 'Select “All” to view older sessions.',
    });
  });

  it('preserves search and filter empty-state copy', () => {
    expect(sessionsEmptyStateCopy({ hasSessions: true, searchTerm: 'api', timeWindow: 'week' })).toEqual({
      kind: 'copy',
      title: 'Nothing matches “api” in this window.',
      detail: 'Select “All” to search older sessions.',
    });
    expect(sessionsEmptyStateCopy({ hasSessions: true, searchTerm: '', timeWindow: 'all' })).toEqual({
      kind: 'copy',
      title: 'No sessions match these filters.',
    });
  });
});
