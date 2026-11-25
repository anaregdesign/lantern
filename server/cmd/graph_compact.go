package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
)

func compactGraphStore(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("compact-graph-store", flag.ContinueOnError)
	flags.SetOutput(out)
	target := flags.String("target", "", "existing data directory of a stopped Lantern node")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(out, "Usage: compact-graph-store --target DIRECTORY")
		_, _ = fmt.Fprintln(out, "Repack the node's Tarp data in key order. Allow temporary space for a full checkpoint.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *target == "" {
		return errors.New("compact-graph-store requires --target DIRECTORY")
	}
	db, err := graphstore.OpenExisting(ctx, *target, hlc.NodeID{})
	if err != nil {
		return err
	}
	node := db.NodeID()
	result, compactErr := db.CompactByKey(ctx)
	if err := errors.Join(compactErr, db.Close()); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Compacted graph storage in key order. Node %x, unlinked %d bytes, retired data still pinned %d bytes.\n", node, result.UnlinkedBytes, result.PinnedRetiredDataBytes)
	return err
}
