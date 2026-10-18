package provider

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

func TestSecurityDataRequiresSameOwnedRuntime(t *testing.T) {
	data := securityDataFixture(t)
	config := SecurityConfig{Mode: "off"}
	runtime, closeRuntime, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	runtime.mode = "oidc"
	foreign := service.NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
	if _, _, err := runtime.PublicDataHTTPHandler(foreign); err == nil {
		t.Fatal("foreign graph passed serving composition")
	}
	runtime.mode = "off"
	owned := data.NewLanternService(nil)
	if path, handler, err := runtime.PublicDataHTTPHandler(owned); err != nil || path != "/graph.v1.LanternService/" || handler == nil {
		t.Fatal("owned public handler", err)
	}
	if path, handler, err := runtime.BrowserDataHTTPHandler(owned); err != nil || path != "/browser/graph.v1.LanternService/" || handler == nil {
		t.Fatal("owned browser handler", err)
	}
}
