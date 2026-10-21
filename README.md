# tailshare

Serve a file or a directory to your tailnet over HTTPS, and print its URL.
Only devices on your tailnet can open it. It stops after a set time or on Ctrl-C, and leaves nothing behind.
What is served for a path is decided by **adapters**, which you can add by dropping in a directory.

```sh
tailshare report.html              # a page and the files it refers to, for an hour
tailshare -t 0 ./dist              # a built site, until Ctrl-C
tailshare -t 10m App.ipa           # an iPhone install page, for ten minutes
url=$(tailshare -d -q notes.pdf)   # in the background; keep the URL
tailshare --list                   # what is being shared now
tailshare --stop all               # stop it all
```

## Usage

```
tailshare [options] <path>      serve a path
tailshare --list                show running shares
tailshare --stop <port|all>     stop running shares
tailshare --adapters            list the adapters
tailshare --which <path>        show which adapter would serve a path
```

| Option | |
|---|---|
| `-p, --port PORT` | tailnet HTTPS port. Default: the first of 10443, 11443, … that `tailscale serve` does not use. A port in use is refused. |
| `-t, --time TIME` | Stop after TIME: `90` (seconds), `30s`, `10m`, `2h`, or `0` for never. Default: `1h`. |
| `-a, --as NAME` | Use this adapter whatever the path is: `ipa`, `html`, `dir`, `file`, … |
| `-d, --detach` | Serve in the background: print the URL and return at once. Stop it with `--stop`, or let `--time` end it. |
| `-j, --json` | Print one JSON object per line instead of plain text (serving, `--list`, `--adapters`, `--which`). |
| `-q, --quiet` | Print nothing to stderr: no messages, no request log. |
| `--list` | Show running shares: port, adapter, until when, URL, path. |
| `--stop TARGET` | Stop the share on port TARGET, or all of them with `all`. |
| `--adapters` | List the adapters in the order they are tried. |
| `--which PATH` | Show which adapter would serve PATH; serve nothing. |
| `-h, --help` / `-V, --version` | Show the help / the version. |

Short options without values can be combined: `-dqj`.

### Output and exit status

- **stdout** carries only the result: the URL when serving (one line), or the listing.
- **stderr** carries messages and the request log, unless `--quiet`.
- With `--json`, serving prints:

  ```json
  {"url": "https://host.tailnet.ts.net:10443/report.html", "port": 10443, "adapter": "html",
   "path": "/Users/me/report.html", "pid": 12345, "expires": "2026-01-01T12:00:00+0900"}
  ```

  `expires` is `null` with `--time 0`. `--list --json` prints the same object for each share.
- Exit status: `0` served and stopped normally (time up, Ctrl-C, `--stop`) or the query succeeded; `1` an error (no such path, no adapter, port in use, tailscale not running, …); `2` wrong arguments.

### For scripts and AI agents

- Use `-d -j` (or `-d -q`): the command returns as soon as the share is up, with the URL on stdout. Nothing else needs to keep running in your session.
- Find what is running with `--list --json`, and stop it with `--stop <port>`. Shares also stop by themselves after `--time` (one hour by default), so a forgotten one does not stay up.
- Use `--which --json <path>` to check what would be served before serving it.

## Built-in adapters

| Adapter | Matches | Serves |
|---|---|---|
| `ipa` | `*.ipa` | An install page for iPhone, with its manifest and the IPA. The title and version come from the IPA's Info.plist. |
| `html` | `*.html` `*.htm` | The page and the local files it refers to, and nothing else: `src`, `href`, `srcset`, `poster`, `url()` and `@import` in styles, following on into style sheets. Other files in the directory, and anything outside it, are not served. |
| `dir` | a directory | The directory at `/`, so a built site finds its files where it expects them: its `index.html`, or a listing. Hidden entries at the top (`.git`, `.env`, …) are left out. |
| `file` | anything else | That one file, at `/<its name>`. |

- iOS installs only IPAs signed for development or ad hoc, on devices registered for them.
- The `html` adapter cannot see files a script loads by itself (`fetch`, …). Serve the directory for such pages.
- Nothing is copied: files are read from where they are when they are requested.

## Adding an adapter

An adapter of your own is a directory with two files. Put it in place and it is used from the next run; there is nothing to register.

```
30-apk/
├── match     # what it serves: the first comment describes it, then patterns
└── prepare   # an executable, in any language; templates and other files may sit beside it
```

```
# match
# Install page for an Android app
*.apk
```

- **Where:** `~/.config/tailshare/adapters/` (`%AppData%\tailshare\adapters` on Windows). Set `TAILSHARE_ADAPTER_PATH` (separated by `:`, or `;` on Windows) to look elsewhere; the first one found for a name wins.
- **Order:** your adapters and the built-in ones are tried together by name, and the first whose patterns match is used, so the leading number sets the priority. One of yours with the name of a built-in one (`99-file`) replaces it. `--as` takes the name without the number (`ipa`) or the whole name (`10-ipa`).
- **Patterns:** shell globs on the name of the path, case-insensitive, separated by spaces or lines; `#` starts a comment. Globs never match a directory; `@dir` matches any directory.
- An adapter whose `prepare` is not executable is skipped with a warning, and marked in `--adapters`. On Windows, `prepare.exe`, `prepare.cmd` or `prepare.bat`.

### The `prepare` contract

```
prepare <path> <site> <base-url>
```

| | |
|---|---|
| `<path>` | What to serve, as an absolute path. |
| `<site>` | An empty directory: whatever is put here is served under `<base-url>/`. Link to the path rather than copy it. |
| `<base-url>` | `https://<host>:<port>`, for things that need absolute URLs, such as a manifest. |
| `TAILSHARE_ADAPTER_DIR` | The adapter's own directory, to read its templates from. |
| stdout | The last line is the path to open, such as `/` or `/report.html`. tailshare URL-encodes it. |
| exit status | Anything but 0 stops tailshare without serving; say why on stderr. |

A `prepare` that serves one file, as the built-in `file` adapter does:

```bash
#!/bin/bash
set -euo pipefail
input="$1" site="$2"
name="$(basename "$input")"
ln -s "$input" "$site/$name"
echo "/$name"
```

## How it works

1. The adapter decides what to serve. An adapter of your own fills a temporary directory.
2. tailshare serves it itself, on a free port of `127.0.0.1`.
3. `tailscale serve --bg --https=<port>` exposes that port to the tailnet.
4. When the time is up, on Ctrl-C, or on `--stop`, tailshare runs `tailscale serve --https=<port> off`, stops the server, and removes the temporary directory and its record.

Running shares are recorded in `~/.local/state/tailshare/<port>` (or `$TAILSHARE_STATE_DIR`), which is how `--list` and `--stop` find them. A share that is gone without cleaning up after itself (killed with `-9`, a crash) is cleaned up the next time they look: its record, log and temporary directory are removed, and its port is turned off if it still points at the share's own server.

The local server is needed because the macOS Tailscale app cannot serve files itself: `tailscale serve <path>` is blocked by its sandbox.

## Platforms

| Platform | Runs tailshare | Opens the URL | Installs an app from it |
|---|:-:|:-:|:-:|
| macOS | ✅ | ✅ | — |
| Linux | ⚠️ ¹ | ✅ | — |
| Windows | ⚠️ ² | ✅ | — |
| iOS, iPadOS | ❌ | ✅ | ✅ ³ |
| Android | ❌ | ✅ | ❌ ⁴ |

✅ works and is tested · ⚠️ builds, but has not been tried there · ❌ does not

- **Runs tailshare:** the machine whose files are served. **Opens the URL:** any device on the tailnet with Tailscale connected, in any browser. The certificate is a real one for the device's `ts.net` name, so there is no warning.
- ¹ `tailscale serve` needs root, or `sudo tailscale set --operator=$USER` once; tailshare says so when it is refused.
- ² `--stop` cannot ask a share to stop on Windows, so it ends it and cleans up for it. Adapters of your own need a way to make links in `<site>` (Developer Mode), or must copy.
- ³ The `ipa` adapter's install page, in Safari.
- ⁴ No built-in adapter: an APK is served by `file` as a download. See [Adding an adapter](#adding-an-adapter).

## Requirements

- `tailscale`, with HTTPS certificates enabled on the tailnet. If it is not on `PATH`, the one in `/Applications/Tailscale.app` (macOS) or `C:\Program Files\Tailscale` (Windows) is used.
- Nothing else at run time: tailshare is a single binary.
- To build it yourself: Go 1.24 or later (earlier ones make macOS binaries that recent macOS refuses to run). It uses the standard library only.

## Install

Download the archive for your platform from [Releases](https://github.com/alumican/tailshare/releases) and put `tailshare` on your `PATH`. For example, on a Mac with Apple Silicon:

```sh
curl -fsSL https://github.com/alumican/tailshare/releases/latest/download/tailshare_darwin_arm64.tar.gz \
  | tar -xz -C ~/.local/bin tailshare
```

| | Archive |
|---|---|
| macOS, Apple Silicon / Intel | `tailshare_darwin_arm64.tar.gz` / `tailshare_darwin_amd64.tar.gz` |
| Linux, x86-64 / ARM | `tailshare_linux_amd64.tar.gz` / `tailshare_linux_arm64.tar.gz` |
| Windows, x86-64 / ARM | `tailshare_windows_amd64.zip` / `tailshare_windows_arm64.zip` |

- Each release has `SHA256SUMS` to check the archives against, and the source code.
- The macOS binary is not signed. Downloaded with `curl` it just runs; downloaded with a browser, macOS blocks it until you run `xattr -d com.apple.quarantine tailshare`.

With Go 1.24 or later:

```sh
go install github.com/alumican/tailshare@latest     # into $(go env GOPATH)/bin
```

Or from a clone, building the binary in place and linking it onto your `PATH` (run `go build` again after `git pull`):

```sh
git clone https://github.com/alumican/tailshare && cd tailshare
go build
ln -s "$PWD/tailshare" ~/.local/bin/tailshare
```

## Development

```sh
tests/run          # go vet, go test, a build for each platform, and the command line tests
tests/run --live   # also serve over the tailnet with the real tailscale
```

See `CLAUDE.md` for the principles and how the tests work.

## License

MIT. See [LICENSE](LICENSE).
