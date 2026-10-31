# tools

Small single-file tools. Each folder holds one tool; the binary or script has the folder's name.

| Tool | Language | What it does |
| --- | --- | --- |
| `claude-status` | C (libcurl, cJSON) | Prints Claude plan usage limits without starting a session. |
| `codex-status` | Go | Prints ChatGPT Codex quotas without starting a model turn. |

## Use

```sh
make            # build every tool
make install    # copy them to ~/.local/bin
make clean      # delete built binaries
```

## Add a tool

1. Create `<name>/` with the source or script.
2. Add `<name>` to `TOOLS` in `Makefile`, plus a build rule if it needs compiling.
3. Add the built binary to `.gitignore`.
