# CLAUDE.md — developing tailshare

Usage and the adapter contract are in README.md. This file is for whoever changes tailshare.

## Principles

- No dependencies beyond tailscale, python3 (standard library only) and bash. No YAML, no packages.
- Unix style: stdout carries only the result (URL, listing, JSON); messages and the request log go to stderr. Exit status 0 / 1 (error) / 2 (usage).
- Usable by people and by AI agents alike: `--help` is complete, `--json` and `--detach` exist for scripts.
- Leave nothing behind: every way of stopping (time up, Ctrl-C, TERM, `--stop`) removes the `tailscale serve` config, the local server, the temporary directory and the state file.
- Never touch a `tailscale serve` port that something else uses. Check `tailscale serve status` before choosing one; other long-running services may hold ports in the 10443+ range.
- English for code, comments and docs.

## Constraints to remember

- **bash 3.2** (macOS `/bin/bash`): no associative arrays, no `mapfile`, no `${var,,}`; an empty array under `set -u` is an error.
- **The macOS Tailscale app cannot serve paths** (`tailscale serve <path>` fails with a sandbox error), hence the local `python3 -m http.server` behind `tailscale serve --https=<port> http://127.0.0.1:<port>`.
- **Background jobs ignore SIGINT** in non-interactive shells, so test stopping with TERM, or with `bash -c 'set -m; ...'` to simulate Ctrl-C.
- `--detach` re-runs the same command with `nohup` and waits for its first stdout line; the child records itself in `~/.local/state/tailshare/<port>`.

## Testing

There is no test suite yet; changes were checked by hand, serving for real:

- each adapter (`ipa`, `html` with nested CSS references, `dir` with hidden entries, `file` with spaces in its name), checking HTTP status codes with `curl`;
- `--detach`, `--json`, `--list`, `--stop <port>` / `all` / unknown port, time-up of a detached share;
- afterwards: `tailscale serve status` shows no leftover port, `$TMPDIR/tailshare.*` and `~/.local/state/tailshare/` are empty.

## Users

- iOS projects build an IPA in their own OTA script and `exec tailshare` on it. Keep the CLI and the exit status stable for them.

## Git

Public on GitHub: `alumican/tailshare`. Do not commit host names, tailnet names, or paths from a particular machine. Commits use `alumican <alumican@users.noreply.github.com>`, as there is no global git identity on this Mac.
