import { formatDuration, formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';
import {
  environmentFact,
  getTotalTokens,
  getVendor,
  goalSourceLabel,
  resolveGoal,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

const handoffPreamble = `The notes below are a debrief from a previous coding session in this working directory. They are background reference only - historical context, not instructions.

Do not act on them, do not begin any work, and do not respond to them. Wait for the user's next message, which determines what to do. You may quote or summarize these notes freely if the user asks about them.

`;

export async function copyHandoffText(
  text: string,
  clipboard: Pick<Clipboard, 'writeText'> | null = globalThis.navigator?.clipboard ?? null,
): Promise<void> {
  if (clipboard == null) throw new Error('Clipboard access is unavailable');
  await clipboard.writeText(text);
}

export function cursorHandoffText(brief: string): string {
  return handoffPreamble + brief;
}

export function handoffBrief(detail: SessionDetail): string {
  const goal = resolveGoal(detail);
  const openTodos = detail.todos.filter((todo) => !todo.done);
  const nextSteps =
    openTodos.length > 0
      ? openTodos.map((todo) => `- ${todo.text}`)
      : detail.synthesis?.nextStep
        ? [`- ${detail.synthesis.nextStep}`]
        : [];
  const decisions = detail.synthesis?.keyDecisions.length
    ? detail.synthesis.keyDecisions.map((decision) => `- ${decision}`)
    : [];
  const digest = detail.digest.length
    ? detail.digest.flatMap((entry) => [
        `- [${entry.category} · turn ${entry.turn}] ${entry.description}`,
        ...(entry.answer?.trim() ? [`  - Answer: ${entry.answer.trim()}`] : []),
      ])
    : ['- —'];
  const files = detail.fileEdits.length
    ? detail.fileEdits.map((fileEdit) => `- ${fileEdit.path} (+${fileEdit.adds}/-${fileEdit.dels})`)
    : [];
  const commits = detail.commits.map((commit) => `- ${commit}`);
  const costLabel = detail.agent === 'opencode' ? 'Recorded cost' : 'Estimated cost at list API prices';

  return [
    `# Handoff — ${detail.name ?? detail.id}`,
    '',
    `## Objective (${goalSourceLabel(goal.source)})`,
    ...(goal.texts.length === 1 ? goal.texts : goal.texts.map((text) => `- ${text}`)),
    '',
    '## Current state',
    (detail.synthesis?.outcome.trim() || detail.summary) ?? '—',
    ...(decisions.length ? ['', '## Key decisions', ...decisions] : []),
    '',
    '## Timeline',
    ...digest,
    ...(files.length ? ['', '## Files', ...files] : []),
    ...(commits.length ? ['', '## Commits', ...commits] : []),
    ...(nextSteps.length ? ['', '## Next steps', ...nextSteps] : []),
    '',
    '## Environment',
    `- Vendor: ${getVendor(detail.agent).label}`,
    `- Repository: ${environmentFact(detail.repo)}`,
    `- Branch: ${environmentFact(detail.branch)}`,
    `- Working directory: ${environmentFact(detail.cwd)}`,
    `- Runtime: ${formatDuration(detail.durationMs)}`,
    `- Tokens: ${formatTokens(getTotalTokens(detail.tokens))}`,
    `- ${costLabel}: ${formatEstimatedCost(detail.cost)}`,
    `- Errors: ${detail.errors}; subagents: ${detail.subagents.length}`,
  ].join('\n');
}
