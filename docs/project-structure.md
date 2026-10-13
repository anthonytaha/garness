# Project structure

Garness separates the executable, agent orchestration, reusable tools, and sandbox
implementation. `harness`, `model`, `tools`, and `sandbox` remain importable by
other Go applications. CLI implementation details live under `internal/`.

```text
cmd/garness/main.go       Executable entry point
internal/cli/            Configuration, terminal approvals, and conversation loop
harness/                 Agent loop, attachments, skills, compaction, and usage
model/                   Provider configuration and OpenAI-compatible HTTP calls
tools/                   Tool registry, calculator, file, shell, and change adapters
sandbox/                 File confinement, snapshots, copy-back, proxy, and runners
tests/                   Application-level CLI tests
examples/skills/         Example skill definitions
docs/                    Usage and implementation documentation
.github/workflows/       Native macOS and Linux sandbox tests
```

The dependency direction is:

```text
cmd/garness → internal/cli → harness → tools → sandbox
                              ↓
                            model
```

The CLI also constructs providers and sandboxes. `harness` uses sandbox workspace
types for attachments and skill resources. `sandbox` imports neither `harness`
nor `tools`; its approvals use callbacks. `Agent.ConfigureSandbox` binds the
workspace and reusable tools to an agent. Skill activation tools remain in
`harness` because they change conversation state.

Within `sandbox`, `policy.go` defines restrictions, `workspace.go` confines file
access, `snapshot.go` creates workspace copies, and `changes.go` handles review
and application. `proxy.go` handles domain grants and proxy requests; `tls.go`
validates HTTPS client handshakes. `command.go` coordinates invocation policy,
approval, and copy-back; `runner.go` handles shared execution and output limits.

`runner_darwin.go` and `runner_linux.go` build their native sandbox commands.
Go selects these files automatically for the target OS. Shared Unix process and
filesystem helpers have explicit build constraints. `runner_unsupported.go`
returns errors on other platforms and never enables unsandboxed execution.

Package tests live beside their implementation. They may use the same package
for private security checks. `tests/cli_test.go` builds and launches the executable
to check profile selection, configuration, and the CLI change-review commands.

Run the application from the repository root:

```sh
go run ./cmd/garness -sandbox-profile local
```

Run the full test suite, including the installed OS backend:

```sh
GARNESS_SANDBOX_INTEGRATION=1 go test -race ./...
```

The former root-level `main.go` entry point has moved to `cmd/garness`; `go run .`
is replaced by `go run ./cmd/garness`. Workspace and sandbox APIs now come from
`anthonytaha/garness/sandbox`; tool APIs come from `anthonytaha/garness/tools`.
