# About the project
Garness is a lightweight privacy and security first agent harness written in Go.
It currently featues:
- OpenAI Chat complete compatibility
- Simple message memory
- Persistent sessions with autosave and a workspace session picker
- Session listing, inspection, resume, rename, and deletion
- Independent session branching and JSON or Markdown export
- Full transcript preservation across context compaction
- Fresh sandboxes on resume and interrupted tool-call recovery
- Agent system prompts
- Basic Tool system
- Workspaces
- Basic Tool Approval
- Context Compaction
- Skills
- Sandboxing
- Selectable local and isolated sandbox profiles
- Sandboxed shell execution on macOS and Linux
- Per-command approval for network access to exact domains
- Protected files and confined attachment and skill reads
- Isolated change review and approved copy-back
- Command timeouts, bounded output, and filtered environments
- Subagent orchestration with planning, versioned plan approvals, execution, verification, and bounded repairs
- Parallel task scheduling with dependency validation and configurable worker limits
- Independent subagent transcripts, tool permissions, write scopes, skills, and isolated workspaces
- Read-only planning and review agents with structured results
- File and shell acceptance checks, combined workspace verification, and dependent task revalidation
- Conflict detection and serial integration of verified worker changes
- Shared request, token reservation, and tool budgets with execution deadlines
- Cancellation of model requests, approval waits, and sandbox commands
- Structured failures and provider retries that preserve completed tool results
- Persistent orchestration runs, event histories, attempt artifacts, and accepted workspace changes
- Interrupted attempt reconciliation and recovery of interrupted final copy-back
- Serialized approval prompts and coordinator-controlled final change review and approved copy-back
- Orchestration CLI commands: :plan, :approve, :reject, :execute, :run-status, and :cancel
- Run management CLI commands: :runs, :run-resume, :attempt, :retry, :reconcile-apply, and :run-close


# Why?
- To better understand how tools like claude code, codex, Pi work.
- Because it's fun.
- Would like to use this for personal projects and experimentation.

# License

MIT License

Copyright (c) 2026 Anthony Taha

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
