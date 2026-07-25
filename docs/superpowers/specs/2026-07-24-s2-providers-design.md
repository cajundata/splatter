# S2 Providers — Design

Date: 2026-07-24
Scope: Milestone M1, Session S2 of [the splatter spec](../../splatter_spec.md) (§6 provider layer, §5 `gen`), plus a small substrate-polish pre-task carried from S1's final review. The spec's locked decisions govern.

## Goal

Live image generation with evidence: the `Provider` interface, instrumented transport, fresh Gemini and OpenAI adapters, config loading, and `splatter gen` producing images plus a complete manifest call record (transport-measured latency, sourced cost, redacted raw sidecar).

**Exit criterion (from spec):** `splatter gen` against each live provider produces images plus a manifest call record containing transport-measured latency, sourced cost, and a redacted raw sidecar. Decision: automated tests use local fake servers only; the operator runs the live check with their own keys from a provided command script and confirms results. Keys never enter the transcript, config files, or code.

## Decisions made this cycle

- **Scope:** includes the S1 polish backlog as an opening task (details below).
- **Models:** `gemini-baseline` → `gemini-2.5-flash-image`; `openai-baseline` → `gpt-image-1`. Config values, changeable without code.
- **Architecture:** package-per-adapter. Per-adapter error normalization is enforced by package boundaries; no registry/factory indirection at two providers (revisit at M4's third adapter).
- **Live verification:** operator-run, at session end.

## Architecture

```
internal/
├── provider/          # Provider iface, Request/Result/Capabilities,
│   │                  #   Image, Cost, CallMeta, Error, aspect vocabulary
│   ├── gemini/        # google.golang.org/genai adapter + its normalizer
│   └── openai/        # openai-go/v3 adapter + its normalizer
├── transport/         # instrumented http.RoundTripper (Recorder)
├── config/            # providers.yaml + pricing.yaml load/validate
├── run/               # orchestration: run-ID alloc, evidence writes,
│                      #   raw-sidecar redaction, cost resolution
└── (existing: fsio, schema, workspace)
cmd/splatter/gen.go    # thin command
```

Evidence-writing lives only in `internal/run`. Adapters return bytes and metadata; they never touch the filesystem. S3's `fan` reuses `internal/run` with multiple profiles.

## Data flow: `splatter gen --brief <path> --profile <id> [-n N]`

1. Find workspace root; parse + hash brief (`schema.ParseBrief`, `schema.BriefSHA256`); load config; resolve profile.
2. **Capability check before any API call**: n ≤ MaxBatch, aspect supported, seed only if supported. Unsupported operation = error, never a silent downgrade (n defaults to 1 in `gen` if not given; the spec's `-n 4` example applies where the provider supports it).
3. Allocate next run ID `r_NNNN` (scan `projects/<p>/runs/`, max+1, zero-padded), create run dir, append run header via `fsio.AppendRecord` (`harness: "splatter v<version>"` from the build-stamped version).
4. Construct adapter with API key from env (`GEMINI_API_KEY` / `OPENAI_API_KEY`) as a constructor parameter; missing key is a clear runtime error before any call. Inject an instrumented HTTP client.
5. `Generate(ctx, req)`. Transport measures wall-clock latency around the HTTP exchange and captures the raw response body; request headers are never stored.
6. Success: write `images/c_01_<i>.png`, sha256 each, write redacted `raw/c_01.json`, resolve cost, append call record atomically.
7. Failure: append call record with `error` populated, images empty, latency/status recorded if the call reached the wire; `gen` exits 1.

## Provider interface

As specified in spec §6.1 (`Provider`, `Capabilities`, `Request`, `Result`) — implemented verbatim in `internal/provider`, plus:

- `provider.Error{Stage, HTTPStatus, Message}` — the normalized error type both adapters produce; maps directly onto the manifest's `error` object. Stages: `"config"`, `"request"`, `"decode"`.
- Harness aspect vocabulary (`square`, `portrait_4_5`, `portrait_2_3`, `landscape_4_3`) is defined once in `internal/schema` (S1) and referenced here; each adapter maps it to native vocabulary and reports the native form for `aspect_actual`.

## Adapters

### Gemini (`internal/provider/gemini`)

- SDK: `google.golang.org/genai`. Starshp's `gemini.go` is pattern reference only (image-mode config: `responseModalities` TEXT+IMAGE, no tools alongside image output, `InlineData` parsing; parameter-injected auth and injectable base URL). No Starshp imports, no streaming shape.
- Aspect map: `square`→`1:1`, `portrait_4_5`→`4:5`, `portrait_2_3`→`2:3`, `landscape_4_3`→`4:3` via generation-config `aspectRatio`.
- `Capabilities{Img2Img: false, Edit: false, Seed: false, MaxBatch: 1, AspectModes: ["1:1","4:5","2:3","4:3"]}`. MaxBatch 1 is honest: the API returns one image per call for this model; `-n 4` fails the capability check instead of silently looping.
- Cost: the API reports no dollar figure → `table:` or `unavailable`, never fabricated.
- Base URL injectable (genai `HTTPOptions`) for httptest.

### OpenAI (`internal/provider/openai`)

- SDK: `openai-go/v3`, `client.Images.Generate`, synchronous, base64 response format.
- Aspect map to size strings: the API offers three sizes, so `square`→`1024x1024`, `landscape_4_3`→`1536x1024`, and both portrait aspects map to `1024x1536` — the spec's nearest-native-mode rule, with `aspect_actual` recording the native size actually requested. Exact strings cross-checked against SDK constants during implementation.
- `Capabilities{Img2Img: false, Edit: false, Seed: false, MaxBatch: 10, AspectModes: ["1024x1024","1024x1536","1536x1024"]}` (10 is the images API's documented n limit). n passed natively.
- Cost: parse response `usage` if it yields a priceable figure, else table.
- Base URL via `option.WithBaseURL` for httptest.

### Both

- `Native` map from the profile is merged verbatim into the request and recorded verbatim in the manifest.
- Error normalization is per-adapter, inside the adapter package, producing `provider.Error`. No shared normalizer importing SDK types (the Starshp audit's trap).
- v1 declares `Seed: false` for both (neither current API accepts one); a `--seed`-style request therefore cannot arise in S2 since `gen` per spec grammar has no seed flag.

## Transport (`internal/transport`)

One `Recorder` per `Generate` call, wrapping `http.DefaultTransport`:

- Wall-clock latency measured immediately around `RoundTrip`.
- Full response body captured (read, stored, re-wrapped so the SDK still consumes it).
- Captures HTTP status and the provider request-ID header (adapter supplies the header name).
- Request headers are never stored — the recorder struct has no field for them. This makes the no-credentials-on-disk rule structural.

## Raw sidecar redaction (`internal/run`)

Provider-agnostic JSON walk of the captured body: any string value that is valid base64 decoding to more than 4KB is replaced with `{"$blob":"<sha256-of-decoded-bytes>","bytes":N}`. The sha256 matches the stored image's hash, keeping the sidecar auditable against `images/`. Non-JSON bodies are stored verbatim (fallback preserves evidence; both current APIs return JSON).

## Config (`internal/config`)

- `providers.yaml` per spec §6.4: `version: 1`, `profiles: {id: {provider, model, native}}`, `profile_sets`. Unknown provider names, missing models, or malformed YAML are runtime errors (exit 1) with file/field context.
- `pricing.yaml`: `version: "<YYYY-MM-DD>.<n>"` (string, bumped on every edit; lands in `cost.source` as `table:<version>`), `models: {<model-id>: {usd_per_image: <float>}}`. Lookup by `model_returned`, falling back to `model_requested`; no entry → `{"usd":null,"source":"unavailable"}`.
- Initial stub content in `workspace.ScaffoldWorkspace` is updated to include the two baseline profiles and both models' prices, so a fresh `init` yields a workable config.

## Cost resolution (`internal/run`)

Locked precedence from spec §4.2: `reported` (parsed from the wire response by the adapter) → `table:<pricing_version>` → `unavailable`. Untagged estimates prohibited; the resolver is the only code that constructs `Cost` for the manifest.

## Substrate polish pre-task (from S1 final review)

- validate: cross-check `header.Run` vs run directory name, and critique filename vs `crit.Run`.
- `ParseBrief`: closing fence without trailing newline parses correctly (currently misreported as unclosed).
- Scanner buffer size extracted to one constant; `sc.Err()` wrapped with file path context.
- status: `runs/r_*` filtered by IsDir; `sc.Err()` checked; redundant sort removed; scanner buffer aligned.
- schema: `ImageRef` W/H ≥ 1 and `AspectActual` non-empty validated for successful calls; tests for the required-subfields loop.
- cmd: table test for `exitCode()` mapping.

## Testing

- **Adapters:** httptest servers replaying fixture JSON captured from real API response shapes — success, API error (429/500 with provider error bodies), malformed JSON. Assert request shape (model, aspect/size, n, no tools for Gemini image mode), image decoding, error normalization.
- **Transport:** latency measured (fake server with deliberate delay), body captured, status/request-ID recorded, no header retention.
- **Redaction:** table tests — small strings untouched, >4KB base64 replaced with correct sha256/bytes, non-JSON passthrough.
- **run:** fake in-process provider; asserts run-ID allocation (fresh, sequential, gap-tolerant), header+call records validate via `schema`, failure records preserve successful siblings' files, sidecar hash matches image hash, cost precedence.
- **CLI:** `gen` end-to-end against a fake provider injected under the config layer; exit codes (success 0, provider failure 1, capability violation 1 with clear message, usage errors 2).
- **Live check (operator-run):** provided script: export keys, `gen` once per baseline profile against a real brief, then `splatter validate` — confirms the exit criterion.

## Out of scope for S2

`fan`, `sheet`, `verdict` (S3); push/pull (S4); img2img/edit operations; seed support; third provider and `report` (M4); prompt-expansion conventions (M2).
