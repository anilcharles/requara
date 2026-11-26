package auth

import "strings"

// claimStrings collects string values from the claim paths ("groups",
// "realm_access.roles") that exist, as one de-duplicated list. A claim may be
// a list of strings or a single string.
func claimStrings(claims map[string]any, paths []string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, path := range paths {
		var v any = claims
		for _, part := range strings.Split(path, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v = m[part]
		}
		switch x := v.(type) {
		case string:
			add(x)
		case []any:
			for _, e := range x {
				if s, ok := e.(string); ok {
					add(s)
				}
			}
		}
	}
	return out
}

// Matches reports whether any of the person's roles or groups is in wanted.
// A group path such as "/engineering/requara-admin" also matches by its last
// segment, so "requara-admin" works across providers.
func Matches(values, wanted []string) bool {
	for _, v := range values {
		last := v[strings.LastIndex(v, "/")+1:]
		for _, w := range wanted {
			if strings.EqualFold(v, w) || strings.EqualFold(last, w) {
				return true
			}
		}
	}
	return false
}
