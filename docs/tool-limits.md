# Tool limits

Garness permits 32 tool rounds per user turn by default. A round is one model response requesting tools; multiple tools in that response count as one round. HTTP retries do not consume additional rounds.

Configure the limit with `GARNESS_MAX_TOOL_STEPS=64` or `garness --max-tool-steps 64`. The flag overrides the environment. Values must be positive integers.

When the limit is reached, Garness makes one additional model request without tools to summarize progress and unfinished work. No additional tools execute in that request. If the provider fails to return a summary, saved tool results remain available. Send a follow-up such as “Continue the remaining work” to start another turn with a fresh budget.
