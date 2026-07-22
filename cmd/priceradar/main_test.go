package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_ValidFixture(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("testdata", "config.valid.json"))
	if err != nil {
		t.Fatalf("loadConfig returned unexpected error: %v", err)
	}

	if len(cfg.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(cfg.Targets))
	}
	if got, want := cfg.Targets[0].Name, "MacBook Pro M2 Pro 32GB/512GB"; got != want {
		t.Errorf("Targets[0].Name = %q, want %q", got, want)
	}
	if got, want := cfg.ListingURL, "https://fptshop.com.vn/may-doi-tra"; got != want {
		t.Errorf("ListingURL = %q, want %q", got, want)
	}
	if got, want := cfg.Notify.PriceDropPct, 5.0; got != want {
		t.Errorf("Notify.PriceDropPct = %v, want %v", got, want)
	}
}

func TestLoadConfig_MalformedJSON(t *testing.T) {
	_, err := loadConfig(filepath.Join("testdata", "config.malformed.json"))
	if err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "loadConfig") {
		t.Errorf("error should identify loadConfig as the source, got: %v", err)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := loadConfig(filepath.Join(os.TempDir(), "priceradar-does-not-exist.json"))
	if err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}
