export type ModelTokens = {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_creation_1h_input_tokens: number;
  cache_read_input_tokens: number;
};

type ModelPricing = {
  input: number;
  output: number;
  cacheWrite: number;
  cacheWrite1h: number;
  cacheRead: number;
};

// $/MTok for current Anthropic and OpenAI models (verified 2026-07-27).
// Both vendors: cacheWrite = 1.25x input (Anthropic 5-min TTL, OpenAI 30-min
// minimum cache life), cacheRead = 0.1x input. Anthropic 1-hour-TTL cache
// writes bill at 2x input.
const PRICING: Record<string, ModelPricing> = {
  'claude-fable-5': {
    input: 10,
    output: 50,
    cacheWrite: 12.5,
    cacheWrite1h: 20,
    cacheRead: 1,
  },
  'claude-mythos-5': {
    input: 10,
    output: 50,
    cacheWrite: 12.5,
    cacheWrite1h: 20,
    cacheRead: 1,
  },
  'claude-opus-5': {
    input: 5,
    output: 25,
    cacheWrite: 6.25,
    cacheWrite1h: 10,
    cacheRead: 0.5,
  },
  'claude-opus-4-8': {
    input: 5,
    output: 25,
    cacheWrite: 6.25,
    cacheWrite1h: 10,
    cacheRead: 0.5,
  },
  'claude-opus-4-7': {
    input: 5,
    output: 25,
    cacheWrite: 6.25,
    cacheWrite1h: 10,
    cacheRead: 0.5,
  },
  'claude-opus-4-6': {
    input: 5,
    output: 25,
    cacheWrite: 6.25,
    cacheWrite1h: 10,
    cacheRead: 0.5,
  },
  'claude-opus-4-5': {
    input: 5,
    output: 25,
    cacheWrite: 6.25,
    cacheWrite1h: 10,
    cacheRead: 0.5,
  },
  // Introductory pricing through 2026-08-31; on 2026-09-01 this becomes
  // 3 / 15 / 3.75 / 6 / 0.3, same as Sonnet 4.6.
  'claude-sonnet-5': {
    input: 2,
    output: 10,
    cacheWrite: 2.5,
    cacheWrite1h: 4,
    cacheRead: 0.2,
  },
  'claude-sonnet-4-6': {
    input: 3,
    output: 15,
    cacheWrite: 3.75,
    cacheWrite1h: 6,
    cacheRead: 0.3,
  },
  'claude-sonnet-4-5': {
    input: 3,
    output: 15,
    cacheWrite: 3.75,
    cacheWrite1h: 6,
    cacheRead: 0.3,
  },
  'claude-haiku-4-5': {
    input: 1,
    output: 5,
    cacheWrite: 1.25,
    cacheWrite1h: 2,
    cacheRead: 0.1,
  },
  'gpt-5.6-sol': {
    input: 5,
    output: 30,
    cacheWrite: 6.25,
    cacheWrite1h: 10,
    cacheRead: 0.5,
  },
  'gpt-5.6-terra': {
    input: 2.5,
    output: 15,
    cacheWrite: 3.125,
    cacheWrite1h: 5,
    cacheRead: 0.25,
  },
  'gpt-5.6-luna': {
    input: 1,
    output: 6,
    cacheWrite: 1.25,
    cacheWrite1h: 2,
    cacheRead: 0.1,
  },
  // Pre-5.6 OpenAI models have no separate cache-write price — writes bill as input.
  // Pro models support no caching at all and publish no cached-input price, so
  // reads bill as input rather than the usual 0.1x.
  'gpt-5.5-pro': {
    input: 30,
    output: 180,
    cacheWrite: 30,
    cacheWrite1h: 30,
    cacheRead: 30,
  },
  'gpt-5.5-cyber': {
    input: 12.5,
    output: 75,
    cacheWrite: 12.5,
    cacheWrite1h: 12.5,
    cacheRead: 1.25,
  },
  'gpt-5.5': {
    input: 5,
    output: 30,
    cacheWrite: 5,
    cacheWrite1h: 5,
    cacheRead: 0.5,
  },
  'gpt-5.4-mini': {
    input: 0.75,
    output: 4.5,
    cacheWrite: 0.75,
    cacheWrite1h: 0.75,
    cacheRead: 0.075,
  },
};

// Model IDs may carry suffixes (dates, "[1m]") — match on the longest known prefix.
// Matches the first insertion-order prefix, not the longest. If prefixes match, short keys go after longer keys
function getModelPricing(model: string): ModelPricing | null {
  if (PRICING[model]) return PRICING[model];
  const key = Object.keys(PRICING).find((prefix) => model.startsWith(prefix));
  return key ? PRICING[key] : null;
}

export function getUnpricedModels(tokens: Record<string, ModelTokens>): string[] {
  return Object.keys(tokens).filter((model) => getModelPricing(model) === null);
}

export function getEstimatedCost(tokens: Record<string, ModelTokens>): number {
  return Object.entries(tokens).reduce((sum, [model, modelTokens]) => {
    const pricing = getModelPricing(model);
    if (!pricing) return sum;
    return (
      sum +
      (modelTokens.input_tokens * pricing.input +
        modelTokens.output_tokens * pricing.output +
        modelTokens.cache_creation_input_tokens * pricing.cacheWrite +
        modelTokens.cache_creation_1h_input_tokens * pricing.cacheWrite1h +
        modelTokens.cache_read_input_tokens * pricing.cacheRead) /
        1e6
    );
  }, 0);
}
