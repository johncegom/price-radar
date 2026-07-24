package prefilter

import (
	"regexp"
	"sort"

	"priceradar/internal/model"
)

// maxCandidates caps the shortlist handed to the judgment layer -- it must
// stay small (0-5), never the full catalog.
const maxCandidates = 5

// specWords are tokens that describe a variant/spec/condition rather than
// the product family/line itself (RAM/storage units, condition labels,
// etc.). They're excluded when deciding whether two names belong to the
// same product family, since a variant differing only in these tokens
// should never be treated as a category mismatch.
var specWords = map[string]bool{
	"gb": true, "tb": true, "mb": true,
	"cu": true, "demo": true, "new": true, "moi": true,
	"like": true, "chinh": true, "hang": true, "cy": true,
	"99": true, "98": true, "97": true, "95": true, "90": true,
}

// capacityToken matches spec/variant tokens like "32gb", "512gb", "1tb"
// (a number directly followed by a storage/memory unit).
var capacityToken = regexp.MustCompile(`^\d+(gb|tb|mb)$`)

// isFamilyToken reports whether a token is meaningful for identifying a
// product's family/line (as opposed to a spec/variant/condition detail or a
// bare number/capacity figure such as "32gb").
func isFamilyToken(tok string) bool {
	if specWords[tok] || tok == "" || capacityToken.MatchString(tok) {
		return false
	}
	isNumeric := true
	for _, r := range tok {
		if r < '0' || r > '9' {
			isNumeric = false
			break
		}
	}
	return !isNumeric
}

func tokenSet(tokens []string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	return set
}

// Filter scores products against target by token overlap and returns a
// small, recall-biased shortlist (0-5 candidates) for the judgment layer.
//
// Products whose name shares none of the target's product-family tokens
// (i.e. a different device line entirely, ignoring spec/variant/condition
// words and bare numbers) are hard-excluded regardless of any incidental
// token overlap. Everything else is soft-included and scored by
// matched/total token overlap against the target name, biased toward
// recall: an imperfect-but-plausible same-family match (different
// RAM/storage variant, reordered words, "Demo"/refurb labels, etc.) is kept
// rather than dropped, so the judgment layer -- not this package -- makes
// the final ambiguous call.
func Filter(target model.Target, products []model.Product) []model.Candidate {
	targetTokens := Tokenize(target.Name)
	if len(targetTokens) == 0 || len(products) == 0 {
		return []model.Candidate{}
	}

	var targetFamily []string
	for _, t := range targetTokens {
		if isFamilyToken(t) {
			targetFamily = append(targetFamily, t)
		}
	}
	targetFamilySet := tokenSet(targetFamily)

	candidates := make([]model.Candidate, 0, len(products))
	for _, p := range products {
		productTokens := Tokenize(p.Name)
		productSet := tokenSet(productTokens)

		// Hard-exclude category mismatches: if the target has identifiable
		// family tokens and the product shares none of them, it's a
		// different product line entirely.
		if len(targetFamilySet) > 0 {
			familyOverlap := false
			for f := range targetFamilySet {
				if productSet[f] {
					familyOverlap = true
					break
				}
			}
			if !familyOverlap {
				continue
			}
		}

		var matched, missing []string
		for _, t := range targetTokens {
			if productSet[t] {
				matched = append(matched, t)
			} else {
				missing = append(missing, t)
			}
		}
		if len(matched) == 0 {
			// No overlap at all despite passing the family check (can
			// happen when every target token happens to be a spec word);
			// nothing meaningful to score.
			continue
		}

		score := float64(len(matched)) / float64(len(targetTokens))
		candidates = append(candidates, model.Candidate{
			Product:       p,
			Score:         score,
			MatchedTokens: matched,
			MissingTokens: missing,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}
	return candidates
}
