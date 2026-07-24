package parser

import (
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"priceradar/internal/model"
)

// cardStartRe locates the opening tag of each product card. Cards are
// sliced from one match's start to the next match's start (or end of
// string for the last card), rather than trying to regex-match balanced
// nested divs — RE2 (Go's regexp engine) has no lookahead/backreferences,
// so "find each card's start, slice to the next start" is the reliable
// fault-isolated approach: a broken/unclosed tag inside one card can't
// corrupt the boundaries used to extract any other card.
var cardStartRe = regexp.MustCompile(`<div class="product-card"[^>]*>`)

var (
	nameRe          = regexp.MustCompile(`<h3 class="product-name">([^<]*)</h3>`)
	urlRe           = regexp.MustCompile(`<a class="product-link" href="([^"]*)"`)
	priceCurrentRe  = regexp.MustCompile(`<span class="price-current">([^<]*)</span>`)
	priceOriginalRe = regexp.MustCompile(`<span class="price-original">([^<]*)</span>`)
	discountRe      = regexp.MustCompile(`<span class="discount-badge">-?([\d.]+)%</span>`)
	stockRe         = regexp.MustCompile(`<div class="stock-status ([\w-]+)">`)

	// nonDigits strips currency symbols/thousands separators (e.g.
	// "45.990.000₫") down to a plain digit string for strconv.Atoi.
	nonDigits = regexp.MustCompile(`[^\d]`)
)

// Parse extracts []model.Product from FPT Shop listing HTML using regexp
// (stdlib) only. Parsing is per-card and fault-isolated: a malformed card
// (missing/unparseable price, missing tag, empty name) is logged and
// skipped, it never aborts or panics the rest of the parse.
func Parse(html string) []model.Product {
	starts := cardStartRe.FindAllStringIndex(html, -1)
	if len(starts) == 0 {
		return nil
	}

	fetchedAt := time.Now().UTC()
	products := make([]model.Product, 0, len(starts))

	for i, start := range starts {
		end := len(html)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := html[start[0]:end]

		product, err := parseCard(block, fetchedAt)
		if err != nil {
			log.Printf("parser: skipping malformed product card %d: %v", i, err)
			continue
		}
		products = append(products, product)
	}

	return products
}

// parseCard extracts a single model.Product from one card's HTML block. It
// returns an error (never panics) if a required field is missing or
// unparseable, so the caller can skip the card without losing the rest of
// the listing.
func parseCard(block string, fetchedAt time.Time) (model.Product, error) {
	name := firstSubmatch(nameRe, block)
	if strings.TrimSpace(name) == "" {
		return model.Product{}, errMalformed("missing or empty product name")
	}

	url := firstSubmatch(urlRe, block)
	if strings.TrimSpace(url) == "" {
		return model.Product{}, errMalformed("missing product URL")
	}

	priceText := firstSubmatch(priceCurrentRe, block)
	price, err := parsePrice(priceText)
	if err != nil {
		return model.Product{}, errMalformed("missing or unparseable price: " + err.Error())
	}

	originalPrice := price
	if origText := firstSubmatch(priceOriginalRe, block); origText != "" {
		if op, err := parsePrice(origText); err == nil {
			originalPrice = op
		}
	}

	discountPct := 0.0
	if discText := firstSubmatch(discountRe, block); discText != "" {
		if pct, err := strconv.ParseFloat(discText, 64); err == nil {
			discountPct = pct
		}
	} else if originalPrice > price && originalPrice > 0 {
		discountPct = (float64(originalPrice-price) / float64(originalPrice)) * 100
	}

	inStock := false
	if status := firstSubmatch(stockRe, block); status != "" {
		inStock = status == "in-stock"
	}

	return model.Product{
		Name:          strings.TrimSpace(name),
		URL:           strings.TrimSpace(url),
		Price:         price,
		OriginalPrice: originalPrice,
		DiscountPct:   discountPct,
		InStock:       inStock,
		FetchedAt:     fetchedAt,
	}, nil
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// parsePrice strips currency symbols/thousands separators from text like
// "45.990.000₫" and parses the remaining digits as an integer VND amount.
func parsePrice(text string) (int, error) {
	digits := nonDigits.ReplaceAllString(text, "")
	if digits == "" {
		return 0, errMalformed("no digits in price text")
	}
	return strconv.Atoi(digits)
}

type parseError string

func errMalformed(msg string) error { return parseError(msg) }
func (e parseError) Error() string  { return string(e) }
