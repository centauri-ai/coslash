import { describe, expect, it } from 'vitest';
import {
  blocksFromTexts,
  collapseDebriefBlocks,
  parseDebriefText,
  previewDebriefBlocks,
} from './debrief-text';

describe('parseDebriefText', () => {
  it('keeps plain prose as paragraphs', () => {
    expect(parseDebriefText('Understand the SSH problem.\n\nPropose a helper.')).toEqual([
      { kind: 'paragraph', text: 'Understand the SSH problem.' },
      { kind: 'paragraph', text: 'Propose a helper.' },
    ]);
  });

  it('parses bullet and numbered lists', () => {
    expect(
      parseDebriefText(`Committed successfully.

- Commit: abc
- Pushed to origin

1. First
2. Second`),
    ).toEqual([
      { kind: 'paragraph', text: 'Committed successfully.' },
      { kind: 'list', ordered: false, items: ['Commit: abc', 'Pushed to origin'] },
      { kind: 'list', ordered: true, items: ['First', 'Second'] },
    ]);
  });

  it('turns multiple goal texts into one list', () => {
    expect(blocksFromTexts(['Diagnose SFTP cost', 'Design Linux helper'])).toEqual([
      {
        kind: 'list',
        ordered: false,
        items: ['Diagnose SFTP cost', 'Design Linux helper'],
      },
    ]);
  });
});

describe('previewDebriefBlocks', () => {
  it('bounds list items and reports what remains', () => {
    const blocks = parseDebriefText(`Done.

- one
- two
- three
- four
- five`);
    expect(previewDebriefBlocks(blocks, 4)).toEqual({
      blocks: [
        { kind: 'paragraph', text: 'Done.' },
        { kind: 'list', ordered: false, items: ['one', 'two', 'three'] },
      ],
      hiddenCount: 2,
      truncated: true,
    });
  });
});

describe('collapseDebriefBlocks', () => {
  it('shortens long paragraphs in the collapsed preview', () => {
    const long = 'a'.repeat(200);
    const collapsed = collapseDebriefBlocks([{ kind: 'paragraph', text: long }], 4, 160);
    expect(collapsed.truncated).toBe(true);
    expect(collapsed.blocks[0]).toEqual({
      kind: 'paragraph',
      text: `${'a'.repeat(159)}…`,
    });
  });
});
