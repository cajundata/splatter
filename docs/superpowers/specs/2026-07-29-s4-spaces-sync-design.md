# S4 Spaces Sync — Design

Date: 2026-07-29
Scope: Milestone M1, Session S4 of [the splatter spec](../../splatter_spec.md) (§5 push/pull grammar, §7 Spaces sync, §10 S4 portability proof). The spec's locked decisions govern: env-var config, flat `blobs/<sha256>` key layout, idempotent-by-construction sync, nothing auto-deletes in v0.1.

## Goal

`splatter push` uploads image blobs referenced by local manifests and absent remotely; `splatter pull` downloads referenced blobs absent locally. Manifests, verdicts, and briefs travel through git (run images are gitignored since S1); blobs travel through DigitalOcean Spaces. Machine handoff is `git pull && splatter pull` — the entire protocol.

**Exit criterion (from spec):** the M1 exit test in full — run a fan on Windows, push and commit, pull on macOS, rebuild the sheet, confirm identical manifests and working relative references. Cross-machine handoff verified end to end.

## Decisions made this cycle

- **Client: hand-rolled SigV4 over `net/http`.** Zero new dependencies. Only three operations are needed (HEAD, PUT, GET), all on flat keys. Signing is verified against AWS's published SigV4 test vectors.
- **Existence check: HEAD per candidate blob**, not LIST. No XML parsing, no query-string canonicalization; at v0.1 scale (dozens of images per session) the extra round-trips are negligible. LIST-based diffing is the future optimization if push ever feels slow.
- **Addressing: path-style** (`<endpoint>/<bucket>/blobs/<sha256>`). Spaces supports it; avoids virtual-host DNS assumptions.
- **Signed payloads on PUT.** The blob's sha256 is its key, so `x-amz-content-sha256` is free. GET/HEAD sign the empty-payload constant.
- **No retries, no parallelism, no multipart.** One attempt per blob, sequential; failures are collected and reported, and rerunning push/pull is idempotent, so the retry mechanism is the operator rerunning the command. Images are a few MB — far under the 5 GB single-PUT limit.
- **`status` reports sync honestly**: "configured"/"not configured" from the env, plus an offline per-project count of manifest-referenced images missing on disk (the pull-side "pending sync"; push-side pending requires the network and lives in `push` itself).

## internal/spaces (new package)

A minimal S3-compatible client. No filesystem knowledge.

```go
type Config struct {
	Endpoint  string // SPLATTER_S3_ENDPOINT, e.g. https://nyc3.digitaloceanspaces.com
	Bucket    string // SPLATTER_S3_BUCKET
	AccessKey string // SPLATTER_S3_ACCESS_KEY
	SecretKey string // SPLATTER_S3_SECRET_KEY
	Region    string // SPLATTER_S3_REGION; optional, see derivation below
}

// FromEnv reads the SPLATTER_S3_* vars. Error names every missing var at
// once. Region default: first hostname label of the endpoint
// (nyc3.digitaloceanspaces.com → nyc3), else "us-east-1".
func FromEnv() (Config, error)

type Client struct{ /* Config + *http.Client */ }

func New(cfg Config) *Client
func (c *Client) Head(ctx context.Context, key string) (exists bool, err error)
func (c *Client) Put(ctx context.Context, key string, body []byte, payloadSHA256 string) error
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error)
```

- `sigv4.go`: canonical request → string-to-sign → signature, `service=s3`, single-chunk. The signing function takes the request, credentials, region, payload hash, and an explicit time (testability).
- HEAD returns `(false, nil)` on 404, `(true, nil)` on 200, error otherwise (403 and 5xx are errors with status and any response body excerpt).
- PUT sends `Content-Type: image/png` (all v0.1 blobs are PNGs per manifest) and the blob's sha as both key suffix and payload hash.
- The client is endpoint-agnostic: tests point it at an `httptest` server over plain HTTP.

## internal/workspace: blob inventory

```go
type BlobRef struct {
	SHA256 string
	Paths  []string // workspace-relative, slash-normalized, ≥1; multiple when several manifests reference one sha
}

// BlobRefs scans projects/*/runs/*/manifest.jsonl and collects
// images[].sha256 → file paths (joined run-dir-relative onto the run dir).
// Malformed manifest lines are errors — sync must not silently skip evidence.
func BlobRefs(root string) ([]BlobRef, error)
```

Deterministic order (sorted by sha) so push/pull output and tests are stable. Raw sidecars' `$blob` references are the same image bytes and add no keys.

## Commands

### cmd/splatter/push.go

For each `BlobRef`, resolve the first path that exists locally:

1. No path exists on disk → count as `missing_local` (reported; the file may live on the other machine; not a failure).
2. Re-hash the local file. Hash ≠ manifest sha → `failed` (evidence integrity violation; never uploaded).
3. `Head(blobs/<sha>)` exists → `skipped`.
4. Else `Put` → `uploaded`.

### cmd/splatter/pull.go

For each `BlobRef`, for each recorded path missing on disk: `Get(blobs/<sha>)` once per sha, hash while streaming to a temp file, and only `fsio.ReplaceFile`-rename into place when the hash matches the key. Hash mismatch or 404 → `failed`, temp removed, no partial file ever lands at a manifest-referenced path. One downloaded blob satisfies all its missing paths. Already-present paths → `skipped`.

### Shared semantics

- Nothing deletes, ever (spec §7 GC policy).
- Per-blob errors are collected; the command attempts every blob, prints failures to stderr, and exits 1 if any failed, 0 otherwise.
- Missing/invalid `SPLATTER_S3_*` config → runtime error, exit 1 (matching S2's missing-API-key behavior). Unknown flags/args → usage, exit 2.
- `--json` emits the result object; human output is a one-line-per-category summary.

```go
type SyncResult struct {
	Uploaded     int         `json:"uploaded"`     // push only
	Downloaded   int         `json:"downloaded"`   // pull only
	Skipped      int         `json:"skipped"`
	MissingLocal int         `json:"missing_local"` // push only
	Failed       []SyncError `json:"failed"`        // sha + message
}
```

### cmd/splatter/status.go

- `Sync` field: `"configured"` when `spaces.FromEnv()` succeeds, else `"not configured"` (replaces the S4 stub).
- Per-project `missing_images` count: manifest-referenced image paths absent on disk. Zero network.

## Testing

- **sigv4:** unit tests against AWS's published SigV4 test-vector values (fixed time, known keys → known signature); header canonicalization and payload-hash cases.
- **spaces client + commands:** an `httptest` fake Spaces server (in-memory `map[key][]byte`, HEAD/PUT/GET; asserts every request carries an `AWS4-HMAC-SHA256` Authorization header naming the configured access key — full signature verification is the sigv4 unit tests' job). End-to-end: push uploads only absent blobs; re-push uploads nothing (idempotence); pull materializes missing files at all recorded paths; corrupt remote blob → pull fails that sha, writes nothing; local file/manifest sha mismatch → push refuses; missing local file → reported, exit 0.
- **CLI:** follows the existing `cli_test.go` pattern; env-var config errors (exit 1, message names all missing vars); `status` sync field and missing-images count.
- **Live half (operator-run):** extend `docs/live-check.md` with the S4 section — the full M1 exit test against a real Spaces bucket: fan on Windows, `splatter push` + git commit/push; on macOS `git pull && splatter pull`, `splatter sheet --run <id> --open`, confirm images render and `splatter validate` passes; `diff` the manifests for identity. Extend `docs/windows-checklist.md` with the Windows-side S4 items.

## Out of scope for S4

LIST-based push diffing; parallel transfers; retry/backoff; multipart upload; `splatter gc` (spec §7 defers it); network-aware pending-push in `status`; syncing anything other than manifest-referenced image blobs (sheets are regenerable; raw sidecars, manifests, verdicts, briefs, critiques travel via git).
