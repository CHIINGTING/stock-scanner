// Command macro-fetch captures the United States macro picture and archives it point-in-time.
//
// Market-wide by nature: it reads no candidate list, no stocks.yaml and no watchlist, and its
// cost does not change with how many stocks are being researched. Four requests, one snapshot.
//
// It reaches no verdict, computes no score and calls no AI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/macro"
)

func main() {
	log.SetFlags(log.Ltime)
	dir := flag.String("out", macro.DefaultDir(), "snapshot archive directory")
	timeout := flag.Duration("timeout", 3*time.Minute, "overall fetch timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	now := time.Now()
	snap, err := macro.NewService(nil, *dir, log.Printf).FetchAndStore(ctx, now)
	if err != nil {
		log.Printf("macro: %v", err)
	}
	if snap == nil {
		fmt.Fprintln(os.Stderr, "macro: no snapshot produced")
		os.Exit(1)
	}

	r := macro.Interpret(snap, now.Format("2006-01-02"))
	fmt.Printf("\nMacro snapshot archived  %s  status=%s\n\n", snap.ArchiveDate, snap.Status)
	render(os.Stdout, r)
	fmt.Printf("\narchive: %s\n", macro.SnapshotPath(*dir, snap.ArchiveDate))

	// A snapshot with nothing in it is a failure worth surfacing: the archive is the whole
	// point of this command, and a green run that stored no data would go unnoticed until
	// someone asked why the history stopped growing.
	if snap.Status == macro.Unavailable {
		os.Exit(1)
	}
}

func render(w *os.File, r macro.Research) {
	macro.RenderText(w, r)
}
