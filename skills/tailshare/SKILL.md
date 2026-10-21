---
name: tailshare
description: Show the user a file, a page, a built site, a PDF, an image or an iPhone app (IPA) on their phone or another device, through their Tailscale tailnet. Use when the user should open, look at, try or install something you made, instead of starting your own web server or tunnel.
---

# tailshare

`tailshare <path>` serves a file or a directory to the devices on the user's tailnet over HTTPS and prints its URL. Only their tailnet can open it, it stops by itself (after one hour by default), and it leaves nothing behind.

## Share something

Run it detached, so your session is not blocked; the URL is the only line on stdout:

```sh
url=$(tailshare -d -q report.html)       # a page and the local files it refers to
url=$(tailshare -d -q ./dist)            # a built site, served at /
url=$(tailshare -d -q -t 10m App.ipa)    # an iPhone install page (open it in Safari)
tailshare -d -j notes.pdf                # the same, as JSON: url, port, adapter, path, pid, expires
```

What is served depends on the path: `*.ipa` gets an install page, `*.html` the page with only the files it refers to (images, styles), a directory is served at `/` without its hidden entries, anything else is that one file. `tailshare --which <path>` tells you which, without serving.

Give the URL to the user as a Markdown link whose target includes the port, such as `[https://host.example.ts.net:10443/](https://host.example.ts.net:10443/)`. A bare URL with a port gets cut at the host by auto-linkers, and a URL in a code span cannot be tapped.

## Options worth knowing

| | |
|---|---|
| `-t 10m`, `-t 2h`, `-t 0` | How long it stays up (default `1h`; `0` until stopped). Keep it short. |
| `--once` | Stop once the thing has been downloaded completely (the file, the IPA, the page). Good for handing over one file. |
| `--secret` | Put an unguessable token in the URL, so only those who get the URL can open it. |
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

Never point it at a directory that holds secrets (`.env`, keys, a whole project with credentials) just to show one file: serve that file, or the page, instead.
