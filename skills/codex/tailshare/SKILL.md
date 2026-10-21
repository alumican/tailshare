---
name: tailshare
description: Show the user a file, a page, a built site, a PDF, an image, an iPhone app (IPA) or an app running on this machine (a dev server) on their phone or another device, through their Tailscale tailnet. Use when the user should open, look at, try or install something you made, instead of starting your own web server, tunnel or tailscale serve.
---

# tailshare

`tailshare <path>` serves a file or a directory, and `tailshare http://127.0.0.1:<port>` an app running on this machine, to the devices on the user's tailnet over HTTPS, and prints the URL. Only their tailnet can open it, it stops by itself (after one hour by default), and it leaves nothing behind.

## Share something

Run it detached, so your session is not blocked; the URL is the only line on stdout:

```sh
tailshare -d -q report.html           # a page and the local files it refers to
tailshare -d -q ./dist                # a built site, served at /
tailshare -d -q -t 10m App.ipa        # an iPhone install page (open it in Safari)
tailshare -d -j notes.pdf             # the same, as JSON: url, port, adapter, path, pid, expires
```

What is served depends on the path: `*.ipa` gets an install page, `*.html` the page with only the files it refers to (images, styles), a directory is served at `/` without its hidden entries, anything else is that one file. `tailshare --which <path>` tells you which, without serving.

## Share an app running on this machine

For a dev server or a local web app, give its URL instead of a path; never run `tailscale serve` yourself:

```sh
tailshare -d -q http://127.0.0.1:8765          # the whole server, opened at /
tailshare -d -q http://localhost:3000/studio   # opened at /studio
```

Uploads, server-sent events and WebSocket pass through. Only `http://` servers on this machine can be shared, and the app must be running first.

Give the URL to the user as a Markdown link whose target includes the port, such as `[https://host.example.ts.net:10443/](https://host.example.ts.net:10443/)`. A bare URL with a port gets cut at the host by auto-linkers, and a URL in a code span cannot be tapped.

## Options worth knowing

| | |
|---|---|
| `-t 10m`, `-t 2h`, `-t 0` | How long it stays up (default `1h`; `0` until stopped). Keep it short. |
| `--once` | Stop once the thing has been downloaded completely (the file, the IPA, the page). Good for handing over one file. Not for a URL. |
| `--secret` | Put an unguessable token in the URL, so only those who get the URL can open it. The app's own links that start with `/` still work. |
| `--allow alice@example.com` | Only let these tailnet users open it (their login; globs such as `*@example.com`; repeat or separate with commas). |
| `--qr` | Also print the URL as a QR code on stderr, for a phone camera, when the user is at the terminal. |
| `-p 10443` | A port of your choice; by default the first free one of 10443, 11443, … is used. |

## When you are done

```sh
tailshare --list            # what is shared now (add --json for scripts)
tailshare --stop 10443      # stop the share on that port
```

Stop the shares you started once the user no longer needs them. Do not use `--stop all`: other projects and the user may have shares of their own running.

## When it fails

tailshare exits with 1 and says why on stderr. The usual ones:

- `tailscale is not connected (state: Stopped)` or `tailscale is not running`: ask the user to turn Tailscale on.
- `tailscale not found`: Tailscale is not installed on this machine.
- `refusing to serve …`: you pointed it at the home directory or the root of a disk; serve the folder you mean instead.
- `port … is already used by tailscale serve`: leave `-p` out, or pick another port.
- `nothing answers at http://127.0.0.1:…`: start the app first. `refusing to share …`: only servers on this machine can be shared.

Never point it at a directory that holds secrets (`.env`, keys, a whole project with credentials) just to show one file: serve that file, or the page, instead.
