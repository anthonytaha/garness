# Sandboxing

Select a profile with `go run ./cmd/garness -sandbox-profile local` or
`go run ./cmd/garness -sandbox-profile isolated`. `GARNESS_SANDBOX_PROFILE` provides the
default when the flag is omitted. The default profile is `local`.

File tools and attachments are confined to the selected workspace. Ordinary
workspace reads and writes do not ask for approval. Skill resources use a
separate read boundary rooted at the configured skill directory. File access
rejects symlinks, hard-linked reads, non-regular files, and files larger than
4 MiB. Writes replace files atomically instead of truncating existing links.

## Profiles

In `local`, file tools edit the original workspace. Each shell command receives
a fresh workspace copy; permitted file and directory changes are automatically
applied afterward, including changes from a command returning a nonzero exit
status. Timed-out commands do not apply changes. Commands cannot refer to the
original workspace using its absolute path.

In `isolated`, file tools and commands share a disposable workspace copy.
`review_changes` (or the CLI command `:changes`) shows paths, hashes, and before
and after content for small files. `apply_changes` (or `:apply`) asks for approval
of those concrete changes before applying them. Changes are rejected if the
original has changed since the snapshot. Copy-back checks protected paths and
attempts rollback if an operation fails. It is not a transaction against other
programs concurrently editing the original workspace. Unapplied changes are
removed when the session closes.

Both profiles share the host kernel. The isolated profile separates workspace
changes; it is not a VM, a resource quota, or a guarantee against kernel exploits.

## Protected paths

`.env` and `.env.*` are unreadable and unwritable, except `.env.example`.
`.git`, `AGENTS.md`, `.garness`, `.agents`, and `.codex` cannot be written by file
tools or copied back from commands. These names are protected at every depth.

Snapshots omit secrets, Git metadata, and agent configuration directories.
`AGENTS.md` files are copied read-only. Commands consequently do not have the
repository's Git metadata or host credential files. On Linux, newly created
protected paths in the disposable copy are excluded from copy-back.

Snapshots support regular files and directories, preserve executable file bits,
and reject symlinks and hard links. They are limited to 64 MiB, 10,000 entries,
and 4 MiB per file. File/directory type replacements require deleting and applying the old entry
before creating and applying the replacement. Toolchain roots are read-only; home and temporary storage are fresh
for each command. The local macOS profile additionally exposes Homebrew and
adds Homebrew and `/usr/local/bin` to the executable search path. Additional read roots can be supplied by the embedding
application through `SandboxPolicy.ReadRoots`; these are explicit host access.

## Network approval

The shell tool accepts a `domains` array of exact lowercase ASCII hostnames,
for example `["github.com", "api.github.com"]`. It asks for approval of the
command, working directory, and requested domains. Approval expires when that
command finishes, is cancelled, or times out; open tunnels are closed too.
Subdomains, IP literals, wildcards, trailing dots, and URL strings are not grants.

Commands cannot connect directly to the network. Approved HTTP (port 80) and
HTTPS (port 443) traffic must pass through an authenticated per-command proxy.
The proxy checks HTTP destinations and HTTPS CONNECT destinations plus TLS SNI.
It resolves destinations on the host, rejects private and reserved addresses,
and dials the checked address without resolving it again. Redirect destinations
must independently be approved. Other protocols and ports are blocked. TLS
ClientHello records must fit in a single record; fragmented or encrypted SNI
handshakes are rejected. Approving a service permits that service's functionality,
including uploads or any forwarding functionality it offers.

Proxy environment variables configure cooperative clients; OS restrictions
prevent bypassing them. Programs that ignore proxy settings fail rather than
receive unrestricted access. The model provider's connection remains in the
host harness and is outside shell network grants.

## Platform backends and limits

macOS uses the built-in `sandbox-exec` with a deny-by-default Seatbelt profile.
It allows system toolchain reads, snapshot and temporary writes, and, after
approval, TCP connections only to the proxy's loopback port. Other host files,
network connections, and IPC services remain denied. Some developer tools may
require additional explicit read roots or OS permissions and will otherwise fail.

Linux requires `bubblewrap` (`bwrap`). Approved networking additionally requires
`socat`, which bridges the private network namespace to the proxy's Unix socket.
The sandbox uses separate user, process, network, and other namespaces, drops
capabilities, provides a minimal `/dev`, and mounts only permitted filesystems.
The host must permit the namespace setup; unavailable helpers or denied setup
never fall back to unsandboxed execution.

Commands default to a two-minute timeout and 1 MiB of combined output. The
embedding application can configure these limits; tool requests can shorten the
timeout. Cancellation kills the process group. Linux also tears down the process
namespace. On macOS, a process deliberately changing its session/process group
can outlive group cleanup, although its filesystem restrictions remain and its
network grant is revoked. CPU, memory, and temporary-disk quotas are not enforced.

## Verification

Run `go test -race ./...` for file boundaries, approval enforcement, copy-back,
proxy filtering, and grant lifecycle tests. Set `GARNESS_SANDBOX_INTEGRATION=1`
to also test the installed OS backend against local HTTP fixtures, direct-network
attempts, protected files, output limits, and timeouts. The GitHub Actions matrix
runs these tests on Linux and macOS, installing Linux helpers first.
