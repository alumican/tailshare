# CLAUDE.md — developing tailshare

Usage and the adapter contract are in README.md. This file is for whoever changes tailshare.

## Principles

- A single Go binary, built from the standard library only: no modules in `go.mod`. At run time it needs tailscale and nothing else.
- Unix style: stdout carries only the result (URL, listing, JSON); messages and the request log go to stderr. Exit status 0 / 1 (error) / 2 (usage).
- Usable by people and by AI agents alike: `--help` is complete, `--json` and `--detach` exist for scripts.
- Leave nothing behind: every way of stopping (time up, Ctrl-C, TERM, `--stop`) removes the `tailscale serve` config, the local server, the temporary directory and the state file. A share killed with `-9` is cleaned up by the next `--list` or `--stop`.
- Never touch a `tailscale serve` port that something else uses. Check `tailscale serve status` before choosing one, including the ports a `tailscale serve` running in the foreground holds (under `Foreground`); other long-running services may hold ports in the 10443+ range. A dead share's port is turned off only if it still proxies to that share's own local server.
- English for code, comments and docs.

## Layout

| | |
|---|---|
| `cmd/tailshare` | The command: arguments and help, serving and the request handler, `--detach`, `--list`, `--stop` |
| `internal/adapter` | Finding adapters, the built-in ones, running a `prepare` of your own |
| `internal/site`, `internal/plist`, `internal/qr` | What a share serves (`fs.FS`), Info.plist, QR codes |
| `internal/tailscale`, `internal/state` | The tailscale CLI, records of running shares |
| `skills/claude`, `skills/codex` | The skill for each agent, embedded by `skills/skills.go` for `--skill` |
| `tests`, `scripts` | Command line tests, the release build |

## Constraints to remember

- **The macOS Tailscale app cannot serve paths** (`tailscale serve <path>` fails with a sandbox error), hence tailshare's own server on `127.0.0.1` behind `tailscale serve --https=<port> http://127.0.0.1:<port>`.
- **tailscale serve runs in the foreground, as a child**, not with `--bg`: its config then lives under `Foreground` only as long as the child does, and does not survive a reboot. The child is in a process group of its own, so a Ctrl-C reaches tailshare alone, which stops the child and waits until the port is free. If the child dies, the share stops with an error. A share killed with `-9` leaves the child running; `--list` and `--stop` stop it (by its `serve_pid`) once they see the port still proxies to the share's own server. `cleanUpAfter` also turns off a `--bg` config left by tailshare 1.0.
- **Identity comes from tailscale serve**: it sets `Tailscale-User-Login` on each request and removes any a client sends (`addTailscaleIdentityHeaders` in tailscale's `ipn/ipnlocal/serve.go`). Tagged devices get none. `--allow` relies on this.
- With `--secret`, the token is stripped before the site is served, and the request log shows the path without it.
- **tailscale serve can be busy.** When two clients change the serve config at once, one gets "Another client is changing the serve config; please try again." (tailscale/tailscale#18742). tailshare tries again, up to five times.
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

- **Go tests** cover what each built-in adapter serves, the site file systems, the plist reader (including damaged input), the request handler (`serve_test.go`: token, `--allow`, when `--once` counts a download as complete, the home check), and QR codes. On macOS, `internal/qr` reads its codes back through CoreImage (`testdata/read.swift`), as an independent reader.
- **Command line tests** (`tests/*.test.sh`, bash and python3) run a build of tailshare from outside, as its users do. A test is a `test_*` function in a `*.test.sh` file, which ends with `run_tests` (see `tests/lib.sh`).
- `tests/fake/tailscale` stands in for tailscale: it keeps its serve config in the sandbox and can play a daemon that is down, off (`Stopped`), logged out, or whose `serve` fails, is denied, or is busy, and ports held by another `tailscale serve` in the foreground. Every test but `live.test.sh` uses it, so they never touch the real `tailscale serve` config and can run while other shares are up.
- Each test gets its own sandbox: `TMPDIR`, `XDG_CONFIG_HOME`, `TAILSHARE_STATE_DIR` and the fake's state all live in it. After each way of stopping, `assert_clean` checks that the serve config, the state directory and the temporary directory are empty.
- The tests for a missing tailscale build tailshare with `-ldflags "-X …/internal/tailscale.MacApp=…"`, so the app in `/Applications` is not found.
- `live.test.sh` serves for real, fetches the URL over HTTPS (with `--secret` and `--allow` too, checking the login tailscale sends), holds an unused port with a foreground `tailscale serve` to check it is refused, and checks that the ports other services use are the same before and after. Run it after changing how tailscale is called.
- Give a command that should be refused a short `--time`, so a regression ends in seconds instead of serving for an hour.

## Releasing

1. Set `version` in `cmd/tailshare/main.go` (`1.2.3`) and commit it.
2. `git tag v1.2.3 && git push origin main v1.2.3`.
3. `.github/workflows/release.yml` runs the tests on macOS and Linux, then `scripts/release`, and publishes a GitHub release with an archive per platform and `SHA256SUMS`. GitHub adds the source.

- `scripts/release v1.2.3` builds the same archives into `dist/` locally; it refuses a tag that does not match `cmd/tailshare/main.go`.
- Releases and CI build with the latest stable Go, for its security fixes. `go.mod` says 1.24, the oldest that works: earlier linkers leave out `LC_UUID`, and macOS 26 refuses to run such binaries (`dyld: missing LC_UUID load command`). The `oldest-go` job checks that 1.24 still builds.
- Stay on v1: from v2 on, Go needs the module path to end in `/v2`, which changes `go install` for everyone.
- `.github/workflows/test.yml` runs `tests/run` (without `--live`) on macOS and Linux for every push to `main`.

## Users

- iOS projects build an IPA in their own OTA script and `exec tailshare` on it. Keep the CLI and the exit status stable for them.
- AI agents, through the skills. When the CLI changes, update `skills/claude/tailshare/SKILL.md` and `skills/codex/tailshare/SKILL.md` with it. They differ only in their front matter (the one for Claude Code has `allowed-tools`); `args.test.sh` checks that their bodies match, that `--skill` prints them, and that the options in `--help` are in the README and the skill.

## Git

Public on GitHub: `alumican/tailshare`. Do not commit host names, tailnet names, or paths from a particular machine. Commits use `alumican <alumican@users.noreply.github.com>`, as there is no global git identity on this Mac.
