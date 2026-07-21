// Command priceradar is the one-shot CLI entrypoint for PriceRadar,
// invoked by an OS-level scheduler (Windows Task Scheduler / cron).
//
// It is not yet wired up to the pipeline (fetch/parse/prefilter/store) —
// that lands in later epics. This file currently only owns config.json's
// schema and loader, per the "straight-line main()" shape documented in
// docs/03-system-architecture.md.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Target describes the specific device to look for in the FPT Shop
// listing, e.g. "MacBook Pro M2 Pro 32GB/512GB".
type Target struct {
	// Name is the device name/model used to match against listing items.
	Name string `json:"name"`
}

// NotifyThresholds controls when a price observation is worth flagging.
type NotifyThresholds struct {
	// PriceDropPct is the minimum percentage drop (vs. the last recorded
	// price) that counts as "meaningful" and worth notifying about.
	PriceDropPct float64 `json:"price_drop_pct"`
}

// Config is the top-level schema for config.json: the target device
// spec(s), the listing URL to fetch, and notify thresholds.
type Config struct {
	// Targets is the list of device specs to track.
	Targets []Target `json:"targets"`

	// ListingURL is the clean FPT Shop listing URL to fetch — never a
	// filtered/query-param URL disallowed by robots.txt.
	ListingURL string `json:"listing_url"`

	// Notify holds the thresholds that decide whether an observation is
	// worth flagging.
	Notify NotifyThresholds `json:"notify"`
}

// loadConfig reads and unmarshals a Config from the given path, returning
// a clear, wrapped error on malformed JSON or a missing/unreadable file.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loadConfig: reading %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("loadConfig: parsing %s: %w", path, err)
	}

	return &cfg, nil
}

func main() {}
