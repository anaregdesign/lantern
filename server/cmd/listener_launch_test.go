package main

import (
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
	"os"
	"testing"
)

func TestListenerLaunchRejectsUnboundArguments(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	for _, args := range [][]string{{"server", "arbitrary"}, {"server", listenerlaunch.Argument}, {"server", listenerlaunch.Argument, "bad"}, {"server", listenerlaunch.Argument, "bad", "extra"}} {
		os.Args = args
		if launch, err := prepareListenerLaunch(t.Context(), func() {}); err == nil {
			launch.Close()
			t.Fatal("unbound local launch admitted")
		}
	}
	os.Args = []string{"server"}
	launch, err := prepareListenerLaunch(t.Context(), func() {})
	if err != nil || launch.owner != nil || launch.acknowledge() != nil {
		t.Fatal("ordinary startup changed", err)
	}
	launch.Close()
}
