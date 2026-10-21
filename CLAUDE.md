# CLAUDE.md — developing tailshare

Usage and the adapter contract are in README.md. This file is for whoever changes tailshare.

## Principles

- A single Go binary, built from the standard library only: no modules in `go.mod`. At run time it needs tailscale and nothing else.
- Unix style: stdout carries only the result (URL, listing, JSON); messages and the request log go to stderr. Exit status 0 / 1 (error) / 2 (usage).
- Usable by people and by AI agents alike: `--help` is complete, `--json` and `--detach` exist for scripts.
- Leave nothing behind: every way of stopping (time up, Ctrl-C, TERM, `--stop`) removes the `tailscale serve` config, the local server, the temporary directory and the state file. A share killed with `-9` is cleaned up by the next `--list` or `--stop`.
- Never touch a `tailscale serve` port that something else uses. Check `tailscale serve status` before choosing one; other long-running services may hold ports in the 10443+ range. A dead share's port is turned off only if it still proxies to that share's own local server.
- English for code, comments and docs.

## Layout

| | |
|---|---|
| `main.go` | Arguments, help, `--adapters` and `--which`, exiting with cleanups (`atExit`, `exit`, `die`) |
| `serve.go` | Serving, `--detach`, `--list`, `--stop` |
| `proc_unix.go`, `proc_windows.go` | Whether a process is alive, asking it to stop, detaching |
| `internal/adapter` | Finding adapters; the built-in ones (`ipa.go`, `html.go`, `file` and `dir` in `adapter.go`); running a `prepare` of your own. Templates for the install page in `templates/`, embedded |
| `internal/site` | What a share serves, as an `fs.FS`: single files mapped to paths, or a directory without its hidden top entries |
| `internal/plist` | Reading an Info.plist, XML or binary |
| `internal/tailscale` | The tailscale CLI: finding it, status, serve |
| `internal/state` | Records of running shares, `key=value` files in the state directory |

## Constraints to remember

- **The macOS Tailscale app cannot serve paths** (`tailscale serve <path>` fails with a sandbox error), hence tailshare's own server on `127.0.0.1` behind `tailscale serve --https=<port> http://127.0.0.1:<port>`.
- **The CLI is a contract.** Options, output, messages a script may match, and exit status stay as they are; the tests pin them. `--which` and `--adapters` show a built-in adapter's directory as `(built in)`, or `null` in JSON.
- `--detach` re-runs the same binary with `--as <id>` and `TAILSHARE_LOG` set, in a session of its own, and waits for its first stdout line. The child records itself in the state directory. If it fails before serving, it leaves its log for the parent to show.
- Windows builds but is untested. It cannot send TERM, so `--stop` kills the share and cleans up for it.
- The tests are bash 3.2 (macOS `/bin/bash`): no associative arrays, no `mapfile`, no `${var,,}`; an empty array under `set -u` is an error. Background jobs ignore SIGINT in non-interactive shells; `start_foreground` uses `set -m` so Ctrl-C can be tested.

## Testing

```sh
tests/run                  # go vet, go test, a build for each platform, the command line tests; about 40 seconds
tests/run --live           # also serve over the tailnet with the real tailscale
tests/run tests/serve.test.sh
TEST_FILTER=stop tests/run
go test ./internal/...     # the Go tests alone
```

- **Go tests** (`internal/*/…_test.go`) cover what each built-in adapter serves, the site file systems, and the plist reader (including damaged input).
- **Command line tests** (`tests/*.test.sh`, bash and python3) run a build of tailshare from outside, as its users do. A test is a `test_*` function in a `*.test.sh` file, which ends with `run_tests` (see `tests/lib.sh`).
- `tests/fake/tailscale` stands in for tailscale: it keeps its serve config in the sandbox and can play a daemon that is down, off (`Stopped`), logged out, or whose `serve` fails or is denied. Every test but `live.test.sh` uses it, so they never touch the real `tailscale serve` config and can run while other shares are up.
- Each test gets its own sandbox: `TMPDIR`, `XDG_CONFIG_HOME`, `TAILSHARE_STATE_DIR` and the fake's state all live in it. After each way of stopping, `assert_clean` checks that the serve config, the state directory and the temporary directory are empty.
- The tests for a missing tailscale build tailshare with `-ldflags "-X …/internal/tailscale.MacApp=…"`, so the app in `/Applications` is not found.
- `live.test.sh` serves for real, fetches the URL over HTTPS, and checks that the ports other services use are the same before and after. Run it after changing how tailscale is called.
- Give a command that should be refused a short `--time`, so a regression ends in seconds instead of serving for an hour.

## Releasing

1. Set `version` in `main.go` (`1.2.3`) and commit it.
2. `git tag v1.2.3 && git push origin main v1.2.3`.
3. `.github/workflows/release.yml` runs the tests on macOS and Linux, then `scripts/release`, and publishes a GitHub release with an archive per platform and `SHA256SUMS`. GitHub adds the source.

- `scripts/release v1.2.3` builds the same archives into `dist/` locally; it refuses a tag that does not match `main.go`.
- Releases and CI build with the latest stable Go, for its security fixes. `go.mod` says 1.24, the oldest that works: earlier linkers leave out `LC_UUID`, and macOS 26 refuses to run such binaries (`dyld: missing LC_UUID load command`). The `oldest-go` job checks that 1.24 still builds.
- Stay on v1: from v2 on, Go needs the module path to end in `/v2`, which changes `go install` for everyone.
- `.github/workflows/test.yml` runs `tests/run` (without `--live`) on macOS and Linux for every push to `main`.

## Users

- iOS projects build an IPA in their own OTA script and `exec tailshare` on it. Keep the CLI and the exit status stable for them.

## Git

Public on GitHub: `alumican/tailshare`. Do not commit host names, tailnet names, or paths from a particular machine. Commits use `alumican <alumican@users.noreply.github.com>`, as there is no global git identity on this Mac.
