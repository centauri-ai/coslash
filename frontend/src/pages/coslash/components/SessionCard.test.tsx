import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { TokenBreakdown } from '@/pages/coslash/components/SessionCard';

describe('TokenBreakdown', () => {
  it('renders unavailable usage for an empty token map', () => {
    expect(renderToStaticMarkup(<TokenBreakdown tokens={{}} />)).toContain('—');
  });
});
