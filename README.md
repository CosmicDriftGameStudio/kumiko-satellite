# kumiko-satellite

A small program for a customer's own machine. It watches one folder and delivers new and changed
files to a Kumiko app over HTTPS. It is the "satellite" of the connectors feature; the wire
protocol is described in the Kumiko guide `docs/guides/connectors-satellite.md`.

## Security model

- Outgoing HTTPS only. The program opens no port except a status page bound to `127.0.0.1`.
  Plain `http://` app URLs are refused unless the host is `localhost` or a loopback address.
- The device token (`ksat_...`) is stored only on this machine, in
  `os.UserConfigDir()/kumiko-satellite/config.json` with mode `0600`. The app keeps only its hash.
  An admin of the tenant can revoke the connector at any time; the satellite then shows
  "Gerät gesperrt, neu koppeln" and stops uploading.
- Only the Go standard library, no third-party modules, so the code can be audited in an
  afternoon. Everything lives in `internal/` with one small package per topic.
- Folder guard: a drive or share root (`C:\`, `D:\`, `\\server\share`, `/`), Windows system folders
  (`Windows`, `Program Files`, `Program Files (x86)`, `ProgramData`, `AppData`), the user profile
  itself and Unix system folders (`/etc`, `/usr`, `/bin`, `/System`, ...) are rejected. Pick a
  dedicated folder. Symlinks are resolved for this check and never followed while scanning.
- Hidden and system files, recycle bin, `Thumbs.db`, `desktop.ini`, `.DS_Store`, `pagefile.sys`,
  `hiberfil.sys`, `swapfile.sys` and `~$` lock files are skipped, the same list the server enforces.
- The status page checks the `Host` header (against DNS rebinding) and protects the retry button
  with a random token plus an `Origin` check.

## Install

Download the binary for your platform from the GitHub releases, or build it:

    go build -ldflags "-X main.version=1.0.0" -o kumiko-satellite ./cmd/kumiko-satellite

Check it with `kumiko-satellite version`.

## Pair

    kumiko-satellite pair --url https://app.example.com --folder "D:\Belege"

The command prints (in German) a verification URL and a code such as `AB12-CD34`. A tenant admin opens the
pairing screen in the app, enters the code and confirms. The satellite then stores the token.
`--folder` is optional here and can be given to `run` instead.

## Run

    kumiko-satellite run --folder "D:\Belege" --status-addr 127.0.0.1:8765

The folder is scanned every 30 seconds. A file is uploaded once its size and modification time
stayed equal over two scans, so half-written files wait. Uploads run one after another; failures
retry after 1, 5 and 30 minutes (`Retry-After` is respected). If the app's storage quota is
exhausted, uploads stop until you press "Erneut versuchen" on the status page. A heartbeat with
version and sync state goes out every 60 seconds. Progress is remembered in `state.json` next to
the config file.

Open `http://127.0.0.1:8765/` for the status page: sync state, progress, the last 200 events and
the retry button.

### Windows: Task Scheduler

A Windows service follows later. Until then use the Task Scheduler:

1. Create a task, trigger "At log on" (or "At startup" with a service account).
2. Action: start `C:\Tools\kumiko-satellite.exe` with arguments `run --folder "D:\Belege"`.
3. Under Settings, enable "If the task fails, restart every 1 minute" and disable
   "Stop the task if it runs longer than".

Run the task as the user whose profile holds the config file, the one that ran `pair`.

## Development

    go vet ./... && go test ./...

## License

MIT, see [LICENSE](LICENSE).
