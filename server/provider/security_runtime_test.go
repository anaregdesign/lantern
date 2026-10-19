package provider

import (
	"sync"
	"testing"
)

func TestSecurityRuntimeOffHasNoOIDCPrerequisites(t *testing.T) {
	runtime, cleanup, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if runtime.mode != "off" || runtime.current != nil || runtime.verifier != nil || runtime.workflow != nil {
		t.Fatal("OFF constructed protected runtime")
	}
}

func TestSecurityRuntimeOrderlyAndAbortAreTerminal(t *testing.T) {
	r, cleanup, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var joined sync.WaitGroup
	for range 4 {
		joined.Go(func() {
			if err := r.Shutdown(); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	if err := r.Close(); err != nil {
		t.Fatal("Wire cleanup changed completed result", err)
	}
	r = &SecurityRuntime{}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if r.Shutdown() == nil {
		t.Fatal("abort upgraded to orderly success")
	}
}
