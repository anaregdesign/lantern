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
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

// These are filesystem/lifecycle tests, not native time qualification. Their
// existing private protocol fixture supplies the closed, independently checked
// M/P/B cut; the whole-process gate separately uses production constructors.
func currentCustodyFixture(t *testing.T) (*CurrentProvisioning, *authorityOriginOwner) {
	t.Helper()
	n, origins, _ := authorityTestComposite(t)
	c, dir := n.configs[1], t.TempDir()
	g := n.f.genesis.state
	genesis := currentGenesisDocument{CurrentPublicVersion, g.projection.cut.Domain, g.projection.cut.Cohort, g.projection.cut.Generation,
		g.projection.cut.Fences, g.projection.Image(), g.configuration, n.f.genesis.roots, n.f.members, n.f.origins, n.f.trust.bounds, sha256.Sum256([]byte(authorityTimeProfileDescription))}
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	genesisRaw, err := json.Marshal(genesis)
	if err != nil {
		t.Fatal(err)
	}
	genesisPath := write("genesis.json", genesisRaw)
	p := c.Participant
	node := currentNodeDocument{Version: CurrentPublicVersion, GenesisFile: genesisPath, GenesisSHA256: sha256.Sum256(genesisRaw),
		Participant: currentParticipantDocument{p.Member, p.Incarnation, p.PPath, p.BPath, p.PIdentity, p.PEpoch, p.PPolicy, p.BScope, p.OwnedOrigin, p.PendingBytes, p.PendingCount, p.OutboxBytes},
		Membership:  currentMembershipDocument{c.Membership.Path, write("membership.pub", c.Membership.Key), write("membership.signed", c.Manifest), c.Membership.Profile, c.Membership.Self},
		Identity:    c.Identity, OriginKeyFile: filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key"), FloorsFile: filepath.Join(dir, "floors.json"), ListenAddress: n.listeners[1].Addr().String(), Limits: c.Limits}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	provisioned, err := LoadCurrentProvisioning(write("node.json", raw))
	if err != nil {
		t.Fatal(err)
	}
	return provisioned, origins[1]
}

func currentCustodyOpenTest(t *testing.T, p *CurrentProvisioning, mode string) *currentCustody {
	t.Helper()
	_, c, _, err := openCurrentCustody(p, mode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.close() })
	return c
}

func currentCustodyReadState(t *testing.T, p *CurrentProvisioning) currentCustodyState {
	t.Helper()
	var state currentCustodyState
	if _, err := currentDocument(p.floors+".state", 4096, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCurrentCustodyOrderlyCyclesAndLease(t *testing.T) {
	p, origin := currentCustodyFixture(t)
	before, err := os.ReadFile(p.source)
	if err != nil {
		t.Fatal(err)
	}
	c := currentCustodyOpenTest(t, p, "fresh")
	if _, err = os.Stat(p.floors + ".state"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only preflight consumed a checkpoint", err)
	}
	if _, second, _, err := openCurrentCustody(p, "fresh"); err == nil {
		_ = second.close()
		t.Fatal("duplicate custody owner admitted")
	}
	if err = c.running(); err != nil {
		t.Fatal(err)
	}
	if state := currentCustodyReadState(t, p); state.Phase != "RUNNING" || state.Cycle != 1 || state.Floors != [32]byte{} {
		t.Fatal("startup did not invalidate old CLEAN", state)
	}
	owner := &CurrentAuthority{origin: origin, custody: c}
	create := c.io.createTemp
	c.io.createTemp = func(dir, prefix string) (currentCustodyFile, error) {
		// Each family has completed close before either final checkpoint write;
		// the outer custody lease still excludes a competing startup.
		for _, path := range []string{p.config.Membership.Path, p.config.Participant.PPath, p.config.Participant.BPath} {
			lease, err := mutationlog.AcquireFileWALLease(path)
			if err != nil {
				return nil, err
			}
			if err := lease.Close(); err != nil {
				return nil, err
			}
		}
		if lease, err := mutationlog.AcquireFileWALLease(p.floors + ".state"); err == nil {
			_ = lease.Close()
			return nil, errors.New("custody lease released before final publication")
		}
		return create(dir, prefix)
	}
	var joined sync.WaitGroup
	for range 8 {
		joined.Go(func() {
			if err := owner.Shutdown(); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	if err := owner.Close(); err != nil {
		t.Fatal("Wire cleanup changed terminal result", err)
	}
	state := currentCustodyReadState(t, p)
	raw, err := os.ReadFile(p.floors)
	if err != nil || state.Phase != "CLEAN" || state.Cycle != 1 || sha256.Sum256(raw) != state.Floors {
		t.Fatal("normal shutdown did not publish a complete pair", state, err)
	}
	// Native preflight failure before RUNNING leaves the old CLEAN reusable.
	preflight := currentCustodyOpenTest(t, p, "resume")
	if err := preflight.close(); err != nil || currentCustodyReadState(t, p) != state {
		t.Fatal("read-only abort destroyed previous CLEAN", err)
	}
	_, resumed, floors, err := openCurrentCustody(p, "resume")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.close() }()
	if err := resumed.running(); err != nil {
		t.Fatal(err)
	}
	if currentCustodyReadState(t, p).Cycle != 2 {
		t.Fatal("old CLEAN reused without a new cycle")
	}
	// Pure custody state-machine completion: the actual native restart and
	// fresh authority are tested by the process gate, not simulated here.
	if err := resumed.complete(floors); err != nil {
		t.Fatal(err)
	}
	if state := currentCustodyReadState(t, p); state.Phase != "CLEAN" || state.Cycle != 2 {
		t.Fatal("second terminal cycle missing", state)
	}
	after, err := os.ReadFile(p.source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("runtime rewrote original provisioning", err)
	}
}

func TestCurrentCustodyRejectsPartialMixedAndNoncanonicalPairs(t *testing.T) {
	for _, damage := range []string{"floor-missing", "state-missing", "running", "hash", "binding", "version", "cycle-zero", "cycle-overflow", "unknown", "duplicate", "state-whitespace", "floor-whitespace", "floor-unknown", "floor-oversize", "state-oversize", "original-node"} {
		t.Run(damage, func(t *testing.T) {
			p, origin := currentCustodyFixture(t)
			c := currentCustodyOpenTest(t, p, "fresh")
			if err := c.running(); err != nil {
				t.Fatal(err)
			}
			if err := (&CurrentAuthority{origin: origin, custody: c}).Shutdown(); err != nil {
				t.Fatal(err)
			}
			state := currentCustodyReadState(t, p)
			path := p.floors + ".state"
			var raw []byte
			var err error
			switch damage {
			case "floor-missing", "state-missing":
				if damage == "floor-missing" {
					path = p.floors
				}
				err = os.Remove(path)
			case "running":
				state.Phase, state.Floors = "RUNNING", [32]byte{}
			case "hash":
				state.Floors[0] ^= 1
			case "binding":
				state.Binding[0] ^= 1
			case "version":
				state.Version++
			case "cycle-zero":
				state.Cycle = 0
			case "cycle-overflow":
				state.Cycle = math.MaxUint64
			case "unknown", "duplicate":
				raw, _ = json.Marshal(state)
				field := `,"Ready":true}`
				if damage == "duplicate" {
					field = `,"Cycle":1}`
				}
				raw = append(raw[:len(raw)-1], field...)
			case "floor-unknown":
				path, raw = p.floors, []byte(`{"Ready":true}`)
			case "state-whitespace":
				raw, _ = json.Marshal(state)
				raw = append([]byte{' '}, raw...)
			case "floor-whitespace":
				floor, e := os.ReadFile(p.floors)
				if e != nil {
					t.Fatal(e)
				}
				floor = append(floor, '\n', '\n')
				if e := os.WriteFile(p.floors, floor, 0600); e != nil {
					t.Fatal(e)
				}
				state.Floors = sha256.Sum256(floor)
			case "floor-oversize":
				path, raw = p.floors, bytes.Repeat([]byte{'x'}, (16<<10)+1)
			case "state-oversize":
				raw = bytes.Repeat([]byte{'x'}, 4097)
			case "original-node":
				// Even a semantically harmless permitted newline changes the
				// original node bytes and cannot silently adopt old custody.
				path = p.source
				raw, err = os.ReadFile(path)
				raw = append(raw, '\n')
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(damage, "-missing") {
				if raw == nil {
					raw, err = json.Marshal(state)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, mode := range []string{"resume", "fresh"} {
				if _, next, _, err := openCurrentCustody(p, mode); err == nil {
					_ = next.close()
					t.Fatal("accepted damaged custody", mode)
				}
			}
		})
	}
}

func TestCurrentCustodyResumeRunningFailurePreservesOnlyUnconsumedClean(t *testing.T) {
	for _, stage := range []string{"write", "rename-after", "directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			p, origin := currentCustodyFixture(t)
			first := currentCustodyOpenTest(t, p, "fresh")
			if err := first.running(); err != nil {
				t.Fatal(err)
			}
			if err := (&CurrentAuthority{origin: origin, custody: first}).Shutdown(); err != nil {
				t.Fatal(err)
			}
			second := currentCustodyOpenTest(t, p, "resume")
			second.io = currentCustodyFaultIO(1, stage, errors.New("RUNNING failed"))
			if err := second.running(); err == nil || second.started {
				t.Fatal("uncertain RUNNING authorized native update", err)
			}
			if err := second.close(); err != nil {
				t.Fatal(err)
			}
			_, next, _, err := openCurrentCustody(p, "resume")
			if stage == "write" {
				if err != nil || currentCustodyReadState(t, p).Cycle != 1 {
					t.Fatal("unconsumed previous CLEAN could not be retried", err)
				}
				_ = next.close()
			} else if err == nil {
				_ = next.close()
				t.Fatal("published RUNNING reused previous CLEAN")
			}
		})
	}
}

func TestCurrentCustodyRejectsFileAliasesAndUnsafeDescriptors(t *testing.T) {
	for _, damage := range []string{"floor-P", "state-key", "lease-P-lease", "same-path", "symlink", "directory", "permissions"} {
		t.Run(damage, func(t *testing.T) {
			p, _ := currentCustodyFixture(t)
			var err error
			switch damage {
			case "floor-P":
				err = os.Link(p.config.Participant.PPath, p.floors)
			case "state-key":
				err = os.Link(p.config.Identity.VotingKey, p.floors+".state")
			case "lease-P-lease":
				err = os.Link(p.config.Participant.PPath+".lease", p.floors+".state.lease")
			case "same-path":
				p.floors = p.config.Participant.BPath + ".tip"
			case "symlink":
				err = os.Symlink(p.config.Identity.VotingKey, p.floors)
			case "directory":
				err = os.Mkdir(p.floors, 0700)
			case "permissions":
				if runtime.GOOS == "windows" {
					return // Windows ACL validation belongs to core/privatefile.
				}
				err = os.WriteFile(p.floors, []byte("unsafe"), 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := currentCustodyPaths(p); err == nil {
				t.Fatal("path-only validation admitted an unsafe descriptor/alias")
			}
		})
	}
}

type currentCustodyFaultFile struct {
	currentCustodyFile
	stage string
	err   error
}

func (f currentCustodyFaultFile) Write(raw []byte) (int, error) {
	if f.stage == "short-write" {
		return f.currentCustodyFile.Write(raw[:len(raw)-1])
	}
	if f.stage == "write" {
		return 0, f.err
	}
	return f.currentCustodyFile.Write(raw)
}
func (f currentCustodyFaultFile) Sync() error {
	if f.stage == "sync" {
		return f.err
	}
	return f.currentCustodyFile.Sync()
}
func (f currentCustodyFaultFile) Close() error {
	err := f.currentCustodyFile.Close()
	if f.stage == "close" {
		return errors.Join(err, f.err)
	}
	return err
}

func currentCustodyFaultIO(wanted int, stage string, failure error) currentCustodyIO {
	ops := nativeCurrentCustodyIO()
	create, rename, syncDirectory := ops.createTemp, ops.rename, ops.syncDirectory
	count := 0
	ops.createTemp = func(dir, prefix string) (currentCustodyFile, error) {
		count++
		if count == wanted && stage == "create" {
			return nil, failure
		}
		f, err := create(dir, prefix)
		if err == nil && count == wanted {
			return currentCustodyFaultFile{f, stage, failure}, nil
		}
		return f, err
	}
	ops.rename = func(old, next string) error {
		if count == wanted && stage == "rename" {
			return failure
		}
		err := rename(old, next)
		if count == wanted && stage == "rename-after" {
			return errors.Join(err, failure)
		}
		return err
	}
	ops.syncDirectory = func(dir string) error {
		if count == wanted && stage == "directory-sync" {
			return failure
		}
		return syncDirectory(dir)
	}
	return ops
}

func TestCurrentCustodyEveryPersistenceBoundary(t *testing.T) {
	for _, phase := range []string{"RUNNING", "floor", "CLEAN"} {
		for _, stage := range []string{"create", "write", "short-write", "sync", "close", "rename", "rename-after", "directory-sync"} {
			t.Run(phase+"/"+stage, func(t *testing.T) {
				p, origin := currentCustodyFixture(t)
				c := currentCustodyOpenTest(t, p, "fresh")
				failure := errors.New("injected custody I/O failure")
				if phase == "RUNNING" {
					c.io = currentCustodyFaultIO(1, stage, failure)
					if err := c.running(); err == nil || c.started {
						t.Fatal("RUNNING uncertainty permitted native startup", err)
					}
					if err := c.running(); err == nil {
						t.Fatal("retried an uncertain RUNNING write")
					}
					return
				}
				if err := c.running(); err != nil {
					t.Fatal(err)
				}
				wanted := 1
				if phase == "CLEAN" {
					wanted = 2
				}
				c.io = currentCustodyFaultIO(wanted, stage, failure)
				owner := &CurrentAuthority{origin: origin, custody: c}
				err := owner.Shutdown()
				if stage == "short-write" {
					failure = io.ErrShortWrite
				}
				if !errors.Is(err, failure) || !errors.Is(owner.Close(), failure) {
					t.Fatal("terminal shutdown error was lost", err)
				}
				state := currentCustodyReadState(t, p)
				complete := phase == "CLEAN" && (stage == "rename-after" || stage == "directory-sync")
				if (state.Phase == "CLEAN") != complete {
					t.Fatal("unexpected crash-boundary publication", state, complete)
				}
				_, next, _, err := openCurrentCustody(p, "resume")
				if complete {
					if err != nil {
						t.Fatal("complete final CLEAN must reach ordinary full validation", err)
					}
					_ = next.close()
				} else if err == nil {
					_ = next.close()
					t.Fatal("partial pair resumed")
				}
			})
		}
	}
}

func TestCurrentCustodyAbortAndNativeCloseFailuresNeverPublishClean(t *testing.T) {
	for _, fault := range []string{"abort", "P", "B", "M", "panic-P", "panic-B", "panic-M", "poison", "P-poison", "B-poison", "M-closed"} {
		t.Run(fault, func(t *testing.T) {
			p, origin := currentCustodyFixture(t)
			c := currentCustodyOpenTest(t, p, "fresh")
			if err := c.running(); err != nil {
				t.Fatal(err)
			}
			n := origin.network
			failure := errors.New("injected native terminal fault")
			switch fault {
			case "P":
				n.kernel.p.walOwner = s3aErrorCloser{n.kernel.p.walOwner, failure}
			case "B":
				n.kernel.b.walOwner = s3aErrorCloser{n.kernel.b.walOwner, failure}
			case "M", "panic-M":
				n.hooks.afterMembershipClose = func() error {
					if fault == "panic-M" {
						panic(failure)
					}
					return failure
				}
			case "panic-P":
				n.kernel.p.walOwner = s2cPanicCloser{n.kernel.p.walOwner}
			case "panic-B":
				n.kernel.b.walOwner = s2cPanicCloser{n.kernel.b.walOwner}
			case "poison":
				n.kernel.gate.Lock()
				n.kernel.unknown = failure
				n.kernel.gate.Unlock()
			case "P-poison":
				n.kernel.p.gate.Lock()
				n.kernel.p.unknown = failure
				n.kernel.p.gate.Unlock()
			case "B-poison":
				n.kernel.b.gate.Lock()
				n.kernel.b.unknown = failure
				n.kernel.b.gate.Unlock()
			case "M-closed":
				if err := n.membership.Close(); err != nil {
					t.Fatal(err)
				}
			}
			owner := &CurrentAuthority{origin: origin, custody: c}
			var recovered any
			var err error
			func() {
				defer func() { recovered = recover() }()
				if fault == "abort" {
					err = owner.Close()
				} else {
					err = owner.Shutdown()
				}
			}()
			if fault != "abort" && err == nil && recovered == nil {
				t.Fatal("native fault became success")
			}
			if owner.Shutdown() == nil || currentCustodyReadState(t, p).Phase != "RUNNING" {
				t.Fatal("abort or failed close became CLEAN")
			}
			if _, err := os.Stat(p.floors); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed cleanup published floors", err)
			}
			for _, path := range []string{p.floors + ".state", p.config.Membership.Path, p.config.Participant.PPath, p.config.Participant.BPath} {
				lease, err := mutationlog.AcquireFileWALLease(path)
				if err != nil {
					t.Fatal("failure stranded native ownership", path, err)
				}
				_ = lease.Close()
			}
			if n.identity.signer.key != nil || len(n.kernel.key) != 0 {
				t.Fatal("terminal cleanup retained signer")
			}
		})
	}
}
