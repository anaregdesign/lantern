package security

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// Cross-cutting native tests compose the typed membership checkpoint, identity
// files, P/B lifecycle, TLS handlers/clients, delivery credits and phase recovery.
// IPC below is test-only observation/control; the protocol travels only on TLS.
type s3aChildSpec struct {
	Participant    s2cParticipantConfig
	Profile        peerauth.ControlProfile
	Operator       []byte
	Self           peerauth.Member
	MembershipPath string
	Manifest       []byte
	Identity       s3aIdentityFiles
	Limits         s3aLimits
	Now            int64
	Fresh          bool
	Floors         s3aFloors
	DropAccepted   bool
	CrashCut       string
}

type s3aChildCommand struct {
	Kind string
	ID   FullChangeID
}
type s3aChildReply struct {
	Floors      s3aFloors
	Accepted    bool
	Value       [32]byte
	Slot        uint64
	Status      S1LookupState
	Handoff     [32]byte
	Disposition S1Disposition
}

func TestS3AGateChild(t *testing.T) {
	path := os.Getenv("LANTERN_S3A_CHILD")
	if path == "" {
		t.Skip("native child process only")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec s3aChildSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	fixture := s2cTestCluster(t, 3)
	spec.Participant.Trust = fixture.trust
	spec.Participant.Key = nil
	c := s3aConfig{Participant: spec.Participant, Identity: spec.Identity, Manifest: spec.Manifest, Limits: spec.Limits,
		Membership: peerauth.ControlStoreOptions{Path: spec.MembershipPath, Key: spec.Operator, Profile: spec.Profile, Self: spec.Self, Now: func() time.Time { return time.Unix(0, spec.Now).UTC() }}}
	if spec.DropAccepted {
		c.hooks = &s3aHooks{beforeSend: func(_ context.Context, _ uint32, raw []byte) error {
			if raw[len(s2cMessageMagic)] == s2cAccepted {
				return errors.New("test lost Accepted")
			}
			return nil
		}}
	}
	var owner *s3aOwner
	if spec.Fresh {
		owner, err = createS3AOwner(c)
	} else {
		owner, err = resumeS3AOwner(c, spec.Floors)
	}
	if err != nil {
		t.Fatal("native child construction", err)
	}
	defer func() { _ = owner.Close() }()
	if spec.CrashCut != "" {
		exit := func() { os.Exit(91) }
		owner.kernel.hooks = &s2cParticipantHooks{}
		switch spec.CrashCut {
		case "afterChosen":
			owner.kernel.hooks.afterChosen = exit
		case "afterB":
			owner.kernel.hooks.afterB = exit
		case "beforeDrained":
			owner.kernel.hooks.beforeDrained = exit
		default:
			t.Fatal("unknown cut")
		}
	}
	u, _ := url.Parse(spec.Self.Origin)
	listener, err := net.Listen("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Start(listener); err != nil {
		t.Fatal(err)
	}
	fmt.Println("S3A_READY")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var command s3aChildCommand
		if json.Unmarshal(scanner.Bytes(), &command) != nil {
			t.Fatal("child command")
		}
		if command.Kind == "crash" {
			os.Exit(73)
		}
		if command.Kind != "inspect" {
			t.Fatal("unknown child command")
		}
		m, err := owner.membership.Floor()
		if err != nil {
			t.Fatal(err)
		}
		owner.kernel.gate.Lock()
		reply := s3aChildReply{Floors: s3aFloors{M: m, P: owner.kernel.p.floor, B: owner.kernel.b.receipt}, Slot: owner.kernel.replayState.slot}
		if accepted := owner.kernel.accepted; accepted != nil {
			reply.Accepted = true
			reply.Value = accepted.value
		}
		owner.kernel.gate.Unlock()
		if command.ID != (FullChangeID{}) {
			status, out, _, err := owner.kernel.LookupOriginal(command.ID)
			if err != nil {
				t.Fatal(err)
			}
			reply.Status = status
			if out != nil {
				reply.Handoff = out.HandoffDigest()
				reply.Disposition = out.Disposition()
			}
		}
		encoded, _ := json.Marshal(reply)
		fmt.Println(string(encoded))
	}
}

type s3aChild struct {
	t      *testing.T
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Scanner
	done   chan error
	errOut bytes.Buffer
	waited bool
}

func (n *s3aTestNetwork) child(id uint32, fresh bool, floors s3aFloors, drop bool, cut string) *s3aChild {
	n.t.Helper()
	c := n.configs[id]
	participant := c.Participant
	participant.Trust = nil
	spec := s3aChildSpec{Participant: participant, Profile: c.Membership.Profile, Operator: c.Membership.Key, Self: c.Membership.Self, MembershipPath: c.Membership.Path, Manifest: c.Manifest, Identity: c.Identity, Limits: c.Limits, Now: n.clock.Load(), Fresh: fresh, Floors: floors, DropAccepted: drop, CrashCut: cut}
	raw, err := json.Marshal(spec)
	if err != nil {
		n.t.Fatal(err)
	}
	path := filepath.Join(n.dir, fmt.Sprintf("child-%d.json", id))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		n.t.Fatal(err)
	}
	_ = n.listeners[id].Close()
	ctx, cancel := context.WithTimeout(n.t.Context(), 30*time.Second)
	n.t.Cleanup(cancel)
	child := &s3aChild{t: n.t, done: make(chan error, 1)}
	child.cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestS3AGateChild$")
	child.cmd.Env = append(os.Environ(), "LANTERN_S3A_CHILD="+path)
	child.cmd.Stderr = &child.errOut
	child.input, err = child.cmd.StdinPipe()
	if err != nil {
		n.t.Fatal(err)
	}
	output, err := child.cmd.StdoutPipe()
	if err != nil {
		n.t.Fatal(err)
	}
	child.output = bufio.NewScanner(output)
	if err := child.cmd.Start(); err != nil {
		n.t.Fatal(err)
	}
	go func() { child.done <- child.cmd.Wait() }()
	n.t.Cleanup(func() {
		_ = child.cmd.Process.Kill()
		_ = child.input.Close()
		if !child.waited {
			select {
			case <-child.done:
				child.waited = true
			case <-time.After(5 * time.Second):
				n.t.Error("native child cleanup did not drain")
			}
		}
		if child.waited && strings.Contains(child.errOut.String(), "DATA RACE") {
			n.t.Error("native child race report", child.errOut.String())
		}
	})
	if !child.output.Scan() || child.output.Text() != "S3A_READY" {
		n.t.Fatal("child readiness", child.output.Text(), child.output.Err())
	}
	n.t.Logf("native child ready: voter=%d pid=%d fresh=%t cut=%s", id, child.cmd.Process.Pid, fresh, cut)
	return child
}

func (c *s3aChild) inspect(id FullChangeID) s3aChildReply {
	c.t.Helper()
	raw, _ := json.Marshal(s3aChildCommand{Kind: "inspect", ID: id})
	if _, err := fmt.Fprintln(c.input, string(raw)); err != nil {
		c.t.Fatal(err)
	}
	if !c.output.Scan() {
		c.t.Fatal("child observation EOF", c.output.Err())
	}
	var reply s3aChildReply
	if err := json.Unmarshal(c.output.Bytes(), &reply); err != nil {
		c.t.Fatal("child observation", c.output.Text(), err)
	}
	return reply
}

func (c *s3aChild) exited(want int) {
	c.t.Helper()
	select {
	case err := <-c.done:
		c.waited = true
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != want {
			c.t.Fatal("child exit", want, err, c.errOut.String())
		}
		if strings.Contains(c.errOut.String(), "DATA RACE") {
			c.t.Fatal("native child race report", c.errOut.String())
		}
	case <-time.After(10 * time.Second):
		c.t.Fatal("child failed to exit", want)
	}
}

func (c *s3aChild) crash() {
	c.t.Helper()
	_, _ = fmt.Fprintln(c.input, `{"Kind":"crash"}`)
	c.exited(73)
}

func TestS3AGateHiddenChoiceTLSProcessRecovery(t *testing.T) {
	for _, both := range []bool{false, true} {
		for _, cut := range []string{"afterChosen", "afterB", "beforeDrained"} {
			t.Run(fmt.Sprintf("A+B-%t/%s", both, cut), func(t *testing.T) {
				var recovering atomic.Bool
				n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
					c.hooks = &s3aHooks{beforeSend: func(_ context.Context, to uint32, raw []byte) error {
						kind := raw[len(s2cMessageMagic)]
						if !recovering.Load() && (kind == s2cAccepted || id == 1 && kind == s2cAccept && (to == 1 || to == 3 && !both)) {
							return errors.New("test hidden-choice lost delivery")
						}
						return nil
					}}
				})
				n.start(1)
				n.start(3)
				child := n.child(2, true, s3aFloors{}, true, "")
				id, value := n.origin(1, 41)
				ctx := s3aTestContext(t)
				if err := n.nodes[1].Begin(ctx, value); err != nil {
					t.Fatal(err)
				}
				var retained s3aChildReply
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					retained = child.inspect(FullChangeID{})
					if retained.Accepted {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				if !retained.Accepted || retained.Value != value || retained.Slot != 0 {
					t.Fatal("A durable hidden accept", retained)
				}
				deadline = time.Now().Add(5 * time.Second)
				for both && time.Now().Before(deadline) {
					n.nodes[3].kernel.gate.Lock()
					accepted := n.nodes[3].kernel.accepted != nil
					n.nodes[3].kernel.gate.Unlock()
					if accepted {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				n.nodes[3].kernel.gate.Lock()
				acceptedB := n.nodes[3].kernel.accepted != nil
				slotB := n.nodes[3].kernel.replayState.slot
				n.nodes[3].kernel.gate.Unlock()
				if acceptedB != both || slotB != 0 {
					t.Fatal("B hidden accept history", acceptedB, both, slotB)
				}
				if err := n.nodes[1].Close(); err != nil {
					t.Fatal(err)
				} // Original H issuer unavailable.
				child.crash() // Native P survives; all process-local protocol proofs die.
				recovering.Store(true)
				child = n.child(2, false, retained.Floors, false, cut)
				if r, err := n.nodes[3].Drive(ctx, [32]byte{}); err != nil || r.ControlSlot != 1 {
					t.Fatal("actual higher-ballot network recovery", r, err)
				}
				child.exited(91) // CHOSEN -> B -> DRAINED cut, reached through real TLS.
				child = n.child(2, false, retained.Floors, false, "")
				got := child.inspect(id)
				if got.Status != S1Known || got.Handoff != value || got.Disposition != S1Applied || got.Slot != 1 {
					t.Fatal("cold exact H/outcome", got)
				}
				status, out, _, err := n.nodes[3].kernel.LookupOriginal(id)
				if err != nil || status != S1Known || out.HandoffDigest() != value || out.Disposition() != S1Applied {
					t.Fatal("recovery fabricated/replaced original H", status, out, err)
				}
				t.Logf("real TLS higher-ballot recovery retained exact expired H after origin loss; independent floors P=%d B=%d", retained.Floors.P.Index, retained.Floors.B.ControlSlot)
			})
		}
	}
}
