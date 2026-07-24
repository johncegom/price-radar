package prefilter

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "hyphenation splits into separate tokens",
			in:   "MacBook Pro M2 Pro 32GB-512GB",
			want: []string{"macbook", "pro", "m2", "pro", "32gb", "512gb"},
		},
		{
			name: "case is normalized",
			in:   "MacBook PRO M2 Max",
			want: []string{"macbook", "pro", "m2", "max"},
		},
		{
			name: "extra/irregular whitespace is collapsed",
			in:   "  MacBook   Pro\tM2 \n Pro  ",
			want: []string{"macbook", "pro", "m2", "pro"},
		},
		{
			name: "leading/trailing hyphens don't produce empty tokens",
			in:   "-MacBook-Pro-",
			want: []string{"macbook", "pro"},
		},
		{
			name: "empty string yields no tokens",
			in:   "",
			want: []string{},
		},
		{
			name: "target and product names tokenize identically",
			in:   "MacBook-Pro M2-Pro",
			want: []string{"macbook", "pro", "m2", "pro"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}
