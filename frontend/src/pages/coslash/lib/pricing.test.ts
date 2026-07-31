import { describe, expect, it } from 'vitest';
import { getEstimatedCost, type ModelTokens } from '@/pages/coslash/lib/pricing';

function tokens(overrides: Partial<ModelTokens>): ModelTokens {
  return {
    input_tokens: 0,
    output_tokens: 0,
    cache_creation_input_tokens: 0,
    cache_creation_1h_input_tokens: 0,
    cache_read_input_tokens: 0,
    ...overrides,
  };
}

describe('getEstimatedCost', () => {
  it('bills 1-hour cache writes at 2x input, 5-minute at 1.25x', () => {
    const fiveMin = getEstimatedCost({
      'claude-fable-5': tokens({ cache_creation_input_tokens: 1_000_000 }),
    });
    const oneHour = getEstimatedCost({
      'claude-fable-5': tokens({ cache_creation_1h_input_tokens: 1_000_000 }),
    });
    expect(fiveMin).toBe(12.5);
    expect(oneHour).toBe(20);
  });

  it("sums all token kinds at the model's rates", () => {
    expect(
      getEstimatedCost({
        'claude-fable-5': tokens({
          output_tokens: 61_000,
          cache_creation_1h_input_tokens: 129_000,
          cache_read_input_tokens: 7_720_000,
        }),
      }),
    ).toBeCloseTo(3.05 + 2.58 + 7.72, 6);
  });

  it('ignores models without a pricing entry', () => {
    expect(getEstimatedCost({ mystery: tokens({ output_tokens: 1e6 }) })).toBe(0);
  });
});
