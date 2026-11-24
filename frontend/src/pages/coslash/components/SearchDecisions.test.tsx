import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { SearchMatchLine } from '@/pages/coslash/components/SearchDecisions';

it('starts a long match line near its first highlight so truncation keeps it visible', () => {
  const prefix = 'x'.repeat(80);
  const markup = renderToStaticMarkup(
    <SearchMatchLine match={{ kind: 'prompt', snippet: `${prefix}release now`, ranges: [[80, 87]] }} />,
  );
  expect(markup).toContain(`: …${'x'.repeat(30)}<mark>release</mark> now`);

  const short = renderToStaticMarkup(
    <SearchMatchLine match={{ kind: 'title', snippet: 'Staging release', ranges: [[8, 15]] }} />,
  );
  expect(short).toContain(': Staging <mark>release</mark>');
});
