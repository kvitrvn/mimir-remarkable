# mimir-remarkable

Sync a reMarkable tablet to a local folder as soon as it is plugged in over USB.

- One-way (tablet → computer), folder tree preserved
- Each document saved as `.pdf` (handwriting rendered) and `.rmdoc` (raw, lossless)
- Incremental, with renames and moves applied locally, deletions sent to `.trash/`
- No cloud and no SSH, only the tablet's built-in USB web interface

## Install

```sh
go install github.com/kvitrvn/mimir-remarkable/cmd/mimir-remarkable@latest
```

On the tablet, enable **Settings → Storage → USB web interface**.

## Usage

```sh
mimir-remarkable ls      # list the tablet's documents
mimir-remarkable sync    # sync once to ~/Documents/Remarkable
mimir-remarkable watch   # sync every time the tablet is plugged in
```

See the [tutorial](docs/tutorial.md) for configuration and running it as a systemd service.

## Library

```go
client := remarkable.NewClient("") // defaults to http://10.11.99.1
report, err := (&rmsync.Syncer{Client: client, Dest: "/path/to/dir"}).Run(ctx)
```

## License

[MIT](LICENSE)
