# Provider retries

Completion requests use at most three attempts within an overall 180-second timeout. Explicit HTTP 429, 500, 502, 503, and 504 responses are eligible for retry. Transport errors are returned immediately because their execution status may be ambiguous.

Garness respects `Retry-After` seconds or HTTP dates. Without valid server advice, 429 retries wait approximately 30 then 60 seconds; other temporary errors wait approximately one then two seconds. Random jitter is added. If a requested wait exceeds the remaining deadline, the latest HTTP error is returned without another retry.

The CLI displays the status and wait before retrying a turn's model request. Retrying a model request does not replay tools or consume another tool round.

Persistent 429 responses require waiting or checking provider request, token, and workspace usage limits. Raising `--max-tool-steps` does not increase provider limits. Completed tool results remain in the saved session; send a follow-up to continue after the limit clears.

When a response includes a valid numeric `X-RateLimit-Remaining`, the CLI displays its value, including on retries and compaction requests. Zero produces an additional warning. Successful model responses expose the value through `LLMResponse.RateLimitRemaining`; absent or malformed headers remain unknown. The count alone does not identify a reset time or every independently enforced quota, so it does not trigger an arbitrary preemptive sleep.
