package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProductJSONRoundTrip(t *testing.T) {
	want := Product{
		Name:          "MacBook Pro M2 Pro 32GB/512GB",
		URL:           "https://fptshop.com.vn/may-doi-tra/macbook-pro-m2-pro",
		Price:         45000000,
		OriginalPrice: 52000000,
		DiscountPct:   13.46,
		InStock:       true,
		FetchedAt:     time.Date(2026, 7, 23, 10, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Product
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, want.FetchedAt)
	}
	got.FetchedAt = want.FetchedAt // normalize for equality check below

	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	want := Snapshot{
		Price:       45000000,
		DiscountPct: 13.46,
		InStock:     true,
		ObservedAt:  time.Date(2026, 7, 23, 10, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Snapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !got.ObservedAt.Equal(want.ObservedAt) {
		t.Errorf("ObservedAt = %v, want %v", got.ObservedAt, want.ObservedAt)
	}
	got.ObservedAt = want.ObservedAt

	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestCandidateJSONRoundTrip(t *testing.T) {
	want := Candidate{
		Product: Product{
			Name:          "MacBook Pro M2 Pro 32GB/512GB",
			URL:           "https://fptshop.com.vn/may-doi-tra/macbook-pro-m2-pro",
			Price:         45000000,
			OriginalPrice: 52000000,
			DiscountPct:   13.46,
			InStock:       true,
			FetchedAt:     time.Date(2026, 7, 23, 10, 30, 0, 0, time.UTC),
		},
		Score:         0.83,
		MatchedTokens: []string{"macbook", "pro", "m2"},
		MissingTokens: []string{"32gb", "512gb"},
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Candidate
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !got.Product.FetchedAt.Equal(want.Product.FetchedAt) {
		t.Errorf("Product.FetchedAt = %v, want %v", got.Product.FetchedAt, want.Product.FetchedAt)
	}
	got.Product.FetchedAt = want.Product.FetchedAt

	if got.Product != want.Product || got.Score != want.Score {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(got.MatchedTokens) != len(want.MatchedTokens) {
		t.Errorf("MatchedTokens = %v, want %v", got.MatchedTokens, want.MatchedTokens)
	}
	for i := range want.MatchedTokens {
		if got.MatchedTokens[i] != want.MatchedTokens[i] {
			t.Errorf("MatchedTokens[%d] = %q, want %q", i, got.MatchedTokens[i], want.MatchedTokens[i])
		}
	}
	if len(got.MissingTokens) != len(want.MissingTokens) {
		t.Errorf("MissingTokens = %v, want %v", got.MissingTokens, want.MissingTokens)
	}
	for i := range want.MissingTokens {
		if got.MissingTokens[i] != want.MissingTokens[i] {
			t.Errorf("MissingTokens[%d] = %q, want %q", i, got.MissingTokens[i], want.MissingTokens[i])
		}
	}
}

func TestTargetJSONRoundTrip(t *testing.T) {
	want := Target{
		Name: "MacBook Pro M2 Pro 32GB/512GB",
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Target
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
