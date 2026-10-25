package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// Only one original workload's material is copied to its mount. Operator
// signing keys and other voters/origins never enter this file set.
func copyComposeAuthority(directory, mount string, index int) error {
	name := fmt.Sprintf("node-%d", index+1)
	source := filepath.Join(directory, "identity", "nodes", name)
	destination := filepath.Join(mount, "identity", "nodes", name)
	if err := os.MkdirAll(filepath.Join(destination, "membership"), 0700); err != nil {
		return err
	}
	for _, name := range []string{"identity.json", "COMPLETE", "workload.pem", "workload-key.pem", "roots.pem", "data-operator.pem", "membership/control.json", "membership/data.json"} {
		if err := copyMountedFixtureFile(filepath.Join(source, name), filepath.Join(destination, name)); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(destination, name), 0600); err != nil {
			return err
		}
	}
	return copyMountedFixtureFile(filepath.Join(directory, "authority", name, "startup.json"), filepath.Join(mount, "startup.json"))
}

func renewComposeAuthority(directory string, now time.Time) error {
	root := filepath.Join(directory, "authority")
	keyInfo, err := os.Lstat(filepath.Join(root, "control-operator.key"))
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm() != 0600 {
		return errors.New("private original control operator key required")
	}
	raw, err := os.ReadFile(filepath.Join(root, "control-operator.key"))
	if err != nil {
		return err
	}
	block, rest := pem.Decode(raw)
	if block == nil || len(rest) != 0 {
		return errors.New("invalid control operator key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(ed25519.PrivateKey)
	if err != nil || !ok {
		return errors.New("invalid control operator key")
	}
	path := filepath.Join(root, "node-1", "membership.signed")
	raw, err = os.ReadFile(path)
	if err != nil || len(raw) > peerauth.MaxManifestBytes {
		return errors.New("bounded original control membership required")
	}
	var envelope struct {
		Payload peerauth.ControlManifest `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("invalid control membership")
	}
	manifest, err := peerauth.VerifyControlManifest(raw, key.Public().(ed25519.PublicKey), envelope.Payload.Profile.Lineage)
	if err != nil || len(manifest.Profile.Voters) != 3 || now.Before(manifest.IssuedAt) || !now.Add(peerauth.ClockMargin).Before(manifest.ExpiresAt) {
		return errors.New("control membership is not current")
	}
	var targets []string
	for i := range 3 {
		name := fmt.Sprintf("node-%d", i+1)
		targets = append(targets, filepath.Join(root, name, "membership.signed"), filepath.Join(directory, "identity", "nodes", name, "membership", "control.json"), filepath.Join(directory, fmt.Sprintf("lantern-%d", i), "identity", "nodes", name, "membership", "control.json"))
	}
	for _, target := range targets {
		current, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(current, raw) {
			return errors.New("original control membership drift")
		}
	}
	manifest.Version++
	manifest.IssuedAt, manifest.ExpiresAt = now, now.Add(peerauth.MaxMembershipLifetime)
	next, err := peerauth.SignControlManifest(manifest, key)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := replaceFixtureFile(target, next, 0600); err != nil {
			return err
		}
	}
	return nil
}
