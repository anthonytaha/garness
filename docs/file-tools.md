# File tools and output limits

`read_file` reads existing UTF-8 text. Choose a mutation tool according to the intended operation:

- `write_file(path, content)` creates a file or replaces its entire contents. Supply the full file.
- `edit_file(path, old_text, new_text)` replaces one exact, unique selection and preserves the surrounding text. Read the file first. An empty selection, missing match, or multiple matches produces an error without writing. An empty replacement deletes the selection.
- `append_file(path, content)` preserves existing contents and adds text at the end. Missing files are created. Include needed newlines explicitly.

All operations enforce workspace confinement, protected paths, and file-size limits. Edits and appends reject symlinks and hard links on read, preserve existing permissions, and replace files atomically. Mutation calls through the same Workspace are serialized; external editors and shell commands are not part of that synchronization.

The model output allowance defaults to 8,192 tokens per completion. Configure it with `GARNESS_MAX_OUTPUT_TOKENS=4096` or:

```
go run ./cmd/garness --max-output-tokens 16384
```

The flag overrides the environment value; values must be positive integers. Choose a value supported by your provider and model. Compaction summaries keep their separate 1,024-token allowance.

If the provider returns `finish_reason: length`, Garness reports the cutoff and executes no tools from that response. Increase the output allowance or request smaller edits before continuing. This guard relies on the provider reporting a cutoff; it cannot establish whether generated code is logically complete.
