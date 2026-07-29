# S1 Windows Verification Checklist

Run in PowerShell 7 on the Windows machine with
`bin/windows-amd64/splatter.exe` copied somewhere on PATH as
`splatter.exe`. Report results back; S1's exit criterion is not fully met
until every box is checked.

- [x] `mkdir ws; cd ws; splatter init` — scaffold listing, exit code 0
      (`$LASTEXITCODE`)
- [x] `splatter validate` — prints `ok: <path>`, exit code 0
- [x] `splatter init` again — reports all files as existing, exit 0
- [x] `splatter init gradient-descent` — creates project, exit 0
- [x] `splatter status --json` — one JSON object, `"sync":"not configured"`
- [x] `splatter nonsense` — error to stderr, exit code 2
- [x] Open `.gitattributes` in an editor that shows line endings — file
      content contains `eol=lf`
- [x] `git init; git add -A; git commit -m x` inside ws, then
      `git ls-files --eol` — all text files show `i/lf`
- [x] On the M-series Mac: run `bin/darwin-arm64/splatter init && splatter validate`
      in a fresh directory once — the arm64 artifact was format-verified but not
      executed during S1 (build host was x86_64)

# S3 Windows Verification Checklist

Same setup as S1 (PS7, `splatter.exe` on PATH, workspace `ws` with project
`gradient-descent` and brief `b_001.md`). Set `GEMINI_API_KEY` and
`OPENAI_API_KEY` in the session for the fan step.

To install or update `splatter.exe` (requires Go and this repo cloned):
`git pull; .\scripts\install.ps1` from the repo root — first run registers
`%LOCALAPPDATA%\splatter\bin` on the user PATH (open a new shell after),
later runs rebuild the exe in place. Check with `splatter --version`.

- [x] `splatter fan --brief projects/gradient-descent/briefs/b_001.md --set baseline`
      — per-call lines print; exit 0 with at least one success (`$LASTEXITCODE`)
- [x] `splatter fan --brief projects/gradient-descent/briefs/b_001.md` — usage
      error (neither --set nor --profiles), exit 2
- [x] `splatter sheet --run <run id>` — prints the sheet path, exit 0
- [x] Double-click `sheet.html` in Explorer — opens in the default browser
      from the filesystem; thumbnails render and click through to full-res
- [x] `splatter sheet --run <run id> --open` — browser opens (`cmd /c start`)
- [x] `splatter verdict --run <run id> --keep <image id> --note "solid direction"`
      — exit 0; `splatter verdict --run <run id> --keep nope_id` — exit 2
- [x] `splatter validate` — exit 0
- [x] `git add -A; git commit -m s3; git ls-files --eol` — `verdicts.jsonl`
      and `manifest.jsonl` show `i/lf` (no CRLF drift from verdict appends)

# S4 Windows Verification Checklist

- [ ] `splatter push` with env vars set — uploads, exit 0, `$LASTEXITCODE` 0
- [ ] `splatter push` again — `uploaded: 0`, all skipped, exit 0
- [ ] `splatter push` with `$env:SPLATTER_S3_SECRET_KEY` removed — error names
      the missing var, exit 1
- [ ] `splatter status --json` — `"sync":"configured"` with vars set,
      `"sync":"not configured"` without
- [ ] `splatter pull` after deleting one local PNG — file restored,
      `splatter validate` exit 0
