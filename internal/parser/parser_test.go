package parser

import (
	"os"
	"testing"
)

func loadFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/listing.html")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	return string(data)
}

// TestParse_WellFormedCardCount asserts the parser returns exactly the
// well-formed cards from the fixture (7 total cards, 2 deliberately
// malformed -> 5 well-formed products), and that it does not panic on the
// malformed cards.
func TestParse_WellFormedCardCount(t *testing.T) {
	html := loadFixture(t)

	got := Parse(html)
	const wantCount = 5
	if len(got) != wantCount {
		t.Fatalf("Parse() returned %d products, want %d; got=%+v", len(got), wantCount, got)
	}
}

// TestParse_MalformedCardsSkipped asserts the two deliberately broken cards
// (missing price / empty name) never appear in the output.
func TestParse_MalformedCardsSkipped(t *testing.T) {
	html := loadFixture(t)
	got := Parse(html)

	for _, p := range got {
		if p.Name == "" {
			t.Errorf("found product with empty name in output: %+v", p)
		}
		if p.URL == "https://fptshop.com.vn/may-doi-tra/broken-card" {
			t.Errorf("malformed 'broken-card' (missing price) should have been skipped, got: %+v", p)
		}
		if p.URL == "https://fptshop.com.vn/may-doi-tra/nameless-card" {
			t.Errorf("malformed 'nameless-card' (empty name) should have been skipped, got: %+v", p)
		}
	}
}

// TestParse_GoldenCard checks every field of card 1 (in-stock, discounted)
// against known-good values from the fixture.
func TestParse_GoldenCard(t *testing.T) {
	html := loadFixture(t)
	got := Parse(html)

	var golden *productResult
	for i := range got {
		if got[i].URL == "https://fptshop.com.vn/may-doi-tra/macbook-pro-m2-pro-32gb-512gb" {
			golden = &productResult{got[i].Name, got[i].URL, got[i].Price, got[i].OriginalPrice, got[i].DiscountPct, got[i].InStock}
			break
		}
	}
	if golden == nil {
		t.Fatalf("golden card not found in Parse() output: %+v", got)
	}

	want := productResult{
		Name:          "MacBook Pro M2 Pro 32GB/512GB",
		URL:           "https://fptshop.com.vn/may-doi-tra/macbook-pro-m2-pro-32gb-512gb",
		Price:         45990000,
		OriginalPrice: 52990000,
		DiscountPct:   13,
		InStock:       true,
	}
	if *golden != want {
		t.Errorf("golden card mismatch:\n got  = %+v\n want = %+v", *golden, want)
	}
}

type productResult struct {
	Name          string
	URL           string
	Price         int
	OriginalPrice int
	DiscountPct   float64
	InStock       bool
}

// TestParse_OutOfStockAndNonDiscounted covers the remaining fixture
// variety: out-of-stock cards and non-discounted cards.
func TestParse_OutOfStockAndNonDiscounted(t *testing.T) {
	html := loadFixture(t)
	got := Parse(html)

	byURL := map[string]int{}
	for i, p := range got {
		byURL[p.URL] = i
	}

	// Card 2: in-stock, no discount badge -> OriginalPrice falls back to
	// Price, DiscountPct 0.
	if i, ok := byURL["https://fptshop.com.vn/may-doi-tra/ipad-air-5-64gb"]; ok {
		p := got[i]
		if !p.InStock {
			t.Errorf("ipad-air-5 expected in-stock, got InStock=%v", p.InStock)
		}
		if p.DiscountPct != 0 {
			t.Errorf("ipad-air-5 expected DiscountPct=0, got %v", p.DiscountPct)
		}
		if p.OriginalPrice != p.Price {
			t.Errorf("ipad-air-5 expected OriginalPrice == Price, got %d != %d", p.OriginalPrice, p.Price)
		}
	} else {
		t.Errorf("ipad-air-5 not found in output")
	}

	// Card 3: out-of-stock, discounted.
	if i, ok := byURL["https://fptshop.com.vn/may-doi-tra/macbook-air-m1-8gb-256gb"]; ok {
		p := got[i]
		if p.InStock {
			t.Errorf("macbook-air-m1 expected out-of-stock, got InStock=%v", p.InStock)
		}
		if p.DiscountPct != 14 {
			t.Errorf("macbook-air-m1 expected DiscountPct=14, got %v", p.DiscountPct)
		}
	} else {
		t.Errorf("macbook-air-m1 not found in output")
	}
}

// TestParse_EmptyInput asserts an empty/garbage document parses to nil/empty
// without panicking.
func TestParse_EmptyInput(t *testing.T) {
	if got := Parse(""); len(got) != 0 {
		t.Errorf("Parse(\"\") = %+v, want empty", got)
	}
	if got := Parse("<html><body>no cards here</body></html>"); len(got) != 0 {
		t.Errorf("Parse(no cards) = %+v, want empty", got)
	}
}
