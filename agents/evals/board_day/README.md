# Board and day evaluation

Run the fake-backed fixture gates with `pytest -q tests/kinds/board tests/kinds/day_report tests/contracts`. They cover canary and reference rejection, model failures, modes, slots and schema validation.

At G-staging only, a manual synthetic live check is available as `python evals/board_day/live.py --live --max-usd 5`. It requires `OPENAI_API_KEY` from the approved environment and refuses nonstandard models or tiers. The program prints aggregate gate results only and stops on the first failed check. This does not replace the real-server journey or a labelled quality evaluation.
