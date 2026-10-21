package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"connectrpc.com/connect"
	graphv1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

const (
	edgeSetupBatchSize = 128
	edgeSetupMaxBytes  = 64 << 10
	edgeSetupValue     = "receipt-edge-add-fixture"
	edgeSetupHead      = "bench:receipt:add"
)

type edgeSetupClient interface {
	PutVertices(context.Context, *connect.Request[graphv1.PutVerticesRequest]) (*connect.Response[graphv1.PutVerticesResponse], error)
	GetVertices(context.Context, *connect.Request[graphv1.GetVerticesRequest]) (*connect.Response[graphv1.GetVerticesResponse], error)
}

type edgeSetupPhase struct {
	Phase    string
	Duration time.Duration
	PairRPS  int
}

type edgeSetupManifestPhase struct {
	Phase      string `json:"phase"`
	DurationNS int64  `json:"duration_ns"`
	PairRPS    int    `json:"pair_rps"`
	MaxOps     uint64 `json:"max_ops"`
	TailCount  uint64 `json:"tail_count"`
	NonceHex   string `json:"nonce_hex"`
}

// This is a data/key plan, never an authority or a cached admission decision.
// Loading it checks fresh capabilities and the live vertices again.
type edgeSetupManifest struct {
	Version    int                      `json:"version"`
	Family     receiptFamily            `json:"family"`
	Binding    string                   `json:"endpoint_policy_sha256"`
	MaxEntries uint64                   `json:"max_entries"`
	Phases     []edgeSetupManifestPhase `json:"phases"`
}

func edgeAddPhaseLimit(duration time.Duration, pairRPS int) (uint64, error) {
	if duration <= 0 || pairRPS <= 0 || int64(pairRPS) > int64(time.Second) {
		return 0, errors.New("edge fixture duration and pair RPS must define a positive ticker interval")
	}
	interval := time.Second / time.Duration(pairRPS)
	return uint64(duration/interval) + 1, nil
}

func edgeSetupNonce(phase edgeSetupManifestPhase) ([16]byte, error) {
	var nonce [16]byte
	decoded, err := hex.DecodeString(phase.NonceHex)
	if err != nil || len(decoded) != len(nonce) || hex.EncodeToString(decoded) != phase.NonceHex {
		return nonce, errors.New("edge fixture nonce must be a canonical 16-byte hex value")
	}
	copy(nonce[:], decoded)
	if nonce == [16]byte{} {
		return nonce, errors.New("edge fixture nonce must be nonzero")
	}
	return nonce, nil
}

func validateEdgeSetupManifest(manifest *edgeSetupManifest) error {
	if manifest == nil || manifest.Version != 1 || manifest.Family != receiptEdgeAdd ||
		manifest.MaxEntries == 0 || len(manifest.Phases) != 2 {
		return errors.New("invalid edge fixture manifest header")
	}
	binding, err := hex.DecodeString(manifest.Binding)
	if err != nil || len(binding) != sha256.Size || hex.EncodeToString(binding) != manifest.Binding {
		return errors.New("invalid edge fixture endpoint/policy binding")
	}
	var total uint64
	var firstNonce [16]byte
	for i, name := range []string{"warmup", "steady"} {
		phase := manifest.Phases[i]
		limit, err := edgeAddPhaseLimit(time.Duration(phase.DurationNS), phase.PairRPS)
		if err != nil || phase.Phase != name || phase.MaxOps != limit || phase.TailCount != (limit+1)/2 {
			return errors.New("edge fixture phase/count binding is invalid")
		}
		if limit > manifest.MaxEntries-total {
			return errors.New("edge fixture operation budget exceeds receipt policy max entries")
		}
		total += limit
		nonce, err := edgeSetupNonce(phase)
		if err != nil {
			return err
		}
		if i == 0 {
			firstNonce = nonce
		} else if bytes.Equal(firstNonce[:8], nonce[:8]) {
			return errors.New("edge fixture phases must have distinct nonce prefixes")
		}
	}
	return nil
}

func edgeSetupBinding(endpoints []receiptEndpoint) (string, uint64, error) {
	if len(endpoints) == 0 || len(endpoints) > receiptReplicaCount {
		return "", 0, errors.New("edge fixture requires one to three configured receipt replicas")
	}
	if err := validateEndpointSet(endpoints, receiptEdgeAdd); err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	var length [8]byte
	write := func(value []byte) {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(value)
	}
	for _, endpoint := range endpoints {
		if _, ok := endpoint.client.(edgeSetupClient); !ok {
			return "", 0, errors.New("edge fixture client does not implement ordinary plural vertex RPCs")
		}
		policy, owner := endpoint.capability.GetPolicy(), endpoint.capability.GetEndpoint()
		if endpoint.address == "" || len(policy.GetDeploymentEpoch()) != 16 ||
			len(policy.GetFingerprint()) != sha256.Size || policy.GetRetentionMs() == 0 ||
			policy.GetMaxEntries() == 0 || policy.GetMaxBytes() == 0 ||
			len(owner.GetNodeId()) != 16 || len(owner.GetGeneration()) == 0 {
			return "", 0, errors.New("edge fixture requires complete endpoint and receipt policy bindings")
		}
		write([]byte(endpoint.address))
		write(policy.GetDeploymentEpoch())
		write(policy.GetFingerprint())
		for _, value := range []uint64{policy.GetRetentionMs(), policy.GetMaxEntries(), policy.GetMaxBytes()} {
			var encoded [8]byte
			binary.BigEndian.PutUint64(encoded[:], value)
			write(encoded[:])
		}
		write(owner.GetNodeId())
		write(owner.GetGeneration())
	}
	return hex.EncodeToString(hash.Sum(nil)), endpoints[0].capability.GetPolicy().GetMaxEntries(), nil
}

func edgeSetupVertex(key string) *graphv1.Vertex {
	return &graphv1.Vertex{Key: key, Value: &graphv1.Vertex_String_{String_: edgeSetupValue}}
}

func walkEdgeSetupBatches(manifest *edgeSetupManifest, visit func([]string) error) error {
	keys := make([]string, 0, edgeSetupBatchSize)
	emit := func(key string) error {
		keys = append(keys, key)
		if len(keys) != edgeSetupBatchSize {
			return nil
		}
		if err := visit(keys); err != nil {
			return err
		}
		keys = make([]string, 0, edgeSetupBatchSize)
		return nil
	}
	if err := emit(edgeSetupHead); err != nil {
		return err
	}
	for _, phase := range manifest.Phases {
		nonce, err := edgeSetupNonce(phase)
		if err != nil {
			return err
		}
		for index := uint64(0); index < phase.TailCount; index++ {
			if err := emit(fmt.Sprintf("bench:receipt:%x:%s:%d", nonce[:8], phase.Phase, index)); err != nil {
				return err
			}
		}
	}
	if len(keys) != 0 {
		return visit(keys)
	}
	return nil
}

func prepareEdgeAdd(ctx context.Context, endpoints []receiptEndpoint, token string, requestTimeout time.Duration, phases []edgeSetupPhase) (*edgeSetupManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return nil, errors.New("edge fixture preparation requires a bounded setup context")
	}
	if token == "" || requestTimeout <= 0 || len(phases) != 2 {
		return nil, errors.New("edge fixture requires auth, a positive RPC deadline, and both phases")
	}
	binding, maxEntries, err := edgeSetupBinding(endpoints)
	if err != nil {
		return nil, err
	}
	manifest := &edgeSetupManifest{Version: 1, Family: receiptEdgeAdd, Binding: binding, MaxEntries: maxEntries}
	for _, phase := range phases {
		limit, err := edgeAddPhaseLimit(phase.Duration, phase.PairRPS)
		if err != nil {
			return nil, err
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, fmt.Errorf("edge fixture nonce: %w", err)
		}
		nonce[0] |= 1
		manifest.Phases = append(manifest.Phases, edgeSetupManifestPhase{
			Phase: phase.Phase, DurationNS: int64(phase.Duration), PairRPS: phase.PairRPS,
			MaxOps: limit, TailCount: (limit + 1) / 2, NonceHex: hex.EncodeToString(nonce[:]),
		})
	}
	if err := validateEdgeSetupManifest(manifest); err != nil {
		return nil, err
	}
	origin := endpoints[0].client.(edgeSetupClient)
	err = walkEdgeSetupBatches(manifest, func(keys []string) error {
		vertices := make([]*graphv1.Vertex, len(keys))
		for i, key := range keys {
			vertices[i] = edgeSetupVertex(key)
		}
		rpcCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		response, err := origin.PutVertices(rpcCtx, authorizedRequest(&graphv1.PutVerticesRequest{Vertices: vertices, IfAbsent: true}, token))
		if err == nil {
			err = rpcCtx.Err()
		}
		cancel()
		if err != nil {
			return fmt.Errorf("seed edge fixture vertices: %w", err)
		}
		if response == nil || response.Msg == nil || len(response.Msg.GetOutcomes()) != len(vertices) {
			return errors.New("edge fixture PutVertices response length is invalid")
		}
		for _, outcome := range response.Msg.GetOutcomes() {
			if outcome != graphv1.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
				return errors.New("edge fixture PutVertices did not return APPLIED_AND_LIVE")
			}
		}
		return nil
	})
	if err == nil {
		err = verifyEdgeSetup(ctx, endpoints, token, requestTimeout, manifest)
	}
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

func verifyEdgeSetup(ctx context.Context, endpoints []receiptEndpoint, token string, requestTimeout time.Duration, manifest *edgeSetupManifest) error {
	return walkEdgeSetupBatches(manifest, func(keys []string) error {
		for _, endpoint := range endpoints {
			pending := keys
			for len(pending) != 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
				rpcCtx, cancel := context.WithTimeout(ctx, requestTimeout)
				response, err := endpoint.client.(edgeSetupClient).GetVertices(rpcCtx, authorizedRequest(&graphv1.GetVerticesRequest{Keys: pending}, token))
				if err == nil {
					err = rpcCtx.Err()
				}
				cancel()
				if err != nil {
					return fmt.Errorf("verify edge fixture vertices: %w", err)
				}
				pending, err = edgeSetupMissing(pending, response)
				if err != nil {
					return err
				}
				if len(pending) != 0 {
					timer := time.NewTimer(25 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}
				}
			}
		}
		return nil
	})
}

func edgeSetupMissing(keys []string, response *connect.Response[graphv1.GetVerticesResponse]) ([]string, error) {
	if response == nil || response.Msg == nil || len(response.Msg.GetVertices())+len(response.Msg.GetMissing()) != len(keys) {
		return nil, errors.New("edge fixture GetVertices response length is invalid")
	}
	remaining := make(map[string]bool, len(keys))
	for _, key := range keys {
		remaining[key] = true
	}
	for _, vertex := range response.Msg.GetVertices() {
		if vertex == nil || !remaining[vertex.GetKey()] || !proto.Equal(vertex, edgeSetupVertex(vertex.GetKey())) {
			return nil, errors.New("edge fixture GetVertices returned an unexpected vertex or value")
		}
		delete(remaining, vertex.GetKey())
	}
	missing := make([]string, 0, len(response.Msg.GetMissing()))
	for _, key := range response.Msg.GetMissing() {
		if !remaining[key] {
			return nil, errors.New("edge fixture GetVertices returned an unexpected or duplicate missing key")
		}
		delete(remaining, key)
		missing = append(missing, key)
	}
	if len(remaining) != 0 {
		return nil, errors.New("edge fixture GetVertices omitted requested keys")
	}
	return missing, nil
}

func writeEdgeSetupManifest(path string, manifest *edgeSetupManifest) error {
	if err := validateEdgeSetupManifest(manifest); err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil || len(data) > edgeSetupMaxBytes {
		return errors.New("edge fixture manifest exceeds its encoded bound")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func openEdgeSetupManifest(path string, expected os.FileInfo) (*os.File, error) {
	// A private regular path can still be replaced after Lstat. Nonblocking
	// open prevents a replacement FIFO from waiting for a writer before fstat.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		!os.SameFile(info, expected) || info.Size() > edgeSetupMaxBytes) {
		err = errors.New("edge fixture manifest must be the same regular private bounded file")
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func loadEdgeSetupManifest(ctx context.Context, path string, cfg probeConfig, endpoints []receiptEndpoint) ([16]byte, uint64, error) {
	var nonce [16]byte
	if err := ctx.Err(); err != nil {
		return nonce, 0, err
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return nonce, 0, errors.New("edge fixture loading requires a bounded setup context")
	}
	if cfg.family != receiptEdgeAdd || cfg.token == "" || cfg.requestTimeout <= 0 {
		return nonce, 0, errors.New("edge fixture is only valid for authenticated EdgeAdd")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nonce, 0, err
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode().Perm() != 0o600 || pathInfo.Size() > edgeSetupMaxBytes {
		return nonce, 0, errors.New("edge fixture manifest must be a regular private bounded file")
	}
	file, err := openEdgeSetupManifest(path, pathInfo)
	if err != nil {
		return nonce, 0, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, edgeSetupMaxBytes+1))
	decoder.DisallowUnknownFields()
	var manifest edgeSetupManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nonce, 0, errors.New("decode edge fixture manifest")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nonce, 0, errors.New("edge fixture manifest contains trailing data")
	}
	if err := validateEdgeSetupManifest(&manifest); err != nil {
		return nonce, 0, err
	}
	binding, maxEntries, err := edgeSetupBinding(endpoints)
	if err != nil || binding != manifest.Binding || maxEntries != manifest.MaxEntries {
		return nonce, 0, errors.New("edge fixture endpoints or receipt policy changed")
	}
	for _, phase := range manifest.Phases {
		if phase.Phase != cfg.phase {
			continue
		}
		if phase.DurationNS != int64(cfg.duration) || phase.PairRPS != cfg.pairRPS {
			return nonce, 0, errors.New("edge fixture load phase does not match its prepared duration and rate")
		}
		if err := verifyEdgeSetup(ctx, endpoints, cfg.token, cfg.requestTimeout, &manifest); err != nil {
			return nonce, 0, err
		}
		nonce, err = edgeSetupNonce(phase)
		return nonce, phase.MaxOps, err
	}
	return nonce, 0, errors.New("edge fixture load phase was not prepared")
}
