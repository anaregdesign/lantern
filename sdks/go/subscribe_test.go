package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type subscribeTestHandler struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	request   *pb.SubscribeRequest
	responses []*pb.SubscribeResponse
}

func (h *subscribeTestHandler) Subscribe(
	_ context.Context,
	req *connect.Request[pb.SubscribeRequest],
	stream *connect.ServerStream[pb.SubscribeResponse],
) error {
	h.request = proto.Clone(req.Msg).(*pb.SubscribeRequest)
	for _, response := range h.responses {
		if err := stream.Send(response); err != nil {
			return err
		}
	}
	return nil
}

func newSubscribeTestClient(t *testing.T, handler *subscribeTestHandler) *Lantern {
	t.Helper()
	mux := http.NewServeMux()
	path, connectHandler := graphv1connect.NewLanternReplicationServiceHandler(handler)
	mux.Handle(path, connectHandler)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := NewLantern(server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestChangeOrigin(t *testing.T) {
	wire := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	origin, err := ChangeOriginFromBytes(wire)
	if err != nil {
		t.Fatalf("ChangeOriginFromBytes: %v", err)
	}
	const encoded = "000102030405060708090a0b0c0d0e0f"
	if got := origin.String(); got != encoded {
		t.Fatalf("String() = %q, want %q", got, encoded)
	}

	parsed, err := ParseChangeOrigin(encoded)
	if err != nil {
		t.Fatalf("ParseChangeOrigin: %v", err)
	}
	if parsed != origin {
		t.Fatalf("ParseChangeOrigin() = %v, want %v", parsed, origin)
	}

	wire[0] = 0xff
	if origin[0] != 0x00 {
		t.Fatal("ChangeOriginFromBytes must defensively copy its input")
	}
	if _, err := ChangeOriginFromBytes([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidChangeOrigin) {
		t.Fatalf("short origin: want ErrInvalidChangeOrigin, got %v", err)
	}
	if _, err := ParseChangeOrigin("not-hex"); !errors.Is(err, ErrInvalidChangeOrigin) {
		t.Fatalf("invalid hex origin: want ErrInvalidChangeOrigin, got %v", err)
	}
}

func TestSubscribe(t *testing.T) {
	origin := ChangeOrigin{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	want := []*pb.Mutation{
		{Seq: 42, Origin: origin[:]},
		{Seq: 43, Origin: origin[:]},
	}
	handler := &subscribeTestHandler{responses: []*pb.SubscribeResponse{
		{Mutation: want[0]},
		{Mutation: want[1]},
	}}
	client := newSubscribeTestClient(t, handler)

	var got []*ChangeEvent
	for event, err := range client.Subscribe(context.Background(), ChangeCursor{origin: 42}) {
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		got = append(got, event)
	}

	if handler.request == nil {
		t.Fatal("Subscribe request was not captured")
	}
	if seq := handler.request.GetFromSeqPerOrigin()[origin.String()]; seq != 42 {
		t.Fatalf("cursor sequence = %d, want 42", seq)
	}
	if len(got) != len(want) {
		t.Fatalf("received %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("event %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSubscribeRejectsMissingMutation(t *testing.T) {
	handler := &subscribeTestHandler{responses: []*pb.SubscribeResponse{{}}}
	client := newSubscribeTestClient(t, handler)

	var gotErr error
	for _, err := range client.Subscribe(context.Background(), nil) {
		gotErr = err
	}
	if !errors.Is(gotErr, ErrInvalidChangeEvent) {
		t.Fatalf("missing mutation: want ErrInvalidChangeEvent, got %v", gotErr)
	}
}
