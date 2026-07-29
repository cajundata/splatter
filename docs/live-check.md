# S2 Live Exit-Criterion Check (operator-run)

Automated tests never touch provider APIs. This script is the live half of
S2's exit criterion. Cost: one Gemini call + one OpenAI call (~$0.08).

In a fresh shell (keys never enter files or the repo):

```shell
export GEMINI_API_KEY=...        # from your key store
export OPENAI_API_KEY=...

cd <your workspace>              # or: mkdir ws && cd ws && splatter init
splatter init demo               # if the project doesn't exist yet
cat > projects/demo/briefs/b_001.md <<'EOF'
---
id: b_001
project: demo
concept: minimal contour-line mountain range, single-color print
aspect: square
---
Sparse, geometric, one accent line. No text.
EOF

splatter gen --brief projects/demo/briefs/b_001.md --profile gemini-baseline
splatter gen --brief projects/demo/briefs/b_001.md --profile openai-baseline
splatter validate
```

Confirm for EACH run directory under projects/demo/runs/:

- [x] images/ contains PNG(s) that open
- [x] manifest.jsonl call record has latency_ms > 0, http_status 200
- [x] cost.source is "table:<version>" (or "reported"), never untagged
- [x] raw/c_01.json exists and contains "$blob" instead of image base64
- [x] `splatter validate` exits 0

Then unset the keys: `unset GEMINI_API_KEY OPENAI_API_KEY`

# S3 Live Exit-Criterion Check (operator-run, macOS)

Cost: one Gemini call + one OpenAI call (~$0.08). Same key setup and demo
workspace as the S2 check above.

```shell
splatter fan --brief projects/demo/briefs/b_001.md --set baseline
splatter sheet --run <run id> --open
splatter verdict --run <run id> --keep <an image id> --note "<image id>: crisp test" --note "good test"
splatter validate
```

Confirm:

- [x] fan exits 0 with one call per profile recorded in one manifest
- [x] the sheet opens from the filesystem: provider groups, thumbnails
      linking to full-res, cost with source tag, copyable verdict block
- [x] the verdict appends to projects/demo/verdicts.jsonl and validate exits 0
- [x] rebuilding the sheet after the verdict shows keep status on the image

Then unset the keys: `unset GEMINI_API_KEY OPENAI_API_KEY`
