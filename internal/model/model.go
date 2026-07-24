package model

import "time"

// Product is one parsed listing card from the source site.
type Product struct {
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	Price         int       `json:"price"`
	OriginalPrice int       `json:"original_price"`
	DiscountPct   float64   `json:"discount_pct"`
	InStock       bool      `json:"in_stock"`
	FetchedAt     time.Time `json:"fetched_at"`
}

// Snapshot is one point-in-time price/stock observation recorded into the
// append-only price-history log (keyed externally by product URL).
type Snapshot struct {
	Price       int       `json:"price"`
	DiscountPct float64   `json:"discount_pct"`
	InStock     bool      `json:"in_stock"`
	ObservedAt  time.Time `json:"observed_at"`
}

// Candidate is prefilter's output: a product paired with its token-overlap
// match score against a Target.
type Candidate struct {
	Product       Product  `json:"product"`
	Score         float64  `json:"score"`
	MatchedTokens []string `json:"matched_tokens"`
	MissingTokens []string `json:"missing_tokens"`
}

// Target is a configured target device spec from config.json, used for
// prefilter token matching and judgment.
type Target struct {
	Name string `json:"name"`
}
