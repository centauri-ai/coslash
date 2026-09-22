import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { SessionName, TokenBreakdown } from '@/pages/coslash/components/SessionCard';

describe('SessionName', () => {
  it('uses a block box so inspector titles can truncate', () => {
    expect(renderToStaticMarkup(<SessionName name="A long session name" variant="inspector" />)).toContain(
      'block',
    );
  });
});

describe('TokenBreakdown', () => {
  it('renders unavailable usage for an empty token map', () => {
    expect(renderToStaticMarkup(<TokenBreakdown tokens={{}} />)).toContain('—');
  });
});
