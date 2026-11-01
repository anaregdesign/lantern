package provider

import (
	"bytes"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/rpcwire"
	"google.golang.org/protobuf/proto"
)

func TestSecurityDataRequiresSameOwnedRuntime(t *testing.T) {
	data := securityDataFixture(t)
	config := SecurityConfig{Mode: "off"}
	runtime, closeRuntime, err := NewSecurityRuntime(t.Context(), config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	runtime.mode = "oidc"
	foreign := dataruntime.NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
	if _, _, err := runtime.PublicDataHTTPHandler(foreign); err == nil {
		t.Fatal("foreign graph passed serving composition")
	}
	runtime.mode = "off"
	owned := data.NewExecutor(nil)
	constructed := 0
	observe := connect.WithConditionalHandlerOptions(func(connect.Spec) []connect.HandlerOption {
		constructed++
		return nil
	})
	methods := pb.File_graph_v1_graph_proto.Services().ByName("LanternService").Methods()
	if path, handler, err := runtime.PublicDataHTTPHandler(owned, observe); err != nil || path != "/graph.v1.LanternService/" || handler == nil || constructed != methods.Len() {
		t.Fatal("owned public handler", err)
	}
	if path, handler, err := runtime.BrowserDataHTTPHandler(owned, observe); err != nil || path != "/browser/graph.v1.LanternService/" || handler == nil || constructed != 2*methods.Len() {
		t.Fatal("owned browser handler", err)
	}
}

func TestCurrentDataRPCHandlerDescriptorParity(t *testing.T) {
	methods := pb.File_graph_v1_graph_proto.Services().ByName("LanternService").Methods()
	svc := graphv1connect.UnimplementedLanternServiceHandler{}
	for i := 0; i < methods.Len(); i++ {
		method := methods.Get(i)
		procedure := "/" + graphv1connect.LanternServiceName + "/" + string(method.Name())
		t.Run(string(method.Name()), func(t *testing.T) {
			var generated []connect.Spec
			graphv1connect.NewLanternServiceHandler(svc, connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
				generated = append(generated, spec)
				return nil
			}))
			var selected []connect.Spec
			newCurrentDataRPCHandler(procedure, svc, connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
				selected = append(selected, spec)
				return nil
			}))
			if len(generated) != methods.Len() || len(selected) != 1 {
				t.Fatal("constructor coverage", len(generated), len(selected), methods.Len())
			}
			var expected connect.Spec
			for _, spec := range generated {
				if spec.Procedure == procedure {
					expected = spec
				}
			}
			got := selected[0]
			if got != expected || got.Schema != method || got.IdempotencyLevel != connect.IdempotencyUnknown {
				t.Fatalf("selected spec differs: got %+v, generated %+v", got, expected)
			}
			wantStream := connect.StreamTypeUnary
			if method.IsStreamingServer() {
				wantStream = connect.StreamTypeServer
			}
			if got.StreamType != wantStream || method.IsStreamingClient() {
				t.Fatal("unsupported or mismatched streaming schema", got.StreamType)
			}
		})
	}
}

func TestCurrentDataRPCHandlerTypedMessages(t *testing.T) {
	methods := pb.File_graph_v1_graph_proto.Services().ByName("LanternService").Methods()
	for i := 0; i < methods.Len(); i++ {
		method := methods.Get(i)
		procedure := "/" + graphv1connect.LanternServiceName + "/" + string(method.Name())
		t.Run(string(method.Name()), func(t *testing.T) {
			initialized := 0
			handler := newCurrentDataRPCHandler(procedure, graphv1connect.UnimplementedLanternServiceHandler{}, connect.WithRequestInitializer(func(spec connect.Spec, message any) error {
				initialized++
				typed, ok := message.(proto.Message)
				if !ok || typed.ProtoReflect().Descriptor() != method.Input() || spec.Procedure != procedure {
					t.Fatal("selected generic request does not match its descriptor", spec.Procedure)
				}
				return nil
			}))
			contentType, body := "application/proto", []byte(nil)
			if method.IsStreamingServer() {
				contentType, body = "application/connect+proto", []byte{0, 0, 0, 0, 0}
			}
			response := currentDataRPCResponse(handler, http.MethodPost, procedure, contentType, body)
			// The generated fake's distinct method error also checks the typed
			// invocation; a constructor Spec observer alone cannot prove this.
			// Connect also initializes the extra receive used to check unary
			// cardinality, so initializer calls are not a method-dispatch count.
			want := graphv1connect.LanternServiceName + "." + string(method.Name()) + " is not implemented"
			if initialized == 0 || !strings.Contains(response.Body.String(), want) {
				t.Fatal("typed service method was not invoked", initialized, response.Code, response.Body.String())
			}
		})
	}
}

func TestCurrentDataRPCHandlerOptionParity(t *testing.T) {
	procedure := graphv1connect.LanternServiceGetVertexProcedure
	svc := graphv1connect.UnimplementedLanternServiceHandler{}
	methods := pb.File_graph_v1_graph_proto.Services().ByName("LanternService").Methods()
	for _, level := range []connect.IdempotencyLevel{connect.IdempotencyUnknown, connect.IdempotencyNoSideEffects} {
		t.Run(level.String(), func(t *testing.T) {
			// Both overrides must follow the generated WithSchema/defaults.
			schema := methods.ByName("PutVertex")
			options := []connect.HandlerOption{connect.WithSchema(schema), connect.WithIdempotency(level)}
			var generated, selected connect.Spec
			_, full := graphv1connect.NewLanternServiceHandler(svc, append(options, connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
				if spec.Procedure == procedure {
					generated = spec
				}
				return nil
			}))...)
			one := newCurrentDataRPCHandler(procedure, svc, append(options, connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
				selected = spec
				return nil
			}))...)
			if selected != generated || selected.Schema != schema || selected.IdempotencyLevel != level {
				t.Fatal("caller option precedence changed", selected, generated)
			}
			for _, request := range []struct {
				name, method, path, contentType string
				body                            []byte
			}{
				{"connect-proto", http.MethodPost, procedure, "application/proto", nil},
				{"connect-json", http.MethodPost, procedure, "application/json", []byte("{}")},
				{"grpc", http.MethodPost, procedure, "application/grpc+proto", []byte{0, 0, 0, 0, 0}},
				{"grpc-web", http.MethodPost, procedure, "application/grpc-web+proto", []byte{0, 0, 0, 0, 0}},
				{"get", http.MethodGet, procedure + "?connect=v1&encoding=json&message=%7B%7D", "", nil},
				{"verb", http.MethodPut, procedure, "application/proto", nil},
				{"media", http.MethodPost, procedure, "text/plain", nil},
				{"malformed", http.MethodPost, procedure, "application/json", []byte("{")},
			} {
				t.Run(request.name, func(t *testing.T) {
					currentDataRPCResponsesEqual(t,
						currentDataRPCResponse(full, request.method, request.path, request.contentType, request.body),
						currentDataRPCResponse(one, request.method, request.path, request.contentType, request.body))
				})
			}
		})
	}
	t.Run("conditional read cap and strict JSON", func(t *testing.T) {
		options := []connect.HandlerOption{rpcwire.StrictJSONHandlerOption(), connect.WithReadMaxBytes(1024), connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
			if spec.Procedure == procedure {
				return []connect.HandlerOption{connect.WithReadMaxBytes(16)}
			}
			return nil
		})}
		_, full := graphv1connect.NewLanternServiceHandler(svc, options...)
		one := newCurrentDataRPCHandler(procedure, svc, options...)
		for _, body := range []string{strings.Repeat(" ", 32) + "{}", `{"unknown":true}`} {
			want := currentDataRPCResponse(full, http.MethodPost, procedure, "application/json", []byte(body))
			got := currentDataRPCResponse(one, http.MethodPost, procedure, "application/json", []byte(body))
			currentDataRPCResponsesEqual(t, want, got)
			if got.Code == http.StatusOK || strings.Contains(got.Body.String(), "is not implemented") {
				t.Fatal("caller rejection did not precede typed dispatch", got.Code, got.Body.String())
			}
		}
	})
}

func TestCurrentDataRPCHandlerFreshAndUnknown(t *testing.T) {
	svc := graphv1connect.UnimplementedLanternServiceHandler{}
	calls := 0
	observe := connect.WithConditionalHandlerOptions(func(connect.Spec) []connect.HandlerOption {
		calls++
		return nil
	})
	first := newCurrentDataRPCHandler(graphv1connect.LanternServicePutVertexProcedure, svc, observe)
	second := newCurrentDataRPCHandler(graphv1connect.LanternServicePutVertexProcedure, svc, observe)
	if first == second || calls != 2 {
		t.Fatal("selected requests shared a handler or constructed unused handlers", calls)
	}
	_, full := graphv1connect.NewLanternServiceHandler(svc)
	for _, path := range []string{"", "/", "/graph.v1.LanternService/Unknown", graphv1connect.LanternServicePutVertexProcedure + "/", "/graph.v1.LanternService/putVertex", "/browser" + graphv1connect.LanternServicePutVertexProcedure, graphv1connect.LanternSecurityServiceGetCurrentPrincipalProcedure} {
		handler := newCurrentDataRPCHandler(path, svc, observe)
		if calls != 2 {
			t.Fatal("unknown procedure constructed a Connect handler", path, calls)
		}
		want := currentDataRPCResponse(full, http.MethodPost, path, "application/proto", nil)
		got := currentDataRPCResponse(handler, http.MethodPost, path, "application/proto", nil)
		currentDataRPCResponsesEqual(t, want, got)
		if got.Code != http.StatusNotFound {
			t.Fatal("unknown route changed", path, got.Code)
		}
	}
}

func currentDataRPCResponse(handler http.Handler, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
	target := path
	if target == "" {
		target = "/"
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if path == "" {
		request.URL.Path = ""
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Connect-Protocol-Version", "1")
	request.ProtoMajor, request.ProtoMinor = 2, 0
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func currentDataRPCResponsesEqual(t *testing.T, expected, actual *httptest.ResponseRecorder) {
	t.Helper()
	equalValues := func(a, b []string) bool { return slices.Equal(a, b) }
	if expected.Code != actual.Code || !bytes.Equal(expected.Body.Bytes(), actual.Body.Bytes()) || !maps.EqualFunc(expected.Header(), actual.Header(), equalValues) || !maps.EqualFunc(expected.Result().Trailer, actual.Result().Trailer, equalValues) {
		t.Fatalf("response differs: generated=(%d, %v, %q), selected=(%d, %v, %q)", expected.Code, expected.Header(), expected.Body.String(), actual.Code, actual.Header(), actual.Body.String())
	}
}

var currentDataRPCBenchmarkHandler http.Handler

// This measures construction only: no requests or used pools, transport,
// authority, receipt retention or marked-live heap. The testing.B harness's
// normal GC remains; this benchmark adds no manual GC or runtime settings.
func BenchmarkCurrentDataRPCHandlerConstruction(b *testing.B) {
	svc := graphv1connect.UnimplementedLanternServiceHandler{}
	options := []connect.HandlerOption{rpcwire.StrictJSONHandlerOption(), connect.WithReadMaxBytes(4 << 20), connect.WithSendMaxBytes(4 << 20), connect.WithCompressMinBytes(1024)}
	b.Run("generated-all", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, currentDataRPCBenchmarkHandler = graphv1connect.NewLanternServiceHandler(svc, options...)
		}
	})
	for _, procedure := range []string{graphv1connect.LanternServicePutVertexProcedure, graphv1connect.LanternServiceGetReceiptStatusProcedure, graphv1connect.LanternServiceBackupSnapshotProcedure} {
		b.Run(strings.TrimPrefix(procedure, "/"+graphv1connect.LanternServiceName+"/"), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				currentDataRPCBenchmarkHandler = newCurrentDataRPCHandler(procedure, svc, options...)
			}
		})
	}
}
