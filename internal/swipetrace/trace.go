// Package swipetrace records optional, content-free latency diagnostics.
package swipetrace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type traceKey struct{}

type trace struct {
	file *os.File
	id   int64
}

func Start(ctx context.Context) (context.Context, func()) {
	dir := Directory()
	if !filepath.IsAbs(dir) {
		return ctx, func() {}
	}
	now := time.Now()
	f, err := os.OpenFile(filepath.Join(dir, "swipe.tsv"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return ctx, func() {}
	}
	ctx = context.WithValue(ctx, traceKey{}, trace{file: f, id: now.UnixNano()})
	Mark(ctx, "mcp.received")
	return ctx, func() {
		Mark(ctx, "mcp.return")
		f.Close()
	}
}

func Mark(ctx context.Context, stage string) {
	if t, ok := ctx.Value(traceKey{}).(trace); ok {
		fmt.Fprintf(t.file, "%d\t%d\t%s\n", t.id, time.Now().UnixMilli(), stage)
	}
}

func Directory() string {
	return os.Getenv("PINKY_HARMONYOS_H264_DIAGNOSTICS")
}

// Wire records arrival and forwarding time without RPC payloads.
func Wire(stage string) {
	dir := Directory()
	if !filepath.IsAbs(dir) {
		return
	}
	now := time.Now().UnixMilli()
	f, err := os.OpenFile(filepath.Join(dir, "stdio.tsv"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%d\t%d\t%s\n", now, os.Getpid(), stage)
	f.Close()
}
