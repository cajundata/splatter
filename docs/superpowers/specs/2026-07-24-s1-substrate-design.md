# S1 Substrate — Design

Date: 2026-07-24
Scope: Milestone M1, Session S1 of [the splatter spec](../../splatter_spec.md). The spec's locked decisions govern; this document records the S1-specific design choices made during brainstorming.

## Goal

Build the substrate the whole instrument stands on: the Go module scaffold, frozen v1 schema types with JSONL round-trip, Windows-safe write helpers, and the `init`, `status`, and `validate` commands.

**Exit criterion (from spec):** `splatter init && splatter validate` passes on a fresh workspace on both OSes; schema round-trip tests green. macOS is verified live during the session; Windows via a manual checklist run on the Windows machine afterward (decision: deferred manual check, not CI).

## Decisions made this cycle

- **Scope:** S1 only. Each later session gets its own design/plan cycle.
- **CLI framework:** `spf13/cobra`.
- **Build tooling:** Makefile now (build, test, cross-compile with version stamping via `-ldflags -X`); goreleaser deferred until the binary first needs installing on the Windows machine.
- **Windows verification:** deferred manual checklist; Windows-sensitive logic still unit-tested.
- **Code layout:** domain packages under `internal/`, thin cobra layer under `cmd/splatter/`.

## Architecture

```
splatter/
├── cmd/splatter/          # main + cobra commands (thin)
│   ├── main.go
│   ├── root.go            # --json plumbing, exit-code mapping
│   ├── init.go
│   ├── status.go
│   └── validate.go
├── internal/
│   ├── schema/            # v1 record types, JSONL codec, brief front matter, hashing
│   ├── workspace/         # root discovery, scaffold, validate/status walks
│   └── fsio/              # append-fsync JSONL writer, replace-with-rename helper
├── docs/
├── Makefile
└── go.mod                 # github.com/cajundata/splatter
```

Version is stamped at build time and surfaces in `splatter --version`; the same variable later feeds manifest `harness` fields (S3).

### internal/schema

- Frozen v1 types: `RunHeader`, `CallRecord`, `Verdict`, `Critique`, plus brief YAML front matter.
- JSONL encode/decode. Go's default unmarshal already tolerates unknown fields (spec requirement). Missing required fields are caught by explicit `Validate()` methods per type, since Go zero-values would otherwise pass silently.
- Brief hashing: `sha256` over full file bytes after LF normalization (`\r\n` → `\n`).

### internal/fsio

Two primitives, used by everything that writes:

- `AppendRecord(path, record)`: open `O_APPEND|O_CREATE|O_WRONLY`, marshal to one line, single `Write` of line+`\n`, fsync, close. No partial lines by construction.
- `ReplaceFile(path, content)`: write temp file in the destination's directory, fsync, remove destination if present, rename. The remove-before-rename step is unconditional on both OSes, so this helper needs **no** `runtime.GOOS` branch — the spec's "exactly one platform branch (browser-open)" rule is preserved. Used only for derived, regenerable files (sheets, exports).

### internal/workspace

- Root discovery: walk up from cwd looking for workspace markers (`providers.yaml` + `projects/`).
- Scaffolding for `init` (workspace and project modes).
- The workspace walk shared by `validate` and `status`.

## Command behavior

### splatter init

- No argument → scaffold a **workspace** in the current directory:
  - `.gitattributes` forcing LF on all text files
  - `.gitignore` ignoring `projects/*/runs/*/images/`
  - `providers.yaml` stub (`version: 1`, commented example profiles)
  - `pricing.yaml` stub (`version` field)
  - `projects/` directory
- `splatter init <project>` inside a workspace → scaffold `projects/<project>/` with `briefs/`, `critiques/`, `packages/` (`.gitkeep` in each). `runs/` and `verdicts.jsonl` are created on first use, not at init.
- Refuses to scaffold a workspace inside an existing workspace; `init <project>` outside a workspace is a usage error.
- Idempotent: re-running reports what already exists; never overwrites.

### splatter status

Workspace root, then per-project counts: briefs, runs, verdict records, critique files. Sync state reports `not configured` until S4 delivers push/pull.

### splatter validate

Walks the workspace, or one project with `--project`:

- Parses every `manifest.jsonl`, `verdicts.jsonl`, and `critiques/*.json` line-by-line against v1 schemas; required-field checks per type.
- Recomputes `brief_sha256` for briefs referenced by run headers; mismatch is a finding.
- Verifies image `sha256` when the PNG is present locally; absent files are not findings (they may live only in Spaces).
- Rejects critique files referencing image IDs not present in the run's manifest.
- Collects **all** findings rather than stopping at the first; exits 3 with the list.

### --json and exit codes

- `--json` is a persistent root flag. Each command builds a result struct: human-readable rendering to stdout by default, one JSON object with `--json`. Logs and warnings always go to stderr.
- Exit codes: 0 success, 1 runtime error, 2 usage error (cobra parse/unknown-command mapped explicitly), 3 validation failure.

## Testing

TDD throughout (superpowers:test-driven-development).

- **schema:** round-trip equality per type; unknown-field tolerance; missing-required rejection; LF-normalization hashing (CRLF and LF inputs hash identically).
- **fsio:** n appends produce n parseable lines; replace works over an existing destination; no temp-file litter on success or failure.
- **workspace:** `init` on a temp dir then `validate` passes; corrupted fixtures (malformed JSON line, missing required field, brief hash mismatch, unknown critique image ID) each produce exit 3 with the right finding.
- **cross-compile:** `make build-all` produces `windows/amd64` and `darwin/arm64` binaries.
- **Windows manual checklist** (run when convenient, results reported back): init a fresh workspace, `validate`, re-run `init` for idempotence, confirm LF line endings in generated files.

## Out of scope for S1

Providers, transport, gen/fan/sheet/verdict/export/push/pull/report commands, brief template content, critique rubric content, goreleaser, CI. Each arrives in its designated session per the spec's build sequence.
