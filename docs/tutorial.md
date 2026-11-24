# Tutorial

This guide takes you from a fresh install to automatic syncing every time you plug in your tablet.

## 1. Enable the USB web interface

On the tablet, open **Settings → Storage** and turn on **USB web interface**.

Plug the tablet into your computer and keep it unlocked. It should answer at <http://10.11.99.1>:

```sh
curl -sI http://10.11.99.1 | head -1   # HTTP/1.1 200
```

## 2. Install

You need Go 1.27 or later.

```sh
go install github.com/kvitrvn/mimir-remarkable/cmd/mimir-remarkable@latest
```

This installs the binary to `~/go/bin/` (or `$GOBIN`). Make sure that directory is on your `PATH`.

## 3. Check that the tablet is visible

```sh
$ mimir-remarkable ls
Journal  [notebook, 2026-11-24 13:49]
Archive/
Work/
  Meeting notes  [notebook, 2026-11-24 15:08]
```

## 4. Sync once

```sh
mimir-remarkable sync
```

By default, files go to `~/Documents/Remarkable`:

```
~/Documents/Remarkable/
├── .mimir-state.json      # sync state, do not edit
├── .trash/                # documents deleted from the tablet
├── Journal.pdf
├── Journal.rmdoc
├── Archive/
└── Work/
    ├── Meeting notes.pdf
    └── Meeting notes.rmdoc
```

Run the command again and it only downloads what changed. To pick another destination or format:

```sh
mimir-remarkable sync -dest ~/Notes -formats pdf
```

### What happens to my files?

| On the tablet          | Locally                                        |
|------------------------|------------------------------------------------|
| New document           | Downloaded                                     |
| Document edited        | Downloaded again                               |
| Renamed or moved       | Moved, with no download                        |
| Deleted                | Moved to `.trash/`, never deleted              |
| Local file removed     | Downloaded again on the next sync              |

Sync is one-way. Files you add to the folder are never sent to the tablet.

## 5. Configure

Create `~/.config/mimir-remarkable/config.toml`. Every key is optional:

```toml
dest            = "~/Documents/Remarkable"
url             = "http://10.11.99.1"
formats         = ["pdf", "rmdoc"]
concurrency     = 1      # parallel downloads
poll_interval   = "5s"   # how often watch checks for the tablet
resync_interval = "2m"   # re-sync while plugged in; "0s" = only on connect
notify          = true   # desktop notification (notify-send) when something changed
```

Command-line flags override the file. Run `mimir-remarkable <command> -h` to list them.

## 6. Sync automatically

`watch` waits for the tablet. It syncs as soon as you plug it in and again every `resync_interval` while it stays connected:

```sh
mimir-remarkable watch
```

To run it in the background at login, install the systemd user service:

```sh
mkdir -p ~/.config/systemd/user
curl -o ~/.config/systemd/user/mimir-remarkable.service \
  https://raw.githubusercontent.com/kvitrvn/mimir-remarkable/main/contrib/mimir-remarkable.service
systemctl --user daemon-reload
systemctl --user enable --now mimir-remarkable
```

The unit runs `~/go/bin/mimir-remarkable`. If you installed the binary somewhere else, edit `ExecStart` in the unit.

To follow the logs:

```sh
journalctl --user -u mimir-remarkable -f
```

## Troubleshooting

- **`tablet not reachable`**: make sure the cable is plugged in, the tablet is unlocked and the USB web interface is enabled. Then run `curl http://10.11.99.1`.
- **A document is not updated**: changes are detected through the tablet's modification date. Close the document on the tablet so it saves, then sync again.
- **Start from scratch**: delete `.mimir-state.json`. The next sync downloads everything again and overwrites the local copies.
- **More detail**: add `-v` for debug logs.
