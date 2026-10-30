package main

// Shared passive fixture for candidate policy and same-scrape collector tests.
func goroutineTestPassive() string {
	return "lantern_subscribe_active_streams 2\nlantern_changes_active_streams 0\nlantern_anti_entropy_cycles_total 1\nlantern_peer_connected{peer=\"https://peer-a:6381\"} 1\nlantern_peer_connected{peer=\"https://peer-b:6381\"} 1\n"
}
