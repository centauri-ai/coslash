# Prunes LiteLLM's model_prices_and_context_window.json to the Anthropic and
# OpenAI chat models coslash can see, keeping only the fields it reads. Field
# names stay exactly as LiteLLM publishes them so a refresh diff is auditable
# against upstream. Run through `make models`.
with_entries(
  select(
    (.value.litellm_provider == "anthropic" or .value.litellm_provider == "openai")
    and .value.mode == "chat"
    and .value.input_cost_per_token != null
  )
  | .value |= (
      {
        input_cost_per_token,
        output_cost_per_token,
        cache_creation_input_token_cost,
        cache_creation_input_token_cost_above_1hr,
        cache_read_input_token_cost,
        max_input_tokens,
      }
      | with_entries(select(.value != null))
    )
)
