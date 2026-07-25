# S1 Windows Verification Checklist

Run in PowerShell 7 on the Windows machine with
`bin/windows-amd64/splatter.exe` copied somewhere on PATH as
`splatter.exe`. Report results back; S1's exit criterion is not fully met
until every box is checked.

- [ ] `mkdir ws; cd ws; splatter init` — scaffold listing, exit code 0
      (`$LASTEXITCODE`)
- [ ] `splatter validate` — prints `ok: <path>`, exit code 0
- [ ] `splatter init` again — reports all files as existing, exit 0
- [ ] `splatter init gradient-descent` — creates project, exit 0
- [ ] `splatter status --json` — one JSON object, `"sync":"not configured"`
- [ ] `splatter nonsense` — error to stderr, exit code 2
- [ ] Open `.gitattributes` in an editor that shows line endings — file
      content contains `eol=lf`
- [ ] `git init; git add -A; git commit -m x` inside ws, then
      `git ls-files --eol` — all text files show `i/lf`
- [ ] On the M-series Mac: run `bin/darwin-arm64/splatter init && splatter validate`
      in a fresh directory once — the arm64 artifact was format-verified but not
      executed during S1 (build host was x86_64)
