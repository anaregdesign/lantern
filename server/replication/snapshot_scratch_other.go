//go:build !linux && !darwin

package replication

import (
	"errors"
	"os"
)

var errSnapshotScratchPlatform = errors.New("private Snapshot disk staging requires Linux or Darwin")

func openSnapshotScratchDirectory(string) (*os.File, error) { return nil, errSnapshotScratchPlatform }
func lockSnapshotScratchOwner(*os.File) (*os.File, error)   { return nil, errSnapshotScratchPlatform }
func recoverSnapshotScratchSlots(*os.File) error            { return errSnapshotScratchPlatform }
func createSnapshotScratchSlot(*os.File, string) (*os.File, error) {
	return nil, errSnapshotScratchPlatform
}
