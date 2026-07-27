# S3 Loop Surface — Design

Date: 2026-07-27
Scope: Milestone M1, Session S3 of [the splatter spec](../../splatter_spec.md) (§5 fan/sheet/verdict grammar, §4.3 verdict records, §10 S3 sheet contents), plus the S2 ride-along backlog as an opening polish task. The spec's locked decisions govern.

## Goal

The critique loop's surface: `splatter fan` (one brief, one run, one call per profile), `splatter sheet` (self-contained static HTML review surface in the run directory), `splatter verdict` (validated append to `verdicts.jsonl`).

**Exit criterion (from spec):** one brief fanned across both providers; sheet opens from the filesystem on both OSes; verdict appends and `validate` passes. macOS verified live; Windows via the standing checklist (extend `docs/windows-checklist.md` with S3 items).

## Decisions made this cycle

- **Scope:** includes the S2 backlog as a pre-task (below); openai `ModelReturned` semantics stay parked for M4's report design.
- **Fan shape:** unify orchestration in `internal/run` (extract per-call execution from `Gen`; `Gen` becomes `Fan` with one profile), sequential calls. Concurrency revisited when a fan spans 3+ providers.
- **Pre-flight seam:** optional `Preflight(provider.Request) error` method on concrete adapters, discovered by type assertion in `run` — the spec-frozen `Provider` interface (§6.1) is untouched. Pre-flight runs the adapter's config-stage checks with zero network.
- **Thumbnails:** CSS-scaled full-res images hyperlinked to themselves; no separate thumbnail files.
- **Sheet is a derived file:** written via `fsio.ReplaceFile`, regenerable at any time.

## Substrate polish pre-task (S2 backlog)

- `run`: config-stage detection via `errors.As` instead of direct type assertion; doc note on `GenParams.Root` expecting an absolute path.
- `openai` adapter: dedicated partial-evidence tests for the bad-base64 and undecodable-image decode branches.
- `config`: `Resolve` errors carry the providers.yaml path (thread the path into the `Providers` struct at load time).
- `gen` command: nonexistent `--brief` file maps to a usage error (exit 2) via `os.IsNotExist`.

## Run refactor and fan

### internal/run

- Extract `executeCall(ctx, runDir, callID string, profileID string, prof config.Profile, prov provider.Provider, req provider.Request, pricing *config.Pricing) (schema.CallRecord, error)` — the wire call plus evidence writes (images, redacted sidecar, call record assembly). The error return is for I/O failures only; provider failures land inside the record.
- New:

```go
type ResolvedProfile struct {
	ID       string
	Profile  config.Profile
	Provider provider.Provider
}

type FanParams struct {
	Root      string
	BriefPath string
	Profiles  []ResolvedProfile // one call per entry, in order
	N         int
	Harness   string
	Pricing   *config.Pricing
}

type CallOutcome struct {
	Call      string            `json:"call"`
	Profile   string            `json:"profile"`
	Provider  string            `json:"provider"`
	Images    []schema.ImageRef `json:"images"`
	Cost      schema.Cost       `json:"cost"`
	LatencyMS int64             `json:"latency_ms"`
	Failed    bool              `json:"failed"`
	Error     *schema.CallError `json:"error,omitempty"`
}

type FanResult struct {
	Project   string        `json:"project"`
	Run       string        `json:"run"`
	Calls     []CallOutcome `json:"calls"`
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
}

func Fan(ctx context.Context, p FanParams) (*FanResult, error)
```

- `Fan` flow: read/parse brief → project scaffolded → containment → **pre-flight every profile** (capability checks: n ≥ 1, n ≤ MaxBatch, seed unsupported; plus adapter `Preflight` when implemented) → any failure aborts with an error and zero disk trace → allocate run, write header → sequential `executeCall` per profile (`c_01`, `c_02`, …) → result.
- A config-stage error from `Generate` after a passing pre-flight should be impossible; if it occurs anyway, it is recorded as a failed call (the header is already written; evidence is preserved rather than destroyed).
- `Gen(ctx, GenParams)` becomes a thin wrapper over `Fan` with one profile; `GenResult` shape preserved for the `gen` command.

### Adapters

- Both adapters gain `Preflight(req provider.Request) error` running exactly their existing config-stage checks (key presence, aspect mappable, n range, seed, native-key allowlist), sharing the check code with `Generate` (extract a common `validateRequest` inside each adapter package). No network, no filesystem.

### cmd/splatter fan.go

- `fan --brief <path> [--set <set> | --profiles a,b] [-n N]`; exactly one of `--set`/`--profiles` (usage error otherwise); set membership resolves via `providers.yaml` (unknown set/profile = usage error). Adapters built per profile via the existing `buildProvider` seam (reused for testability).
- Exit codes: 0 if at least one call succeeded, 1 only if all calls failed (spec §5). Flag misuse (both/neither of `--set`/`--profiles`, unknown set or profile, outside workspace) → usage error (2). Pre-flight failures (capability violation, missing key, bad native key) → runtime error (1), matching S2's capability-check behavior in `gen`. `--json` emits `FanResult`.

## Verdict

### cmd/splatter verdict.go

Run location lives in `internal/workspace`: `FindRun(root, runID string) (project string, err error)` scans `projects/*/runs/<runID>`, wrapping zero-match and multi-match cases in distinguishable errors (both surfaced as usage errors by commands). `verdict` and `sheet` share it.

- `verdict --run <id> [--keep ids] [--cull ids] [--note "<text>"]... [--session <label>]`
- IDs comma-separated. Notes repeatable; a note matching `^([a-z0-9_]+):\s` with a leading image ID is an image note; otherwise run-level (`image: null`). The spec-locked convention: note without "id:" prefix = run-level.
- Run located by scanning `projects/*/runs/<id>`; found in zero projects → usage error; found in more than one → usage error naming the candidates.
- Validation before append (all violations are usage errors; nothing is appended): every ID in keep/cull/notes exists in the run's manifest images; keep ∩ cull empty; at least one of keep/cull/note present.
- Record: `v:1, type:"verdict", run, ts (UTC now), session (default local calendar date YYYY-MM-DD, `--session` overrides), keep, cull, notes`. Appended via `fsio.AppendRecord` to the owning project's `verdicts.jsonl`. Empty keep/cull serialize as `[]`, not null.
- `--json` emits the appended record.

## Sheet

### internal/sheet

- `Build(root, project, runID string) (*Data, error)` reads `manifest.jsonl`, `verdicts.jsonl` (all records for the run; later records win per image), and `critiques/<run>.json` if present; assembles a render-ready `Data` struct (header, provider groups, cards, footer, verdict command block).
- `Render(w io.Writer, d *Data) error` executes one embedded `html/template` (`//go:embed sheet.tmpl.html`). Inline CSS, no JavaScript, no external assets. All image hrefs relative (`images/c_01_0.png`).
- Card content per spec: thumbnail (CSS-scaled full-res `<img>` wrapped in a link to the same file), provider + model badge (`model_returned`, falling back to `model_requested`), params (n, aspect requested → `aspect_actual`, seed), latency ms, cost with source tag, critique verdict + scores when present, per-image keep/cull status from verdicts. Failed calls render as error cards (stage, http status, message, latency) inside their provider group.
- Header: project, run, iteration, brief ID + concept (from the brief file named in the header; if the brief is missing locally, render the hash and a note instead of failing).
- Footer: totals (image count, summed cost over non-null USD with "N unavailable" note, summed latency), parent-run link `../<parent_run>/sheet.html` when `parent_run` is set.
- Verdict block: `<pre>` containing `splatter verdict --run <id> --keep <all image ids comma-joined>` — the operator edits IDs into `--cull` as needed.

### cmd/splatter sheet.go

- `sheet --run <id> [--open]`: locate run (same scan as verdict), `Build` + render to a buffer, write `sheet.html` into the run directory via `fsio.ReplaceFile`, print the path (or `--json` `{run, path}`).
- `--open`: calls `openInBrowser(path)` — the codebase's single permitted `runtime.GOOS` branch, isolated in one helper: darwin → `open <path>`; windows → `cmd /c start "" <path>`. No other GOOS conditionals anywhere.

## Testing

- **run/fan:** fake providers (reuse the S2 pattern): mixed success/failure fan → one run, two records, exit semantics; all-fail → command exit 1; pre-flight failure (fake with failing Preflight) → error, `runs/` empty; sequential call IDs c_01/c_02; Gen-wrapper regression (existing Gen tests keep passing unchanged).
- **adapters:** Preflight unit tests mirroring the config-error table (no network); Generate's checks stay (defense in depth).
- **verdict:** CLI tests — append then decode via schema and `validate` passes; unknown image ID, keep∩cull overlap, ambiguous run, empty verdict → usage errors with nothing appended; session default vs `--session`.
- **sheet:** golden-substring tests over rendered HTML for fixture runs (success + failed call, with and without critique/verdicts): provider grouping, badges, cost source tags, error card, verdict command block, parent link, relative image paths; regeneration via `ReplaceFile` is idempotent. `--open` excluded from automated tests; verified live (macOS) and via the Windows checklist.
- **Windows checklist:** extended with S3 items (fan on Windows, sheet double-click opens in default browser, `--open` works, verdict appends CRLF-free).

## Out of scope for S3

Spaces push/pull (S4); export packages (M3); report (M4); critique authoring and rubric content (M2 — the sheet only *renders* critiques when files exist); refinement lineage creation (parent_run stays null in fan-created headers until textual refinement arrives with M2 workflows; the sheet's parent-link rendering is ready for it); sheet visual polish beyond the spec's content list (deliberately unplanned, spec §12).
