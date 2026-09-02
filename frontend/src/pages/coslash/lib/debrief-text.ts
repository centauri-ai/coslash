export type DebriefBlock =
  | { kind: 'paragraph'; text: string }
  | { kind: 'list'; ordered: boolean; items: string[] }
  | { kind: 'heading'; text: string };

const LIST_ITEM = /^(?:[-*•]|\d+[.)])\s+(.*\S.*)$/;
const HEADING = /^(#{1,3})\s+(.*\S.*)$/;

/** Turn freeform goal/outcome text into paragraphs, lists, and headings when markers fit. */
export function parseDebriefText(text: string): DebriefBlock[] {
  const trimmed = text.trim();
  if (!trimmed) return [];

  const blocks: DebriefBlock[] = [];
  let paragraph: string[] = [];
  let list: { ordered: boolean; items: string[] } | null = null;

  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    blocks.push({ kind: 'paragraph', text: paragraph.join(' ').trim() });
    paragraph = [];
  };

  const flushList = () => {
    if (list == null || list.items.length === 0) return;
    blocks.push({ kind: 'list', ordered: list.ordered, items: list.items });
    list = null;
  };

  for (const rawLine of trimmed.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (line === '') {
      flushParagraph();
      flushList();
      continue;
    }

    const heading = line.match(HEADING);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({ kind: 'heading', text: heading[2].trim() });
      continue;
    }

    const item = line.match(LIST_ITEM);
    if (item) {
      flushParagraph();
      const ordered = /^\d/.test(line);
      if (list == null || list.ordered !== ordered) {
        flushList();
        list = { ordered, items: [] };
      }
      list.items.push(item[1].trim());
      continue;
    }

    flushList();
    paragraph.push(line);
  }

  flushParagraph();
  flushList();
  return blocks;
}

export function blocksFromTexts(texts: string[]): DebriefBlock[] {
  if (texts.length === 0) return [];
  if (texts.length === 1) return parseDebriefText(texts[0]);
  return [{ kind: 'list', ordered: false, items: texts.map((text) => text.trim()).filter(Boolean) }];
}

export type DebriefPreview = {
  blocks: DebriefBlock[];
  hiddenCount: number;
  truncated: boolean;
};

const DEFAULT_PREVIEW_UNITS = 4;

function listPreview(
  block: Extract<DebriefBlock, { kind: 'list' }>,
  budget: number,
): {
  block: DebriefBlock;
  used: number;
  hidden: number;
} {
  if (budget <= 0) return { block: { ...block, items: [] }, used: 0, hidden: block.items.length };
  const items = block.items.slice(0, budget);
  return {
    block: { ...block, items },
    used: items.length,
    hidden: block.items.length - items.length,
  };
}

/** Keep a bounded preview: each list item and each paragraph/heading costs one unit. */
export function previewDebriefBlocks(
  blocks: DebriefBlock[],
  maxUnits = DEFAULT_PREVIEW_UNITS,
): DebriefPreview {
  if (blocks.length === 0) return { blocks: [], hiddenCount: 0, truncated: false };

  const visible: DebriefBlock[] = [];
  let budget = maxUnits;
  let hidden = 0;

  for (const block of blocks) {
    if (budget <= 0) {
      if (block.kind === 'list') hidden += block.items.length;
      else hidden += 1;
      continue;
    }

    if (block.kind === 'list') {
      const next = listPreview(block, budget);
      if (next.block.kind === 'list' && next.block.items.length > 0) visible.push(next.block);
      budget -= next.used;
      hidden += next.hidden;
      continue;
    }

    visible.push(block);
    budget -= 1;
  }

  return { blocks: visible, hiddenCount: hidden, truncated: hidden > 0 };
}

export const DEBRIEF_PREVIEW_CHARS = 160;

/** Collapse for display: bound block count and shorten long text in the source text. */
export function collapseDebriefBlocks(
  blocks: DebriefBlock[],
  maxUnits = DEFAULT_PREVIEW_UNITS,
  maxTextChars = DEBRIEF_PREVIEW_CHARS,
): DebriefPreview {
  const preview = previewDebriefBlocks(blocks, maxUnits);
  let shortened = false;
  const shorten = (text: string) => {
    if (text.length <= maxTextChars) return text;
    shortened = true;
    return `${text.slice(0, Math.max(0, maxTextChars - 1)).trimEnd()}…`;
  };
  const collapsed = preview.blocks.map((block) => {
    if (block.kind === 'list') return { ...block, items: block.items.map(shorten) };
    return { ...block, text: shorten(block.text) };
  });
  return {
    blocks: collapsed,
    hiddenCount: preview.hiddenCount,
    truncated: preview.truncated || shortened,
  };
}
