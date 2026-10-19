package security

import (
	"os"
	"path/filepath"
	"strings"
)

// requireFreshAuthorityStorage refuses an existing or partial storage family.
// The caller owns the canonical anchor's WAL lease. A retired rotated store
// may have no anchor or tip left, so their absence cannot establish freshness.
// This guard does not read, adopt, migrate or remove any retained state.
func requireFreshAuthorityStorage(anchor string) error {
	entries, err := os.ReadDir(filepath.Dir(anchor))
	if err != nil {
		return err
	}
	base := filepath.Base(anchor)
	for _, entry := range entries {
		name := entry.Name()
		if name == base || name == base+".tip" || name == base+".current" ||
			strings.HasPrefix(name, base+".segment-") || strings.HasPrefix(name, base+".current-tmp-") {
			return os.ErrExist
		}
	}
	return nil
}
