# codex-status

`codex-status` reads `auth.json` from the Codex home directory but never writes
it. It requests quotas from an undocumented endpoint that may change. If the
direct request fails, it warns and falls back to `codex app-server`. Use
`--fallback` to skip the direct request. `--json` prints the raw response from
the successful path; the direct and app-server response shapes differ.
