package prefilter

import "strings"

// Tokenize lowercases s and splits it on whitespace and hyphens, dropping
// empty tokens. It is used identically on both target and product names so
// their tokens compare on equal footing (case, hyphenation, and whitespace
// don't affect matching).
func Tokenize(s string) []string {
	lower := strings.ToLower(s)
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
