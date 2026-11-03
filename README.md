# systemctl-ui

A native desktop GUI for `systemctl`, built with [MyGo](https://github.com/egoist/mygo)'s
native UI (no web view). It lists the units of the system or user manager,
shows one unit's properties, status and journal, and runs the everyday
verbs — start, stop, restart, reload, enable, disable, mask, unmask.

Actions go through `systemctl` itself. When the system manager refuses a
verb for want of privilege, the app retries through `sudo`: silently while
sudo's credentials are cached, otherwise after asking for the sudo password
in its own dialog. Desktops running a polkit agent authorize in the agent's
native dialog instead, before sudo is ever considered.

## Develop

The environment is declared with [devenv](https://devenv.sh) (`devenv.nix`):
Go plus the GTK 3 / WebKitGTK / Pango libraries MyGo dlopens at run time.

```sh
devenv shell   # enter the environment
dev            # go run .
test           # CGO_ENABLED=0 go test ./... && go tool mygo vet .
```

## Layout

- `backend.go` — the `Backend` interface and its CLI implementation
  (`systemctl` / `journalctl`); tests substitute a fake.
- `main.go` — application state and its async lifecycle: workers publish
  through `Window.Update`, guarded by generation counters.
- `view.go` — the views: sidebar (scope + type filter), toolbar, sortable
  units table, detail pane (Overview / Status / Logs), status bar.
