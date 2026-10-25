package main

import (
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/server/provider"
)

// Initialization runs on the actual target after explicit trust provisioning.
// Normal startup only resumes the selected node's intact CLEAN custody.
func initializeSecurity(ctx context.Context) error {
	config, err := provider.LoadSecurityConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := provider.InitializeSecurity(ctx, config); err != nil {
		return fmt.Errorf("initialize security: %w", err)
	}
	return nil
}
