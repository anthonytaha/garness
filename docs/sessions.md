# Sessions

Interactive startup shows sessions for the current workspace; enter a number or `n` to start fresh. `garness --new` and `garness --resume <id>` bypass the picker. Piped input requires one of these explicit flags. Sessions are created upon the first accepted prompt and saved throughout each turn. A failed save stops further agent work.

Sessions live under `$XDG_DATA_HOME/garness/sessions`, or `~/.local/share/garness/sessions`, grouped by a hash of the canonical workspace path. Set `GARNESS_SESSION_DIR` to override the root. Files contain conversation and tool output, use private permissions, and contain no saved credentials or approval grants. A session's transcript can still contain sensitive text supplied by the user or tools.

Management commands work without contacting the model:

```
garness sessions list
garness sessions inspect <id>
garness sessions rename <id> New name
garness sessions delete <id>
garness sessions branch <id>
garness sessions export <id> markdown transcript.md
garness sessions export <id> json
```

Exports go to stdout when no output path is supplied. Existing output files are never overwritten. Branch prints the new ID. Branches have independent files and retain the original parent ID.

REPL commands: `:session`, `:sessions`, `:inspect`, `:new`, `:resume <id>`, `:rename <name>`, `:branch`, `:export [markdown|json] [path]`, and `:delete <id>`. Switch away before deleting the active session. Names accept spaces; REPL export paths currently cannot contain spaces.

Resume and branch create fresh sandboxes from current workspace files. Unapplied changes from previous isolated sandboxes are not restored. Current project instructions and skills are rebuilt, and missing active skills produce warnings. Compaction affects model context while the complete transcript is retained for export.

Unanswered saved tool calls receive an interruption result. Garness does not automatically execute them again: a command might already have run before a crash. Verify filesystem state before retrying.

Only one process can edit a session at a time. On macOS and Linux, operating-system locks release automatically when a process exits, including after a crash. The `.json.lock` files remain as stable lock targets; do not delete them while Garness is running. Stale locks from older versions are recovered automatically when their recorded process is no longer running. Corrupt or unsupported session files produce errors rather than being silently overwritten. Moving a workspace changes its session group.
