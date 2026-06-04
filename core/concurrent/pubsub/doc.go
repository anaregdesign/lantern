// Package pubsub is an in-process publish/subscribe primitive for the
// Lantern core. It owns message lifecycles (Publish → enqueue → consumer
// → Ack/salvage) and the FullPolicy semantics that govern channel
// saturation.
//
// # Observability
//
// Subscriptions emit telemetry through the Observer interface. core/ is
// a leaf module and never imports server/metrics (see AGENTS.md
// "Dependency boundaries"); the server-side Prometheus collectors are
// plugged in per subscription via WithObserver(...). The default is a
// no-op so library consumers pay zero overhead.
//
// Authors adding new drop or dispatch paths must invoke the matching
// Observer method exactly once per event and, when a new drop policy is
// introduced, extend the DropPolicies slice so server-side label
// pre-warming stays exhaustive.
package pubsub
