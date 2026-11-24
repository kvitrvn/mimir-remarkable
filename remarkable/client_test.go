package remarkable_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/kvitrvn/mimir-remarkable/internal/faketablet"
	"github.com/kvitrvn/mimir-remarkable/remarkable"
)

func TestPing(t *testing.T) {
	tb := faketablet.New(t)
	if err := tb.Client().Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	tb.Server.Close()
	if err := tb.Client().Ping(context.Background()); err == nil {
		t.Fatal("Ping on a closed server: want error")
	}
}

func TestWalk(t *testing.T) {
	tb := faketablet.New(t)
	tb.AddDoc("d1", "Journal ", "", "2026-11-24T12:49:26.571Z")
	tb.AddFolder("f1", "Work", "")
	tb.AddFolder("f2", "Sub", "f1")
	tb.AddDoc("d2", "Meeting", "f1", "2026-11-24T14:08:05.3Z")
	tb.AddDoc("d3", "Deep", "f2", "2026-11-24T14:08:05.3Z")

	nodes, err := tb.Client().Walk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nodes {
		got = append(got, strings.Join(append(slices.Clone(n.Path), n.VisibleName), "/"))
	}
	want := []string{"Journal ", "Work", "Work/Sub", "Work/Sub/Deep", "Work/Meeting"}
	if !slices.Equal(got, want) {
		t.Fatalf("Walk paths = %q, want %q", got, want)
	}
	if m := nodes[0].Modified(); m.IsZero() || m.Nanosecond() != 571_000_000 {
		t.Errorf("Modified() = %v", m)
	}
}

func TestDownload(t *testing.T) {
	tb := faketablet.New(t)
	tb.AddDoc("d1", "Doc", "", "2026-01-01T00:00:00Z")
	c := tb.Client()
	ctx := context.Background()

	rc, err := c.Download(ctx, "d1", remarkable.FormatPDF)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if want := faketablet.Content("d1", "2026-01-01T00:00:00Z", remarkable.FormatPDF); string(b) != want {
		t.Errorf("body = %q, want %q", b, want)
	}

	_, err = c.Download(ctx, "missing", remarkable.FormatPDF)
	var se *remarkable.StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Errorf("missing doc: err = %v, want 404 StatusError", err)
	}

	if _, err := c.Download(ctx, "d1", "docx"); err == nil {
		t.Error("invalid format: want error")
	}

	tb.Break("d1", true)
	rc, err = c.Download(ctx, "d1", remarkable.FormatRMDoc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(rc)
	rc.Close()
	if err == nil {
		t.Error("broken download: want read error")
	}
}
