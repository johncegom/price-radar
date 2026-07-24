package prefilter

import (
	"testing"

	"priceradar/internal/model"
)

func product(name string) model.Product {
	return model.Product{Name: name, URL: "https://fptshop.com.vn/" + name}
}

func TestFilter_EmptyInput(t *testing.T) {
	target := model.Target{Name: "MacBook Pro M2 Pro 32GB 512GB"}

	got := Filter(target, nil)
	if len(got) != 0 {
		t.Fatalf("Filter with nil products = %#v, want empty", got)
	}

	got = Filter(target, []model.Product{})
	if len(got) != 0 {
		t.Fatalf("Filter with empty products = %#v, want empty", got)
	}

	got = Filter(model.Target{Name: ""}, []model.Product{product("MacBook Pro M2 Pro")})
	if len(got) != 0 {
		t.Fatalf("Filter with empty target name = %#v, want empty", got)
	}
}

func TestFilter_ExactMatchScoresHighest(t *testing.T) {
	target := model.Target{Name: "MacBook Pro M2 Pro 32GB 512GB"}
	products := []model.Product{
		product("MacBook Pro M2 Pro 32GB 512GB"),
		product("MacBook Pro M2 Pro 16GB 512GB"),
		product("MacBook Air M2 256GB"),
	}

	got := Filter(target, products)
	if len(got) == 0 {
		t.Fatalf("Filter returned no candidates")
	}
	if got[0].Product.Name != "MacBook Pro M2 Pro 32GB 512GB" {
		t.Fatalf("top candidate = %q, want exact match first", got[0].Product.Name)
	}
	if got[0].Score != 1.0 {
		t.Fatalf("exact match score = %v, want 1.0", got[0].Score)
	}
}

func TestFilter_CategoryMismatchExcluded(t *testing.T) {
	target := model.Target{Name: "MacBook Pro M2 Pro 32GB 512GB"}
	products := []model.Product{
		product("iPhone 15 Pro Max 256GB"),
		product("iPad Pro M2 128GB"),
		product("Samsung Galaxy S23 Ultra"),
	}

	got := Filter(target, products)
	for _, c := range got {
		if c.Product.Name == "Samsung Galaxy S23 Ultra" {
			t.Errorf("Samsung Galaxy should be hard-excluded as a category mismatch, got %#v", c)
		}
	}
}

func TestFilter_ImperfectButPlausibleMatchRetained(t *testing.T) {
	// Same product family/line as target, but a different RAM/storage
	// variant and reordered/relabeled -- this should be scored and kept
	// (recall bias), not hard-excluded, so the judgment layer can weigh in.
	target := model.Target{Name: "MacBook Pro M2 Pro 32GB 512GB"}
	products := []model.Product{
		product("MacBook Pro M2 Pro 16GB 1TB Demo"),
	}

	got := Filter(target, products)
	if len(got) != 1 {
		t.Fatalf("Filter = %#v, want the imperfect same-family match retained", got)
	}
	if got[0].Score <= 0 || got[0].Score >= 1.0 {
		t.Fatalf("score = %v, want a partial score strictly between 0 and 1", got[0].Score)
	}
}

func TestFilter_CapsAtFiveCandidates(t *testing.T) {
	target := model.Target{Name: "MacBook Pro M2 Pro 32GB 512GB"}
	var products []model.Product
	for i := 0; i < 10; i++ {
		products = append(products, product("MacBook Pro M2 Pro 16GB 512GB Demo"))
	}

	got := Filter(target, products)
	if len(got) != maxCandidates {
		t.Fatalf("Filter returned %d candidates, want capped at %d", len(got), maxCandidates)
	}
}

func TestFilter_RepresentativeFixture(t *testing.T) {
	target := model.Target{Name: "MacBook Pro M2 Pro 32GB 512GB"}
	products := []model.Product{
		product("MacBook Pro M2 Pro 32GB 512GB"),          // exact match
		product("MacBook Pro M2 Pro 16GB 512GB - Demo"),    // same family, diff variant
		product("MacBook Pro M2 Max 32GB 1TB"),             // same family, diff chip variant
		product("MacBook Air M1 8GB 256GB"),                // same family (macbook), very different spec
		product("iPhone 15 Pro Max 256GB"),                 // different family
		product("iPad Pro M2 128GB"),                       // different family
		product("Samsung Galaxy Tab S9"),                   // different family entirely
	}

	got := Filter(target, products)
	if len(got) == 0 || len(got) > maxCandidates {
		t.Fatalf("Filter returned %d candidates, want between 1 and %d", len(got), maxCandidates)
	}
	for _, c := range got {
		if c.Product.Name == "Samsung Galaxy Tab S9" {
			t.Errorf("unrelated product family leaked into shortlist: %#v", c)
		}
	}
	// Scores must be sorted descending.
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("candidates not sorted by descending score: %#v", got)
		}
	}
}
