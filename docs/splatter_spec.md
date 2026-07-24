# Splatter Build Specification

Version: 1.0 (2026-07-24)
Status: Handoff artifact for Claude Code. Locked decisions are not open for reinterpretation during implementation. Items marked OPERATOR DECISION were resolved to recommendations and approved before handoff.

## 1. Purpose

Splatter is a concept-refinement instrument for T-shirt designs. It generates image concepts through multiple cloud providers from identical briefs, records evidence (cost, latency, model metadata) at call time, supports an agent-driven critique loop, and exports selected concepts as a package for a human artist. It does not produce print-quality output. Provider comparison is a first-class function: keep-rate, cost, and latency per provider must be computable from manifests alone.

## 2. Repo topology

Two repositories under `github.com/cajundata`:

- **splatter**: the harness. Go module `github.com/cajundata/splatter`. Contains all Go source, tests, and release tooling. Cross-compiled to `windows/amd64` and `darwin/arm64`. Versioned releases; the binary is installed on PATH on both machines.
- **splats**: the workspace. Contains briefs, run evidence, verdicts, critiques, sheets, packages, provider profiles, pricing table, and agent instructions. Git-tracked, synced between machines by push/pull. No Go source.

Claude Code design sessions run inside splats with the splatter binary on PATH. The tool source is not present in the workspace; the operate/modify boundary is physical.

## 3. Workspace layout (splats)

```
splats/
├── CLAUDE.md              # agent operating contract (written in M2)
├── AGENTS.md              # Codex equivalent, same contract
├── .gitattributes         # forces LF on all text files (session 1)
├── providers.yaml         # provider profiles and profile sets
├── pricing.yaml           # versioned price table for cost fallback
└── projects/
    └── <project>/
        ├── briefs/
        │   └── b_001.md
        ├── runs/
        │   └── r_0001/
        │       ├── manifest.jsonl   # CLI-written only, append-only
        │       ├── sheet.html       # CLI-generated, relative paths
        │       ├── images/
        │       │   └── c_01_0.png
        │       └── raw/
        │           └── c_01.json    # redacted wire responses
        ├── verdicts.jsonl           # CLI-written only, append-only
        ├── critiques/
        │   └── r_0001.json          # agent-written, schema-validated
        └── packages/
            └── p_001/               # artist deliverables
```

Rules:
- PNGs under `runs/*/images/` are gitignored. Manifests reference them by sha256; `splatter push`/`pull` sync them through DigitalOcean Spaces.
- `manifest.jsonl` and `verdicts.jsonl` are written exclusively by the CLI. Any other writer is a contract violation.
- `sheet.html` lives inside the run directory so image references stay relative and open identically on both OSes with no server.

## 4. Frozen schemas (v1)

Every record carries `"v": 1`. Schema changes bump the version; readers must tolerate unknown fields but never missing required ones.

### 4.1 Brief

Markdown file with YAML front matter:

```yaml
---
id: b_001
project: gradient-descent
concept: one-line concept statement
mood: [technical, austere]
motifs: [contour lines, sparse grids]
exclusions: [brains, circuit-board cliches]
aspect: square
---
Creative direction prose follows in the body.
```

`brief_sha256` is computed over the full file bytes after LF normalization. The hash freezes the brief for a run; editing a brief after use produces a new hash and therefore a distinguishable lineage node.

### 4.2 Manifest records (`runs/<run>/manifest.jsonl`)

Run header, first line:

```json
{"v":1,"type":"run","run":"r_0001","project":"gradient-descent",
 "brief":"briefs/b_001.md","brief_sha256":"9f2c...","iteration":1,
 "parent_run":null,"relationship":null,
 "created":"2026-07-24T14:31:08Z","harness":"splatter v0.1.0"}
```

`relationship` on non-null `parent_run` takes values `"textual-refinement-of"` or `"regeneration-of"`. A regeneration is never recorded as an edit. If a provider supports true image-to-image, that call records `operation: "edit"` in its call record; otherwise refinement is a fresh generation linked by lineage. No silent translation between the two.

Call record, one per provider invocation, written atomically at call completion:

```json
{"v":1,"type":"call","run":"r_0001","call":"c_01","ts":"2026-07-24T14:31:26Z",
 "provider":"gemini","model_requested":"<model>","model_returned":"<model>",
 "profile":"gemini-baseline","operation":"generate",
 "request":{"prompt":"...","n":4,"aspect":"square","seed":null,
            "native":{"...":"recorded verbatim"}},
 "response":{"latency_ms":18240,"http_status":200,
             "provider_request_id":"req_..."},
 "cost":{"usd":0.27,"source":"reported"},
 "images":[{"id":"c_01_0","file":"images/c_01_0.png","sha256":"...",
            "w":1024,"h":1024,"aspect_actual":"square"}],
 "raw":"raw/c_01.json","error":null}
```

Failed calls also get records: `images` empty, `error` populated with `{"stage":"...","http_status":...,"message":"..."}`, `cost` and `latency_ms` recorded if the call reached the wire. Failures never destroy successful sibling records.

Cost source precedence, locked: `"reported"` (parsed from the API response), then `"table:<pricing_version>"` (from `pricing.yaml`), then `{"usd":null,"source":"unavailable"}`. Untagged estimates are prohibited. Agents never compute or narrate any metric; they read manifests.

### 4.3 Verdicts (`verdicts.jsonl`)

```json
{"v":1,"type":"verdict","run":"r_0001","ts":"2026-07-24T15:02:11Z",
 "session":"2026-07-24",
 "keep":["c_01_0","c_02_2"],"cull":["c_01_1","c_01_2"],
 "notes":[{"image":"c_01_0","text":"tighten the left glyph"},
          {"image":null,"text":"run-level note"}]}
```

`session` defaults to the local calendar date; `--session` overrides. The UI promotion trigger counts spatial-reference prose in `notes[].text` across distinct consecutive `session` values.

### 4.4 Critique (`critiques/<run>.json`, agent-written)

```json
{"v":1,"run":"r_0001","rubric":"rubric_v1",
 "items":[{"image":"c_01_0","verdict":"keep",
           "scores":{"concept_fit":4,"novelty":3,"legibility":4,"cliche_risk":1},
           "reason":"one to two sentences"}]}
```

`splatter validate` checks critique files against this schema and rejects unknown image IDs. Critique-versus-operator agreement (standing check, section 11) is computed by joining `items[].verdict` against verdict records per image.

## 5. CLI grammar

Binary name `splatter`. Every command accepts `--json` for a machine-readable result object on stdout (logs go to stderr). Exit codes: 0 success, 1 runtime or provider error, 2 usage error, 3 validation failure. `fan` exits 0 if at least one call succeeded (failures are still recorded), 1 only if all calls failed.

```
splatter init                                  # scaffold workspace or project
splatter status                                # workspace state, pending sync
splatter validate [--project <p>]              # schemas, hashes, critique files
splatter gen  --brief <path> --profile <id> [-n 4]
splatter fan  --brief <path> [--set baseline | --profiles a,b]
splatter sheet --run <id> [--open]
splatter verdict --run <id> [--keep ids] [--cull ids]
                 [--note "<image_id>: text"]...   # note without "id:" prefix = run-level
                 [--session <label>]
splatter export --project <p> --runs <ids>     # artist package
splatter push | splatter pull                  # Spaces blob sync
splatter report [--project <p>]                # keep-rate, cost, latency per provider (M4)
splatter providers                             # configured profiles + capabilities
```

This surface is the contract the M2 skill layer is written against. Skill and agent instructions invoke `splatter` only, never shell syntax, so nothing agent-driven varies between PowerShell 7 and zsh.

## 6. Provider layer

### 6.1 Interface

```go
type Provider interface {
    Name() string
    Capabilities() Capabilities
    Generate(ctx context.Context, req Request) (Result, error)
}

type Capabilities struct {
    Img2Img, Edit, Seed bool
    MaxBatch            int
    AspectModes         []string // provider-native vocabulary
}

type Request struct {
    Model  string
    Prompt string
    N      int
    Aspect string         // harness vocabulary: square, portrait_4_5, portrait_2_3, landscape_4_3
    Seed   *int64
    Native map[string]any // provider-specific, recorded verbatim in manifest
}

type Result struct {
    Images  []Image        // bytes + dimensions; CLI writes files and hashes
    Latency time.Duration  // from the instrumented transport
    Cost    Cost           // per precedence in 4.2
    Meta    CallMeta       // model_returned, provider_request_id, http_status
    Raw     []byte         // redacted wire body (see 6.3)
}
```

Capability checks fail at planning time, before any API call. An unsupported operation is an error, never a silent downgrade. Aspect maps to the nearest provider-native mode per adapter; `aspect_actual` records what the provider was actually asked for.

### 6.2 Adapters (M1)

Two adapters, written fresh against this interface. Starshp's `internal/provider` is reference material only, per the extraction audit (AUDIT.md, verdict overridden to pattern-copy on premise failure):

- **Gemini**: use `starshp_app/internal/provider/gemini.go` as reference for image-mode configuration (responseModalities TEXT+IMAGE, the API rejecting tools alongside image output, InlineData part parsing) and the parameter-injected auth pattern. Do not import Starshp code. Do not reproduce the streaming ChatProvider shape; splatter's contract is synchronous batch.
- **OpenAI**: fresh adapter against the `openai-go/v3` `client.Images.Generate` service (synchronous; no streaming partials needed).

Error normalization is per-adapter. No shared normalizer with SDK dependencies (the Starshp audit found its shared `errors.go` hard-imports the Gemini SDK; do not repeat that shape).

### 6.3 Instrumented transport

One custom `http.RoundTripper` injected into both SDK clients:

- Measures wall-clock latency around the actual HTTP exchange.
- Captures the raw response body.
- Never persists request headers (no credentials on disk, ever).

Raw sidecar policy (OPERATOR DECISION, approved): `raw/c_NN.json` stores the wire response verbatim except base64 image payload fields, each replaced by `{"$blob":"<sha256>","bytes":N}`. This keeps raw evidence auditable without duplicating megabytes of image data in git.

The transport layer makes the no-narration rule mechanical: metrics and raw bytes originate below adapter code and cannot be forgotten per adapter.

### 6.4 Configuration

`providers.yaml` (git-tracked in splats; profiles are evidence):

```yaml
version: 1
profiles:
  gemini-baseline:
    provider: gemini
    model: <model id>
    native: { }
  openai-baseline:
    provider: openai
    model: <model id>
    native: { quality: high }
profile_sets:
  baseline: [gemini-baseline, openai-baseline]
```

`pricing.yaml` carries a `version` field, bumped on every edit; that version string lands in `cost.source` as `table:<version>`.

API keys via environment variables only, injected into adapters as constructor parameters: `GEMINI_API_KEY`, `OPENAI_API_KEY`. No config-file key storage, no OS keychain code.

## 7. Spaces sync

- Env vars: `SPLATTER_S3_ENDPOINT`, `SPLATTER_S3_BUCKET`, `SPLATTER_S3_ACCESS_KEY`, `SPLATTER_S3_SECRET_KEY`, optional `SPLATTER_S3_REGION`.
- Key layout: `blobs/<sha256>`, flat and content-addressed. Sync is idempotent by construction: `push` uploads blobs referenced by local manifests and absent remotely; `pull` downloads blobs referenced by local manifests and absent locally.
- GC policy (OPERATOR DECISION, approved): nothing auto-deletes in v0.1. A future `splatter gc` computes unreferenced blobs from manifests; until it exists, culled-run images persist remotely. Manifests preserve what existed and what it cost regardless.
- Machine handoff: `git pull && splatter pull`. That is the entire protocol.

## 8. Cross-platform requirements

- Targets: `windows/amd64` (native Windows, PowerShell 7) and `darwin/arm64`. No linux target, no WSL branches.
- Exactly one permitted platform branch in the codebase: browser-open (`cmd /c start` / `open`) via `runtime.GOOS`.
- All paths through `path/filepath`; all manifest-stored paths relative and slash-normalized.
- `.gitattributes` forcing LF on all text files is created by `splatter init`, session 1. Evidence files crossing PS7/macOS through git must never CRLF-drift.
- Write semantics: append-only JSONL uses `O_APPEND` with one `Write` per record plus fsync (no partial lines, no rename needed). Derived, regenerable files (sheets, exports) use write-temp, remove-dest, rename, with the Windows rename-over-existing behavior handled in one shared helper from session 1.

## 9. Agent operating contract (summary; full CLAUDE.md in M2)

Agent MAY: write under `briefs/` and `critiques/`; invoke any `splatter` command; read everything in the workspace.

Agent MUST NOT: write under `runs/` or `packages/`; append to `verdicts.jsonl` except via `splatter verdict` at the operator's explicit direction; modify `providers.yaml`, `pricing.yaml`, or agent instruction files (propose changes for the operator to apply); state any cost, latency, or model metadata not read from a manifest in the same session.

The two-repo split enforces the tool boundary physically; this contract covers the workspace interior.

## 10. Build sequence

Sessions are 2-3 focused hours. Each exit criterion is testable; a session is not done until its criterion passes.

**M1: end-to-end evidence loop (4 sessions)**

- S1, substrate: splatter repo scaffold; schema types and JSONL round-trip tests; append and replace write helpers with Windows semantics; `init`, `status`, `validate`; `.gitattributes` generation; cross-compile both targets. Exit: `splatter init && splatter validate` passes on a fresh workspace on both OSes; schema round-trip tests green.
- S2, providers: `Provider` interface, instrumented RoundTripper, Gemini and OpenAI adapters, `providers.yaml` and `pricing.yaml` loading, `splatter gen`. Exit: `splatter gen` against each live provider produces images plus a manifest call record containing transport-measured latency, sourced cost, and a redacted raw sidecar.
- S3, loop surface: `splatter fan`, `splatter sheet`, `splatter verdict`. Sheet contents: header (project, run, iteration, brief excerpt); provider-grouped cards with thumbnail linking to full-res, provider and model badge, params and seed, latency, cost, critique verdict when present, per-image status; footer with run totals and parent-run link; pre-formatted copyable `splatter verdict` command block. Exit: one brief fanned across both providers; sheet opens from the filesystem on both OSes; verdict appends and `validate` passes.
- S4, sync and portability proof: `splatter push`/`pull`; then the M1 exit test in full: run a fan on Windows, push and commit, pull on macOS, rebuild the sheet, confirm identical manifests and working relative references. Exit: cross-machine handoff verified end to end.

**M2: orchestration layer (2-3 sessions)**: splats `CLAUDE.md` and skill; brief template; per-provider prompt-expansion conventions; critique rubric v1 and `validate` coverage of critique files; first real design session end to end. Exit: a design session produces briefs, a fan, an agent critique that validates, a sheet, and recorded verdicts with the agent touching only its permitted surface.

**M3: deliverable (1-2 sessions)**: `splatter export` artist package: selected images, brief, style notes, lineage summary of accepted and rejected directions with reasons, generated from manifests plus verdicts. Exit: a package a human artist could work from with no verbal briefing.

**M4: instrument hardening (1-2 sessions)**: `splatter report` (keep-rate, cost, latency per provider from evidence); third provider adapter as an abstraction test; pricing table maintenance. Exit: report output matches hand-computed figures from raw manifests.

## 11. Standing checks and triggers

- **Agent integrity**: if in two consecutive sessions the agent invents metrics instead of reading manifests, or writes outside its permitted surface, orchestration thins to a scripted loop over the same CLI. Substrate unchanged.
- **Critique value**: over the first five design sessions, compute critique-versus-operator agreement on keep/cull. Below roughly 65-70%, critique demotes from gatekeeper to advisory sort order on the sheet, and the skill layer shrinks accordingly.
- **UI promotion (compound)**: categorical arm: the workflow requires mask or region selection for image-to-image editing (build the interactive canvas immediately). Measured arm: more than a third of verdict notes contain spatial-reference prose across three consecutive sessions, counted from `verdicts.jsonl` (build the served review app). Until either arm fires, the static sheet is the review surface.

## 12. Deliberately unplanned

Per-provider prompt dialects, critique rubric content beyond v1's field names, mutation rules, report visual design, and providers beyond the third. These are what the instrument exists to learn empirically. Pre-planning them substitutes speculation for the evidence loop. Tooling earns existence through repeated friction.