# Prunes LiteLLM's model_prices_and_context_window.json to priced chat models,
# then adds every OpenCode gateway model under `opencode/`, the provider-qualified
# id coslash records for them -- including ids LiteLLM also lists, since the
# gateway prices them differently. OpenCode costs are dollars per million tokens;
# LiteLLM costs are already dollars per token. Run through `make models`.
def per_token: if . == null then null else . / 1000000 end;

# LiteLLM writes some context windows as floats (2000000.0); Go decodes an int.
def whole: if . == null then null else floor end;

with_entries(
  select(
    (.value.mode == "chat" or .value.mode == "responses")
    and .value.input_cost_per_token != null
  )
  | .value |= (
      {
        input_cost_per_token,
        output_cost_per_token,
        cache_creation_input_token_cost,
        cache_creation_input_token_cost_above_1hr,
        cache_read_input_token_cost,
        max_input_tokens: (.max_input_tokens | whole),
      }
      | with_entries(select(.value != null))
    )
)
| reduce ($opencode[0].opencode.models | to_entries[]) as $model (
    .;
    .["opencode/\($model.key)"] = (
      $model.value
      | {
          input_cost_per_token: (.cost.input | per_token),
          output_cost_per_token: (.cost.output | per_token),
          cache_creation_input_token_cost: (.cost.cache_write | per_token),
          cache_read_input_token_cost: (.cost.cache_read | per_token),
          max_input_tokens: (.limit.context | whole),
        }
      | with_entries(select(.value != null))
    )
  )
