package service

import (
	"context"
	"crypto/sha256"

	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// Bind the exact logical request/shape and the service's already checked view.
// Mapping, queries and business writes keep their ordinary implementations.
// Output never re-evaluates an old result under a newer policy projection.
func bindCurrentDataOutput(ctx context.Context, a *security.Admission, request proto.Message, resources []security.PublicOutputResource) error {
	if a == nil {
		return nil
	}
	if _, current := a.CurrentProfile(); !current {
		return nil
	}
	digest, err := currentDataRequestDigest(request)
	if err != nil {
		return err
	}
	if len(resources) == 0 {
		resources = []security.PublicOutputResource{{Kind: security.OutputEmpty}}
	}
	return a.BindCurrentResourceOutput(ctx, digest, resources)
}

func currentDataRequestDigest(request proto.Message) ([32]byte, error) {
	if request == nil || !request.ProtoReflect().IsValid() || proto.Size(request) > 64<<20 {
		return [32]byte{}, dataPermissionError()
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
	if err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("lantern/current-output-request/v2\x00" + request.ProtoReflect().Descriptor().FullName() + "\x00"))
	_, _ = h.Write(raw)
	return [32]byte(h.Sum(nil)), nil
}
