package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/privatefile"
)

const currentCustodyVersion = 1

var errCurrentCustody = errors.New("current authority custody is not a valid orderly restart checkpoint")

// This is local lifecycle evidence, not a serving epoch or an external rollback
// witness. A floor is usable only with the CLEAN record that hashes its bytes.
type currentCustodyState struct {
	Version uint32
	Phase   string
	Cycle   uint64
	Binding [32]byte
	Floors  [32]byte
}

type currentCustody struct {
	provisioning *CurrentProvisioning
	lease        *mutationlog.FileWALLease
	state        currentCustodyState
	io           currentCustodyIO
	attempted    bool
	started      bool
	finished     bool
}

// The private I/O port only replaces filesystem operations in paired fault
// tests. No configuration, public constructor or authority path can supply it.
type currentCustodyFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

type currentCustodyIO struct {
	createTemp    func(string, string) (currentCustodyFile, error)
	rename        func(string, string) error
	syncDirectory func(string) error
}

func nativeCurrentCustodyIO() currentCustodyIO {
	return currentCustodyIO{
		createTemp: func(dir, prefix string) (currentCustodyFile, error) { return privatefile.CreateTemp(dir, prefix) },
		rename:     os.Rename, syncDirectory: privatefile.SyncDirectory,
	}
}

func currentCustodyPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errCurrentCustody
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", errors.Join(errCurrentCustody, err)
	}
	return filepath.Join(dir, filepath.Base(path)), nil
}

// Open each existing file and verify its private descriptor and its pathname
// identity. Lstat alone is not a file-ownership check. The input list includes
// all three native families and their tip/lease names, even before creation.
func currentCustodyPaths(p *CurrentProvisioning) error {
	paths := append([]string{p.floors, p.floors + ".state", p.floors + ".state.lease"}, p.paths...)
	infos := make([]os.FileInfo, len(paths))
	for i, path := range paths {
		canonical, err := currentCustodyPath(path)
		if err != nil {
			return err
		}
		paths[i] = canonical
		for j := 0; j < min(i, 3); j++ {
			if paths[j] == canonical {
				return errCurrentCustody
			}
		}
		before, err := os.Lstat(canonical)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !before.Mode().IsRegular() {
			return errors.Join(errCurrentCustody, err)
		}
		f, err := os.Open(canonical)
		if err != nil {
			return errors.Join(errCurrentCustody, err)
		}
		opened, statErr := f.Stat()
		privateErr := privatefile.Check(f)
		after, pathErr := os.Lstat(canonical)
		closeErr := f.Close()
		if err = errors.Join(statErr, privateErr, pathErr, closeErr); err != nil {
			return errors.Join(errCurrentCustody, err)
		}
		if !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
			return errCurrentCustody
		}
		infos[i] = opened
		for j := 0; j < min(i, 3); j++ {
			if infos[j] != nil && os.SameFile(infos[j], opened) {
				return errCurrentCustody
			}
		}
	}
	return nil
}

// LoadCurrentProvisioning is also used as a detached, read-only profile probe.
// Startup therefore re-loads and validates its exact original inputs under the
// custody lease. The preliminary path check only prevents aliasing the lease
// itself with a provisioned key or family; it does not authorize startup.
func openCurrentCustody(p *CurrentProvisioning, mode string) (_ *CurrentProvisioning, _ *currentCustody, floors s3aFloors, err error) {
	if p == nil || p.source == "" || p.binding == [32]byte{} || mode != "fresh" && mode != "resume" {
		return nil, nil, floors, errCurrentCustody
	}
	if err = currentCustodyPaths(p); err != nil {
		return nil, nil, floors, err
	}
	lease, err := mutationlog.AcquireFileWALLease(p.floors + ".state")
	if err != nil {
		return nil, nil, floors, errors.Join(errCurrentCustody, err)
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, lease.Close())
		}
	}()
	verified, err := LoadCurrentProvisioning(p.source)
	if err != nil || verified.binding != p.binding || verified.floors != p.floors {
		return nil, nil, floors, errors.Join(errCurrentCustody, err)
	}
	if err = currentCustodyPaths(verified); err != nil {
		return nil, nil, floors, err
	}
	state := currentCustodyState{Version: currentCustodyVersion, Phase: "RUNNING", Cycle: 1, Binding: verified.binding}
	if mode == "fresh" {
		for _, path := range []string{p.floors, p.floors + ".state"} {
			if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				return nil, nil, floors, errors.Join(errCurrentCustody, e)
			}
		}
	} else {
		var clean currentCustodyState
		_, err = currentCustodyDocument(p.floors+".state", 4096, &clean)
		if err != nil || clean.Version != currentCustodyVersion || clean.Phase != "CLEAN" || clean.Cycle == 0 || clean.Cycle == math.MaxUint64 ||
			clean.Binding != verified.binding || clean.Floors == [32]byte{} {
			return nil, nil, floors, errors.Join(errCurrentCustody, err)
		}
		raw, e := currentCustodyDocument(p.floors, 16<<10, &floors)
		if e != nil || sha256.Sum256(raw) != clean.Floors || !currentCustodyFloors(floors, verified) {
			return nil, nil, floors, errors.Join(errCurrentCustody, e)
		}
		state.Cycle = clean.Cycle + 1
	}
	transferred = true
	return verified, &currentCustody{provisioning: verified, lease: lease, state: state, io: nativeCurrentCustodyIO()}, floors, nil
}

func currentCustodyDocument(path string, limit int64, target any) ([]byte, error) {
	raw, err := currentDocument(path, limit, target)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(bytes.TrimSuffix(raw, []byte{'\n'}), canonical) {
		return nil, errors.Join(errCurrentCustody, err)
	}
	return raw, nil
}

func currentCustodyFloors(f s3aFloors, p *CurrentProvisioning) bool {
	return f.M.Binding == p.profile.Membership && f.M.Version != 0 && f.P.Scope != [32]byte{} && f.P.Index != 0 && f.B.ScopeDigest != [32]byte{} && f.B.LocalIndex != 0
}

func (c *currentCustody) running() error {
	if c == nil || c.lease == nil || c.attempted {
		return errCurrentCustody
	}
	c.attempted = true
	err := c.lease.WithPath(func(path string) error {
		if err := currentCustodyPaths(c.provisioning); err != nil {
			return err
		}
		raw, err := json.Marshal(c.state)
		if err != nil {
			return err
		}
		return c.io.write(path, raw)
	})
	c.started = err == nil
	return err
}

func (c *currentCustody) complete(floors s3aFloors) error {
	if c == nil || !c.started || c.finished || !currentCustodyFloors(floors, c.provisioning) {
		return errCurrentCustody
	}
	c.finished = true // No retry over an uncertain filesystem result.
	return c.lease.WithPath(func(path string) error {
		if err := currentCustodyPaths(c.provisioning); err != nil {
			return err
		}
		raw, err := json.Marshal(floors)
		if err != nil || len(raw) > 16<<10 {
			return errors.Join(errCurrentCustody, err)
		}
		if err = c.io.write(c.provisioning.floors, raw); err != nil {
			return err
		}
		clean := c.state
		clean.Phase, clean.Floors = "CLEAN", sha256.Sum256(raw)
		raw, err = json.Marshal(clean)
		if err != nil {
			return err
		}
		return c.io.write(path, raw)
	})
}

func (c *currentCustody) close() error {
	if c == nil || c.lease == nil {
		return nil
	}
	return c.lease.Close()
}

// Neither write is a two-file transaction. RUNNING invalidates the previous
// CLEAN before native journals may change; CLEAN is the last publication after
// final floor durability and successful journal/resource cleanup.
func (ops currentCustodyIO) write(path string, raw []byte) (err error) {
	f, err := ops.createTemp(filepath.Dir(path), filepath.Base(path)+".next-")
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, f.Close())
		}
		_ = os.Remove(f.Name())
	}()
	n, err := f.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return err
	}
	err = f.Close()
	closed = true
	if err != nil {
		return err
	}
	if err = ops.rename(f.Name(), path); err != nil {
		return err
	}
	return ops.syncDirectory(filepath.Dir(path))
}

// Shutdown is terminal and is called only after App's public and private
// workers have joined. The network owner then joins its own producers and
// captures floors before closing M/P/B. Generic Close never publishes CLEAN.
func (o *CurrentAuthority) Shutdown() error { return o.finish(true) }

func (o *CurrentAuthority) finish(orderly bool) error {
	if o == nil {
		return nil
	}
	var failure any
	o.closeOnce.Do(func() {
		defer func() {
			if value := recover(); value != nil {
				failure = value
				o.closeErr = errors.Join(o.closeErr, errS3ACleanup)
			}
			o.observations.mu.Lock()
			clear(o.observations.items)
			o.observations.order = nil
			o.observations.mu.Unlock()
			o.closeErr = errors.Join(o.closeErr, o.custody.close())
		}()
		if o.origin == nil {
			return
		}
		floors, err := o.origin.network.finish(orderly)
		o.closeErr = err
		if orderly && err == nil && o.custody != nil {
			o.closeErr = o.custody.complete(floors)
		}
		o.closeOrderly = orderly && o.closeErr == nil
	})
	if failure != nil {
		panic(failure)
	}
	if orderly && !o.closeOrderly {
		return errors.Join(o.closeErr, errCurrentCustody)
	}
	return o.closeErr
}
