package main

import (
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/server/provider"
)

// A separate action keeps first creation out of ordinary startup. No process
// environment mutation selects fresh mode, and an ordinary start always
// resumes the environment JSON authority's existing CLEAN state.
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
