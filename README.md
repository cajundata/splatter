# splatter

Splatter is a concept-refinement instrument for T-shirt designs. It generates
image concepts across multiple cloud providers from identical briefs, records
evidence (cost, latency, model metadata) at call time, and produces a
reviewable sheet so an operator can keep or cull concepts and route the
survivors toward a human artist. It is not a print-quality image generator —
it's the harness that turns "try this idea against three providers" into a
comparable, auditable trail instead of a pile of loose PNGs and vibes.

Provider comparison is a first-class function: keep-rate, cost, and latency
per provider are computable from manifests alone, never narrated or
estimated by a human or an agent in the loop.

## Overview

Splatter is one half of a two-repo pair:

- **splatter** (this repo) — the harness. Go source, tests, release tooling.
  Cross-compiled to `windows/amd64` and `darwin/arm64`; the binary lives on
  PATH on both machines.
- **splats** (separate repo) — the workspace. Briefs, run evidence,
  verdicts, critiques, provider profiles, and agent instructions. No Go
  source, just data that gets synced between machines.

The split is deliberate: splatter is the tool, splats is the material the
tool operates on. A design session runs an agent (Claude Code) inside a
splats workspace with `splatter` on PATH — the agent writes briefs and
critiques, and calls `splatter` for everything else. It never touches
`runs/` or `packages/` directly, and every cost or latency claim it makes
has to trace back to a manifest record from that session.

## Current status

M1 ("end-to-end evidence loop") is functionally complete except for
cross-machine sync:

| Command | Status |
| --- | --- |
| `splatter init` | done — scaffolds a workspace or project |
| `splatter status` | done |
| `splatter validate` | done — schemas, hashes, critique files |
| `splatter gen` | done — single-profile generation |
| `splatter fan` | done — one brief across a provider set |
| `splatter sheet` | done — self-contained HTML review sheet |
| `splatter verdict` | done — keep/cull/notes, append-only |
| `splatter push` / `pull` | not yet built |
| `splatter export` | not yet built (M3) |
| `splatter report` | not yet built (M4) |
| `splatter providers` | not yet built |

Two provider adapters exist today: Gemini and OpenAI image generation, both
behind an instrumented `http.RoundTripper` that measures real wall-clock
latency and captures redacted raw responses. Windows is the actively
verified target (see `docs/windows-checklist.md`); the macOS build compiles
and cross-builds but hasn't been the primary dev machine yet.

## Workflow

```
brief  →  splatter fan  →  splatter sheet  →  splatter verdict  →  (splatter export)
```

1. Write a brief (Markdown + YAML front matter: concept, mood, motifs,
   exclusions, aspect).
2. `splatter fan` sends it to every provider in a profile set, writing an
   append-only `manifest.jsonl` call record per invocation — cost, latency,
   model metadata, a sha256'd image, and a redacted raw wire sidecar.
3. `splatter sheet` renders a static HTML review page from that manifest —
   provider-grouped thumbnails, params, cost, latency, a copy-pasteable
   verdict command. Opens from disk on either OS, no server.
4. `splatter verdict` records keep/cull decisions and notes against the run.
5. (Once built) `splatter export` turns the accepted concepts plus their
   verdict/critique trail into a package a human artist can work from
   without a verbal briefing.

Every step's output is evidence: nothing about cost, latency, or provider
behavior is asserted anywhere except by reading it back out of a manifest.

## Where it's going

The end state is a fully evidence-driven, agent-assisted concept pipeline,
built in three more milestones on top of the working M1 loop:

- **M2 — orchestration layer**: a `splats/CLAUDE.md` operating contract and
  skill layer so a design session runs end to end through an agent —
  briefs and critiques written by the agent, everything else invoked
  through `splatter`, critique rubric v1 checked against operator verdicts
  for agreement.
- **M3 — deliverable**: `splatter export` assembles the artist package —
  selected images, brief, style notes, and a lineage summary of accepted
  and rejected directions with reasons, generated purely from manifests and
  verdicts.
- **M4 — instrument hardening**: `splatter report` (keep-rate, cost,
  latency per provider computed from evidence), a third provider adapter as
  an abstraction test, and pricing-table maintenance.

Cross-machine sync (`splatter push`/`pull` against DigitalOcean Spaces,
content-addressed by sha256) is the remaining piece of M1 and lands before
M2 orchestration work begins.

Deliberately unplanned: per-provider prompt dialects, critique rubric
content beyond v1's field names, mutation/edit rules, report visual design,
and providers beyond the third. These are what the instrument exists to
learn empirically — see `docs/splatter_spec.md` §12.

## Use case

Splatter exists for one recurring workflow: an operator has a T-shirt
concept in mind, wants to see how 2-3 image models render the same brief,
and needs a fast, low-ceremony way to compare them on cost, latency, and
output quality before committing an idea to a human artist for print-ready
finishing. It's built for iteration across sessions and machines, not for
one-off single-image generation.

## Build & run

Requires Go 1.26+. From the repo root:

```
make build       # bin/splatter (host OS/arch)
make build-all    # bin/windows-amd64 and bin/darwin-arm64
make test
```

On Windows, `.\scripts\install.ps1` builds and registers `splatter.exe` on
the user PATH (first run only needs a new shell after).

Provider calls need `GEMINI_API_KEY` and `OPENAI_API_KEY` as environment
variables — never written to config files or committed. See
`docs/windows-checklist.md` and `docs/live-check.md` for step-by-step
verification scripts.

## Docs

- `docs/splatter_spec.md` — full build spec: schemas, CLI grammar, provider
  interface, sync design, build sequence, standing checks
- `docs/windows-checklist.md` — Windows install and verification steps
- `docs/live-check.md` — live provider-call exit-criterion scripts
