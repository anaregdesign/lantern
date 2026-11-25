package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/server/backup"
)

func restoreGraphStore(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("restore-graph-store", flag.ContinueOnError)
	flags.SetOutput(out)
	source := flags.String("source", "", "private recovery point directory")
	target := flags.String("target", "", "new graph storage directory")
	instance := flags.String("instance", "", "backup producer's instance ID")
	expected := flags.String("cut-sha256", "", "independently retained SHA-256 from the backup log")
	origin := flags.String("node-id", "", "expected 16-byte node ID in hex")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *source == "" || *target == "" || *instance == "" {
		return errors.New("restore-graph-store requires --source, --target, --instance and --cut-sha256")
	}
	hash, err := hex.DecodeString(*expected)
	if err != nil || len(hash) != 32 {
		return errors.New("--cut-sha256 requires 32 bytes in hex")
	}
	var node hlc.NodeID
	if *origin != "" {
		id, err := hex.DecodeString(*origin)
		if err != nil || len(id) != len(node) || hlc.NodeID(id) == (hlc.NodeID{}) {
			return errors.New("--node-id requires a nonzero 16-byte node ID in hex")
		}
		node = hlc.NodeID(id)
	}
	if err := backup.RestoreGraphStore(ctx, *source, *target, *instance, node, [32]byte(hash)); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Restored and verified private graph storage.")
	return err
}
