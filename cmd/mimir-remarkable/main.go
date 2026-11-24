// Command mimir-remarkable syncs a reMarkable tablet plugged in over USB into
// a local folder.
//
//	mimir-remarkable sync   # sync once
//	mimir-remarkable watch  # wait for the tablet, sync whenever it is plugged in
//	mimir-remarkable ls     # print the tablet's tree
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kvitrvn/mimir-remarkable/internal/config"
	"github.com/kvitrvn/mimir-remarkable/remarkable"
	"github.com/kvitrvn/mimir-remarkable/rmsync"
)

const usage = `Usage: mimir-remarkable <command> [flags]

Commands:
  sync    sync the tablet once, then exit
  watch   wait for the tablet and sync each time it is plugged in
  ls      print the documents on the tablet

Run "mimir-remarkable <command> -h" for the command's flags.
The tablet must be plugged in over USB with "USB web interface" enabled
(Settings > Storage on the tablet).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "sync":
		err = cmdSync(ctx, args)
	case "watch":
		err = cmdWatch(ctx, args)
	case "ls":
		err = cmdLs(ctx, args)
	case "-h", "-help", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mimir-remarkable:", err)
		os.Exit(1)
	}
}

type env struct {
	cfg    config.Config
	log    *slog.Logger
	client *remarkable.Client
}

// setup parses flags, loads the config file and applies flag overrides.
func setup(name string, args []string, watch bool) (*env, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/mimir-remarkable/config.toml)")
	dest := fs.String("dest", "", "local destination folder")
	url := fs.String("url", "", "tablet web interface URL")
	formats := fs.String("formats", "", "comma-separated formats to download: pdf,rmdoc")
	conc := fs.Int("concurrency", 0, "parallel downloads")
	verbose := fs.Bool("v", false, "verbose logging")
	var poll, resync *time.Duration
	var notify *bool
	if watch {
		poll = fs.Duration("poll", 0, "how often to check whether the tablet is plugged in")
		resync = fs.Duration("resync", 0, "re-sync interval while the tablet stays plugged in (0 disables)")
		notify = fs.Bool("notify", true, "desktop notification after a sync that changed something")
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %q", fs.Args())
	}

	path, required := *cfgPath, true
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return nil, err
		}
		path, required = p, false
	}
	cfg, err := config.Load(path, required)
	if err != nil {
		return nil, err
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "dest":
			cfg.Dest = *dest
		case "url":
			cfg.URL = *url
		case "formats":
			cfg.Formats = strings.Split(*formats, ",")
		case "concurrency":
			cfg.Concurrency = *conc
		case "poll":
			cfg.PollInterval.Duration = *poll
		case "resync":
			cfg.ResyncInterval.Duration = *resync
		case "notify":
			cfg.Notify = *notify
		}
	})
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	return &env{cfg: cfg, log: log, client: remarkable.NewClient(cfg.URL)}, nil
}

func (e *env) syncer() *rmsync.Syncer {
	formats, _ := e.cfg.ParsedFormats() // validated in setup
	return &rmsync.Syncer{
		Client:      e.client,
		Dest:        e.cfg.Dest,
		Formats:     formats,
		Concurrency: e.cfg.Concurrency,
		Logger:      e.log,
	}
}

func (e *env) runSync(ctx context.Context) (rmsync.Report, error) {
	start := time.Now()
	e.log.Info("sync started", "dest", e.cfg.Dest)
	r, err := e.syncer().Run(ctx)
	if err != nil {
		return r, err
	}
	e.log.Info("sync finished", "result", r.String(), "took", time.Since(start).Round(time.Millisecond))
	return r, nil
}

func notReachable(url string, err error) error {
	return fmt.Errorf("tablet not reachable at %s (plugged in, unlocked, \"USB web interface\" enabled?): %w", url, err)
}

func cmdSync(ctx context.Context, args []string) error {
	e, err := setup("sync", args, false)
	if err != nil {
		return err
	}
	if err := e.client.Ping(ctx); err != nil {
		return notReachable(e.cfg.URL, err)
	}
	r, err := e.runSync(ctx)
	if err != nil {
		return err
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("%d documents failed to sync", len(r.Errors))
	}
	return nil
}

func cmdWatch(ctx context.Context, args []string) error {
	e, err := setup("watch", args, true)
	if err != nil {
		return err
	}
	e.log.Info("watching for the tablet", "url", e.cfg.URL, "dest", e.cfg.Dest, "poll", e.cfg.PollInterval.Duration)

	connected := false
	var lastSync time.Time
	check := func() {
		if err := e.client.Ping(ctx); err != nil {
			if connected && ctx.Err() == nil {
				e.log.Info("tablet disconnected")
			}
			connected = false
			return
		}
		resync := e.cfg.ResyncInterval.Duration
		if connected && (resync == 0 || time.Since(lastSync) < resync) {
			return
		}
		if !connected {
			e.log.Info("tablet connected")
			connected = true
		}
		lastSync = time.Now()
		r, err := e.runSync(ctx)
		if err != nil {
			if ctx.Err() == nil {
				e.log.Error("sync failed", "err", err)
			}
			return
		}
		if e.cfg.Notify && (r.Changed() || len(r.Errors) > 0) {
			notify(e.log, r.String())
		}
	}

	ticker := time.NewTicker(e.cfg.PollInterval.Duration)
	defer ticker.Stop()
	for {
		check()
		select {
		case <-ctx.Done():
			e.log.Info("stopping")
			return nil
		case <-ticker.C:
		}
	}
}

// notify shows a desktop notification if notify-send is available.
func notify(log *slog.Logger, msg string) {
	bin, err := exec.LookPath("notify-send")
	if err != nil {
		log.Debug("notify-send not found; skipping notification")
		return
	}
	if err := exec.Command(bin, "--app-name=mimir-remarkable", "--icon=document-save", "reMarkable", msg).Run(); err != nil {
		log.Debug("notification failed", "err", err)
	}
}

func cmdLs(ctx context.Context, args []string) error {
	e, err := setup("ls", args, false)
	if err != nil {
		return err
	}
	nodes, err := e.client.Walk(ctx)
	if err != nil {
		return notReachable(e.cfg.URL, err)
	}
	for _, n := range nodes {
		indent := strings.Repeat("  ", len(n.Path))
		if n.IsFolder() {
			fmt.Printf("%s%s/\n", indent, n.VisibleName)
			continue
		}
		mod := n.ModifiedClient
		if t := n.Modified(); !t.IsZero() {
			mod = t.Local().Format("2006-01-02 15:04")
		}
		fmt.Printf("%s%s  [%s, %s]\n", indent, n.VisibleName, n.FileType, mod)
	}
	return nil
}
