# tools

Small single-file tools. Each folder holds one tool; the binary or script has the folder's name.

| Tool | Language | What it does |
| --- | --- | --- |
| `claude-status` | C (libcurl, cJSON) | Prints Claude plan usage limits without starting a session. |
| `codex-status` | Go | Reads ChatGPT Codex quotas directly, with a Codex app-server fallback. |
| `net-check` | Go | Reports route, gateway, name resolution, and TCP reachability. |
| `randint` | C | Prints a random integer from an inclusive range (default 0 to 10). |
| `rename-by-date` | Bash | Renames files in a directory to `0001.ext`, `0002.ext`, ... by creation date. |

## Use

```sh
make            # build every tool
make install    # copy them to ~/.local/bin
make clean      # delete built binaries
```

### codex-status

`codex-status` reads `auth.json` from the Codex home directory but never writes
it. It requests quotas from an undocumented endpoint that may change. If the
direct request fails, it warns and falls back to `codex app-server`. Use
`--fallback` to skip the direct request. `--json` prints the raw response from
the successful path; the direct and app-server response shapes differ.

### net-check

`net-check` requires Linux and the `ip` command. It uses `ping` when a route
has a gateway; a missing or unsuccessful ping is a warning because routers may
filter ICMP. Direct routes and tunnels do not require a gateway ping.

By default, the command resolves `example.com` through the system resolver and
opens a TCP connection to `1.1.1.1:443`. Name resolution can use hosts files or
caches. TCP success proves only reachability of that endpoint, not HTTPS success
or unrestricted internet access. Failure can be specific to the target provider.
Each diagnostic operation has its own timeout, defaulting to three seconds.

```sh
net-check -timeout 5s -dns-host example.com -target '[2606:4700:4700::1111]:443'
net-check -h
```

Exit status is 0 when local routing, system name resolution, and TCP succeed,
1 when a required diagnostic fails, and 2 for invalid options. Gateway warnings
do not affect the exit status.

## Add a tool

1. Create `<name>/` with the source or script.
2. Add `<name>` to `TOOLS` in `Makefile`, plus a build rule if it needs compiling.
3. Add the built binary to `.gitignore`.
