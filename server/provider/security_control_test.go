package provider

import (
	"testing"

	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

func TestSecurityControlMountIncludesDedicatedSurface(t *testing.T) {
	runtime, closeRuntime, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	path, handler := runtime.PublicControlHTTPHandler()
	if path != "/"+graphv1connect.LanternSecurityServiceName+"/" || handler == nil {
		t.Fatal("control surface omitted")
	}
}
