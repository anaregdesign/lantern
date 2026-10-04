package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// exportComposeFixture owns the fixed local benchmark topology. Only one
// workload's material is mounted into each server; the operator key stays on
// the host. These fixtures never qualify clocks, providers or deployments.
func exportComposeFixture(result *fixture, dir string) error {
	if len(result.Nodes) != 3 {
		return errors.New("Compose requires three nodes")
	}
	for i := range result.Nodes {
		node := &result.Nodes[i]
		service := fmt.Sprintf("lantern-%d", i)
		mount := filepath.Join(dir, service)
		if err := os.Mkdir(mount, 0755); err != nil {
			return err
		}
		// Explicit mode is needed inside the host-private 0700 outer directory
		// so the container's non-root workload can traverse its own bind mount.
		if err := os.Chmod(mount, 0755); err != nil {
			return err
		}
		env := node.Environment
		env["LANTERN_PORT"] = "6380"
		env["LANTERN_PEER_LISTEN_ADDR"] = ":6381"
		env["LANTERN_PEER_MEMBERSHIP_STATE_FILE"] = "/state/membership.state"
		env["LANTERN_METRICS_ADDR"] = "127.0.0.1:9090"
		if env["LANTERN_AUTH_MODE"] == "oidc" {
			env["LANTERN_SECURITY_STORE_PATH"] = "/state/security.wal"
		} else {
			delete(env, "LANTERN_TLS_CERT_FILE")
			delete(env, "LANTERN_TLS_KEY_FILE")
			node.PublicOrigin = strings.Replace(node.PublicOrigin, "https://", "http://", 1)
		}
		if env["LANTERN_RECEIPT_WAL_MODE"] != "" {
			env["LANTERN_RECEIPT_WAL_PATH"] = "/state/receipts.wal"
		}
		for key, value := range env {
			if !strings.HasPrefix(value, dir+string(os.PathSeparator)) {
				continue
			}
			name := filepath.Base(value)
			if value != filepath.Join(dir, name) || name == "operator.key" || name == "tokens.json" {
				return errors.New("unexpected mounted fixture path")
			}
			if err := copyMountedFixtureFile(value, filepath.Join(mount, name)); err != nil {
				return err
			}
			env[key] = "/run/lantern-config/" + name
		}
		// The public certificate proof helper consumes these stable names.
		for _, pair := range [][2]string{{node.Name + "-public.pem", "server.pem"}, {node.Name + "-public.key", "server.key"}, {"ca.pem", "ca.pem"}} {
			if err := copyMountedFixtureFile(filepath.Join(dir, pair[0]), filepath.Join(mount, pair[1])); err != nil {
				return err
			}
		}
		config, err := composeEnvFile(env)
		if err != nil {
			return err
		}
		if _, err := writeFile(dir, service+".env", config); err != nil {
			return err
		}

	}
	_, err := writeJSON(dir, "fixture.json", result)
	return err
}

func copyMountedFixtureFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("invalid fixture source file")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	mode := os.FileMode(0644)
	name := filepath.Base(source)
	if strings.HasSuffix(name, ".key") || name == "machines.json" || name == "cdc-keys.json" {
		mode = 0600
	}
	if err := os.WriteFile(destination, raw, mode); err != nil {
		return err
	}
	return os.Chmod(destination, mode)
}

// renewComposeMembership signs a bounded successor of the exact current
// cohort. It refuses drift or expiry, rather than creating a new deployment,
// accepting a stale file, or silently restoring a stopped operator process.
func renewComposeMembership(dir string, now time.Time) error {
	info, err := os.Lstat(dir)
	if err != nil || !filepath.IsAbs(dir) || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("unsafe Compose fixture directory")
	}
	keyInfo, err := os.Lstat(filepath.Join(dir, "operator.key"))
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm() != 0600 {
		return errors.New("unsafe operator key")
	}
	rawKey, err := os.ReadFile(filepath.Join(dir, "operator.key"))
	if err != nil {
		return err
	}
	block, rest := pem.Decode(rawKey)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return errors.New("invalid operator key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(ed25519.PrivateKey)
	if err != nil || !ok {
		return errors.New("invalid operator key")
	}
	manifestPath := filepath.Join(dir, "membership.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil || len(raw) > peerauth.MaxManifestBytes {
		return errors.New("missing membership")
	}
	var envelope struct {
		Payload peerauth.Manifest `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("invalid membership")
	}
	manifest, err := peerauth.VerifyManifest(raw, key.Public().(ed25519.PublicKey), envelope.Payload.Domain)
	if err != nil || len(manifest.Members) != 3 || now.Before(manifest.IssuedAt) || !now.Add(peerauth.ClockMargin).Before(manifest.ExpiresAt) {
		return errors.New("membership is not current")
	}
	for i := range 3 {
		current, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("lantern-%d", i), "membership.json"))
		if err != nil || !bytes.Equal(current, raw) {
			return errors.New("mounted membership drift")
		}
	}
	manifest.Version++
	manifest.IssuedAt = now
	manifest.ExpiresAt = now.Add(peerauth.MaxMembershipLifetime)
	next, err := peerauth.SignManifest(manifest, key)
	if err != nil {
		return err
	}
	for i := range 3 {
		if err := replaceFixtureFile(filepath.Join(dir, fmt.Sprintf("lantern-%d", i), "membership.json"), next, 0644); err != nil {
			return err
		}
	}
	return replaceFixtureFile(manifestPath, next, 0600)
}

func replaceFixtureFile(path string, raw []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".membership-next-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func runComposeRenewal(ctx context.Context, dir string, interval time.Duration) error {
	if interval != 0 && (interval < 30*time.Second || interval > 2*time.Minute) {
		return errors.New("invalid renewal interval")
	}
	var count uint64
	for {
		now := time.Now().UTC()
		if err := renewComposeMembership(dir, now); err != nil {
			return err
		}
		count++
		raw, err := json.Marshal(struct {
			Renewals uint64    `json:"renewals"`
			LastAt   time.Time `json:"last_at"`
		}{count, now})
		if err != nil {
			return err
		}
		if err := replaceFixtureFile(filepath.Join(dir, "renewal.json"), raw, 0600); err != nil {
			return err
		}
		if interval == 0 {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func composeEnvFile(env map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var config strings.Builder
	for _, key := range keys {
		value := env[key]
		if !strings.HasPrefix(key, "LANTERN_") || strings.ContainsAny(key, "='\n\r\x00") || strings.ContainsAny(value, "'\n\r\x00") {
			return nil, errors.New("invalid Compose fixture env")
		}
		fmt.Fprintf(&config, "%s='%s'\n", key, value)
	}
	return []byte(config.String()), nil
}

func prepareComposeRestart(dir, service string) error {
	info, err := os.Lstat(dir)
	if err != nil || !filepath.IsAbs(dir) || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("unsafe fixture directory")
	}
	index := -1
	for i := range 3 {
		if service == fmt.Sprintf("lantern-%d", i) {
			index = i
		}
	}
	if index < 0 {
		return errors.New("unknown workload")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("missing fixture metadata")
	}
	var metadata fixture
	if json.Unmarshal(raw, &metadata) != nil || len(metadata.Nodes) != 3 || metadata.Nodes[index].Name != fmt.Sprintf("node-%d", index) {
		return errors.New("invalid fixture metadata")
	}
	env := metadata.Nodes[index].Environment
	if env["LANTERN_PEER_MEMBERSHIP_MODE"] != "fresh" {
		return errors.New("workload restart must be prepared only once")
	}
	env["LANTERN_PEER_MEMBERSHIP_MODE"] = "resume"
	if env["LANTERN_AUTH_MODE"] == "oidc" {
		env["LANTERN_SECURITY_STORE_MODE"] = "restart"
	}
	if env["LANTERN_RECEIPT_WAL_MODE"] == "fresh" {
		env["LANTERN_RECEIPT_WAL_MODE"] = "restart"
	} else {
		// A retained workload identity does not prove ephemeral application log
		// continuity. Give graph-only restart a new origin even with durable sys.
		nodeID, err := randomID()
		if err != nil {
			return err
		}
		env["LANTERN_NODE_ID"] = hex.EncodeToString(nodeID[:])
	}
	config, err := composeEnvFile(env)
	if err != nil {
		return err
	}
	if err := replaceFixtureFile(filepath.Join(dir, service+".env"), config, 0600); err != nil {
		return err
	}
	updated, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return replaceFixtureFile(filepath.Join(dir, "fixture.json"), updated, 0600)
}
