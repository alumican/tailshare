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

```sh
tests/run                  # everything but the live tests, about 40 seconds
tests/run --live           # also serve over the tailnet with the real tailscale
tests/run tests/serve.test.sh
TEST_FILTER=stop tests/run
```

- `tests/fake/tailscale` stands in for tailscale: it keeps its serve config in the sandbox and can play a daemon that is down, off (`Stopped`), logged out, or whose `serve` fails. Every test but `live.test.sh` uses it, so they never touch the real `tailscale serve` config and can run while other shares are up.
- Each test gets its own sandbox: `TMPDIR`, `XDG_CONFIG_HOME`, `TAILSHARE_STATE_DIR` and the fake's state all live in it. After each way of stopping, `assert_clean` checks that the serve config, the state directory and the temporary directory are empty.
- `live.test.sh` serves for real, fetches the URL over HTTPS, and checks that the ports other services use are the same before and after. Run it after changing how tailscale is called.
- A test is a `test_*` function in a `*.test.sh` file, which ends with `run_tests` (see `tests/lib.sh`). Give a command that should be refused a short `--time`, so a regression ends in seconds instead of serving for an hour.

## Users

- iOS projects build an IPA in their own OTA script and `exec tailshare` on it. Keep the CLI and the exit status stable for them.

## Git

Public on GitHub: `alumican/tailshare`. Do not commit host names, tailnet names, or paths from a particular machine. Commits use `alumican <alumican@users.noreply.github.com>`, as there is no global git identity on this Mac.
