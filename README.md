# kumiko-satellite

A small program for a customer's own machine. It watches one folder and delivers new and changed
files to a Kumiko app over HTTPS. It is the "satellite" of the connectors feature; the wire
protocol is described in the Kumiko guide `docs/guides/connectors-satellite.md`.

## Security model

- Outgoing HTTPS only. The program opens no port except the local page bound to `127.0.0.1`.
  Plain `http://` app URLs are refused unless the host is `localhost` or a loopback address.
- The device token (`ksat_...`) is stored only on this machine, in
  `os.UserConfigDir()/kumiko-satellite/config.json` with mode `0600`. The app keeps only its hash.
  An admin of the tenant can revoke the connector at any time; the satellite then shows
  "Device revoked, pair again" and stops uploading.
- Only the Go standard library, no third-party modules, so the code can be audited in an
  afternoon. Everything lives in `internal/` with one small package per topic.
- Folder guard: a drive or share root (`C:\`, `D:\`, `\\server\share`, `/`), Windows system folders
  (`Windows`, `Program Files`, `Program Files (x86)`, `ProgramData`, `AppData`), the user profile
  itself and Unix system folders (`/etc`, `/usr`, `/bin`, `/System`, ...) are rejected. Pick a
  dedicated folder. Symlinks are resolved for this check and never followed while scanning.
- Hidden and system files, recycle bin, `Thumbs.db`, `desktop.ini`, `.DS_Store`, `pagefile.sys`,
  `hiberfil.sys`, `swapfile.sys` and `~$` lock files are skipped, the same list the server enforces.
- Only the user who started the satellite can use the local page, also on a shared server (RDP):
  each start writes a random secret to `session` in the user's config dir (mode `0600`). The page
  answers 401 without the random session cookie that `/login?k=<nonce>` sets. The nonce is single-use, valid 60 s, and is fetched with the secret as Bearer token (`/login-nonce`), so the secret is never in a URL, command line or cookie; a second start does the same to
  ask the running instance to show its window. If the default port is taken (for example by
  another user's instance) a free port is used and recorded in the same file.
- The local page checks the `Host` header (against DNS rebinding) and protects every button
  with a random token plus an `Origin` check. Folder dialog and `schtasks` run with fixed
  arguments only; the `ksat_` token is never shown on the page.

## Install and set up

Download the binary for your platform from the GitHub releases and double-click it. A window
opens (Windows and macOS; on Linux the default browser opens instead):

1. **Connect:** enter the app address and press "Pair". A code appears; an admin of your company
   confirms it in the app ("Confirm in the app" opens the pairing screen).
2. **Choose folder:** pick the folder with "Browse…" (native folder dialog; on Linux needs
   `zenity`, otherwise type the path) and keep "Start automatically when I sign in" ticked.

Then the window shows the status. Closing the window does not stop the sync; double-click the
program again to get the window back. "Settings" on the status page changes the folder and the
autostart. After a revocation, "Pair again" runs step 1 again and keeps the folder.

Autostart: Windows uses a per-user logon task (`schtasks`, no admin rights), macOS a LaunchAgent in
`~/Library/LaunchAgents`, Linux an entry in `~/.config/autostart`. The log is written next to
`state.json` (`kumiko-satellite.log`).

If WebView2 is missing or the window cannot open, the default browser opens the same page.
`kumiko-satellite open` opens it again from a terminal.

### For admins: command line

    kumiko-satellite pair --url https://app.example.com --folder "D:\Belege"
    kumiko-satellite run --folder "D:\Belege" --status-addr 127.0.0.1:8765
    kumiko-satellite version

`pair` prints a verification URL and a code such as `AB12-CD34`; an admin confirms it in the app
and the token is stored. `--folder` is optional there and can be given to `run`. The Windows exe
is built for the GUI subsystem and attaches to the parent console, so the commands print in `cmd`
and PowerShell (the prompt may return before the output ends).

Build from source (Windows and Linux are pure Go; macOS needs cgo for the window):

    go build -ldflags "-X main.version=1.0.0" -o kumiko-satellite ./cmd/kumiko-satellite

## How it runs

The folder is scanned every 30 seconds. A file is uploaded once its size and modification time
stayed equal over two scans, so half-written files wait ("Checking new files"). Uploads run one
after another; failures retry after 1, 5 and 30 minutes (`Retry-After` is respected). If the app's
storage is full, uploads pause until you press "Retry". A heartbeat with version and sync state
goes out every 60 seconds. Progress is remembered in `state.json` next to the config file.

## Dependencies

Everything else is the Go standard library. Two small modules draw the window, one per platform:

- `github.com/jchv/go-webview2` (Windows): hosts WebView2, which ships with Windows 10 and 11;
  no cgo, so the Windows build stays a plain cross-build.
- `github.com/webview/webview_go` (macOS): hosts the system WKWebView through cgo; the macOS
  binary is built on a Mac runner.

Both are pinned in `go.mod`. Linux builds do not use them.

## Development

    go vet ./... && go test ./...

## License

MIT, see [LICENSE](LICENSE).

The status page embeds IBM Plex Sans and IBM Plex Mono (SIL Open Font License 1.1, see
`internal/status/fonts/OFL.txt`).
