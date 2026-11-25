# tools

Small single-file tools. Each folder holds one tool; the binary or script has the folder's name.
Tools with a linked name have more detail in their folder's README.

| Tool | Language | What it does |
| --- | --- | --- |
| `claude-status` | C (libcurl, cJSON) | Prints Claude plan usage limits without starting a session. |
| [`codex-status`](codex-status/README.md) | Go | Reads ChatGPT Codex quotas directly, with a Codex app-server fallback. |
| `fix-keyring` | Bash | Adds `--password-store=gnome-libsecret` to the Claude Desktop and Signal launchers. |
| [`net-check`](net-check/README.md) | Go | Reports route, gateway, name resolution, and TCP reachability. |
| `randint` | C | Prints a random integer from an inclusive range (default 0 to 10). |
| `rename-by-date` | Bash | Renames files in a directory to `0001.ext`, `0002.ext`, ... by creation date. |
| `softupdate` | Bash | Updates pacman, flatpak, rustup, and cargo packages, then runs `fix-keyring`. |
| `stgz` | Bash | Extracts a `.tar.gz`, `.tgz`, or `.tar` archive. Without an extension, extracts `NAME.tar.gz`. |
| `stgzrm` | Bash | Same as `stgz`, then deletes the archive. |
| `tgz` | Bash | Packs a file or directory into `NAME.tar.gz`. |
| `tgzrm` | Bash | Same as `tgz`, then deletes the original. |

## Use

```sh
make            # build every tool
make install    # copy them to ~/.local/bin
make clean      # delete built binaries
```

## Add a tool

1. Create `<name>/` with the source or script.
2. Add `<name>` to `TOOLS` in `Makefile`, plus a build rule if it needs compiling.
3. If it needs compiling, add the built binary to `.gitignore`.
4. Add a row to the table above. Put longer notes in `<name>/README.md` and link it.
