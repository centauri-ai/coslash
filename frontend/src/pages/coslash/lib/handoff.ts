import { formatDuration, formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';
import {
  environmentFact,
  getTotalTokens,
  getVendor,
  goalSourceLabel,
  resolveGoal,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

export function handoffBrief(detail: SessionDetail): string {
  const goal = resolveGoal(detail);
  const openTodos = detail.todos.filter((todo) => !todo.done);
  const nextSteps =
    openTodos.length > 0
      ? openTodos.map((todo) => `- ${todo.text}`)
      : detail.synthesis?.nextStep
        ? [`- ${detail.synthesis.nextStep}`]
        : ['- —'];
  const decisions = detail.synthesis?.keyDecisions.length
    ? detail.synthesis.keyDecisions.map((decision) => `- ${decision}`)
    : ['- —'];
  const digest = detail.digest.length
    ? detail.digest.map((entry) => `- [${entry.category} · turn ${entry.turn}] ${entry.description}`)
    : ['- —'];
  const files = detail.fileEdits.length
    ? detail.fileEdits.map((fileEdit) => `- ${fileEdit.path} (+${fileEdit.adds}/-${fileEdit.dels})`)
    : ['- —'];
  const commits = detail.commits.length ? detail.commits.map((commit) => `- ${commit}`) : ['- —'];
  const costLabel = detail.agent === 'opencode' ? 'Recorded cost' : 'Estimated cost at list API prices';

  return [
    `# Handoff — ${detail.name ?? detail.id}`,
    '',
    `## Objective (${goalSourceLabel(goal.source)})`,
    ...(goal.texts.length === 1 ? goal.texts : goal.texts.map((text) => `- ${text}`)),
    '',
    '## Current state',
    detail.synthesis?.outcome ?? detail.summary ?? '—',
    '',
    '## Key decisions',
    ...decisions,
    '',
    '## Timeline',
    ...digest,
    '',
    '## Files',
    ...files,
    '',
    '## Commits',
    ...commits,
    '',
    '## Next steps',
    ...nextSteps,
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
