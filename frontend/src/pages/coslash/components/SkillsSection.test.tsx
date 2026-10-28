import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { SkillsSection } from './SkillsSection';

it('collapses exact duplicates and retains content variants under the same name', () => {
  const html = renderToStaticMarkup(
    <SkillsSection
      skills={[
        { name: 'review', content: 'first <script>\n  body' },
        { name: 'review', content: 'first <script>\n  body' },
        { name: 'review', content: 'second' },
      ]}
    />,
  );
  expect(html).toContain('2 SKILLS');
  expect(html.match(/<pre/g)).toHaveLength(2);
  expect(html).toContain('first &lt;script&gt;\n  body');
  expect(html).toContain('second');
  expect(html).not.toContain(' open=');
});
it('hides absent skills and explains unavailable content', () => {
  expect(renderToStaticMarkup(<SkillsSection />)).toBe('');
  expect(renderToStaticMarkup(<SkillsSection skills={[{ name: 'review', content: '' }]} />)).toContain(
    'Content unavailable.',
  );
});
