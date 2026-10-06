import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { TokenBreakdown } from '../components/SessionCard';
import { DigestSection } from '../components/SessionInspector';
import { handoffTargetsPath } from './directed-handoff';
import { handoffBrief } from './handoff';
import { buildInsights } from './insights';
import {
  boardStatusKey,
  displayStatusLabel,
  freshLaunchDisabledHint,
  getModality,
  getSessionVendors,
  resumeDisabled,
  resumeDisabledHint,
  sessionCost,
  sessionTotalTokens,
  sumKnown,
  type Session,
} from './session';

const usage = {
  input_tokens: 10,
  output_tokens: 2,
  cache_creation_input_tokens: 3,
  cache_creation_1h_input_tokens: 4,
  cache_read_input_tokens: 5,
};
const accounting = { tokens: { model: usage }, cost: 0, unattributedTokens: usage };

function pi(overrides: Partial<Session> = {}): Session {
  return {
    sourceId: 'local',
    agent: 'pi',
    id: 'pi-session',
    cwd: '/repo',
    name: null,
    summary: null,
    synthesis: null,
    firstPrompt: null,
    declaredGoal: null,
    todos: [],
    digest: [],
    fileEdits: [],
    commits: [],
    repo: null,
    branch: null,
    durationMs: null,
    errors: 0,
    subagents: [],
    unpricedModels: [],
    eligibleForAggregates: true,
    mtime: new Date(2026, 8, 3).getTime(),
    ...accounting,
    ...overrides,
  } as Session;
}

describe('Pi accounting and local affordances', () => {
  it('keeps cost and usage availability independent and counts unattributed usage once', () => {
    expect(sessionTotalTokens(accounting)).toBe(48);
    expect(sessionTotalTokens({ ...accounting, tokensUnavailable: true })).toBeNull();
    expect(sessionCost(pi({ tokensUnavailable: true }))).toBe(0);
    expect(sessionCost({ ...accounting, costUnavailable: true })).toBeNull();
    expect(sessionTotalTokens({ tokens: {}, tokensKnown: true })).toBe(0);
    expect(sessionTotalTokens({ tokens: {} })).toBeNull();
    expect(sumKnown([48, sessionTotalTokens({ ...accounting, tokensUnavailable: true })])).toBeNull();
    expect(sumKnown([0, sessionCost({ ...accounting, costUnavailable: true })])).toBeNull();
  });

  it('does not render partial usage as a complete breakdown', () => {
    expect(renderToStaticMarkup(<TokenBreakdown {...accounting} tokensUnavailable />)).toContain(
      'Unavailable',
    );
    const known = renderToStaticMarkup(
      <TokenBreakdown
        tokens={{ model: { ...usage, input_tokens: 1000 } }}
        unattributedTokens={{ ...usage, input_tokens: 1000 }}
      />,
    );
    expect(known).toContain('in 2k');
    expect(known).toContain('usage without model attribution');
    expect(renderToStaticMarkup(<TokenBreakdown tokens={{}} tokensKnown />)).toContain('in 0');
  });

  it('retains Unknown status and modality without runtime evidence', () => {
    expect(boardStatusKey({ status: 'unknown', displayStale: false })).toBe('unknown');
    expect(displayStatusLabel({ status: 'unknown', displayStale: false })).toBe('Unknown');
    expect(getModality(null, 'pi')).toBe('Unknown');
    expect(getSessionVendors([{ agent: 'cursor' }, { agent: 'pi' }])).toEqual(['cursor', 'pi']);
  });

  it('labels the verified Pi runtime modalities', () => {
    for (const [entrypoint, label] of Object.entries({
      'pi-tui': 'CLI',
      'pi-rpc': 'RPC',
      'pi-json': 'JSON',
      'pi-print': 'Print',
      'pi-sdk': 'SDK',
    }))
      expect(getModality(entrypoint, 'pi')).toBe(label);
  });

  it('allows local unknown Pi resume but blocks live owners and all remote Pi launch', () => {
    const local = { sourceId: 'local', agent: 'pi', cwd: '/repo', status: 'unknown', displayStale: false };
    expect(resumeDisabledHint(local)).toBeUndefined();
    for (const status of ['busy', 'idle', 'waiting'])
      expect(resumeDisabledHint({ ...local, status })).toContain('already active');
    expect(freshLaunchDisabledHint({ ...local, sourceId: 'remote' })).toContain('locally only');
    expect(resumeDisabled({ ...local, sourceId: 'remote', launchable: true })).toBe(true);
    expect(handoffTargetsPath({ ...local, id: 'session' }, 'review')).toBe(
      '/api/directed-handoffs/targets?source=local&kind=review',
    );
    expect(handoffTargetsPath({ ...local, id: 'session' }, 'custom')).toBe(
      '/api/directed-handoffs/targets?source=local',
    );
  });

  it('hands off selected edited context while preserving the historical digest', () => {
    const detail = pi({
      tokensUnavailable: true,
      digest: [
        {
          turn: 1,
          category: 'user',
          description: 'Historical original',
          contextDescription: 'Edited request',
          contextSelected: true,
        },
        { turn: 2, category: 'user', description: 'Discarded branch', contextSelected: false },
      ],
    });
    const brief = handoffBrief(detail);
    expect(brief).toContain('Edited request');
    expect(brief).not.toContain('Historical original');
    expect(brief).not.toContain('Discarded branch');
    expect(brief).toContain('Tokens: Unavailable');
    expect(brief).toContain('Recorded cost: ≈$0.00');
    expect(detail.digest[0].description).toBe('Historical original');
  });

  it('renders every historical branch chronologically without replacing edited text', () => {
    const detail = pi({
      detailsIncomplete: true,
      digest: [
        {
          turn: 1,
          category: 'user',
          sourceEntryId: 'early',
          branchId: 'b1',
          time: 1000,
          description: 'Original request',
          contextDescription: 'Edited request',
          contextSelected: false,
          inherited: true,
        },
        {
          turn: 1,
          category: 'user',
          sourceEntryId: 'late',
          branchId: 'b2',
          time: 3000,
          description: 'Late branch',
          active: true,
        },
      ],
    });
    const markup = renderToStaticMarkup(<DigestSection detail={detail} />);
    expect(markup.indexOf('Original request')).toBeLessThan(markup.indexOf('Late branch'));
    expect(markup).not.toContain('Edited request');
    expect(markup).toContain('[branch b1]');
    expect(markup).toContain('[inherited]');
    expect(markup).toContain('[active]');
    expect(markup).toContain('all branches in time order');
    expect(markup).toContain('Some nested tool details are unavailable');
    expect(renderToStaticMarkup(<DigestSection detail={pi({ detailsIncomplete: true })} />)).toContain(
      'Some nested tool details are unavailable',
    );
  });

  it('preserves producer order for undated timeline entries', () => {
    const detail = pi({
      digest: [
        { turn: 1, category: 'recap', description: 'DATED_A_REVIEW', time: 1000 },
        { turn: 1, category: 'recap', description: 'UNDATED_B_REVIEW' },
        { turn: 1, category: 'recap', description: 'DATED_C_REVIEW', time: 3000 },
      ],
    });
    const markup = renderToStaticMarkup(<DigestSection detail={detail} />);
    expect(markup.indexOf('DATED_A_REVIEW')).toBeLessThan(markup.indexOf('UNDATED_B_REVIEW'));
    expect(markup.indexOf('UNDATED_B_REVIEW')).toBeLessThan(markup.indexOf('DATED_C_REVIEW'));
  });

  it('makes monthly cost unavailable while retaining the session count', () => {
    const insights = buildInsights(
      [pi(), pi({ id: 'unknown', costUnavailable: true })],
      new Date(2026, 8, 1),
    );
    expect(insights.sessionCount).toBe(2);
    expect(insights.knownCost).toBeNull();
    expect(insights.unknownCostCount).toBe(1);
  });
});
