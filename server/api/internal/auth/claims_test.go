package auth

import (
	"reflect"
	"testing"
)

func TestClaimStrings(t *testing.T) {
	claims := map[string]any{
		"groups":       []any{"/engineering/speklo-admin", "payments"},
		"roles":        "api-designer", // some providers send a single string
		"realm_access": map[string]any{"roles": []any{"offline_access", "api-designer"}},
	}
	got := claimStrings(claims, []string{"roles", "realm_access.roles", "missing.path"})
	if want := []string{"api-designer", "offline_access"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	if got := claimStrings(claims, []string{"groups"}); len(got) != 2 {
		t.Fatalf("groups = %v", got)
	}
	if got := claimStrings(map[string]any{}, []string{"groups"}); got == nil || len(got) != 0 {
		t.Fatalf("missing claim should give an empty list, got %#v", got)
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		values, wanted []string
		want           bool
	}{
		{[]string{"/engineering/speklo-admin"}, []string{"speklo-admin"}, true},
		{[]string{"Speklo-Admin"}, []string{"speklo-admin"}, true},
		{[]string{"7f3c2e9a-…-group-object-id"}, []string{"7f3c2e9a-…-group-object-id"}, true}, // Entra ID group IDs
		{[]string{"payments"}, []string{"speklo-admin"}, false},
		{nil, []string{"speklo-admin"}, false},
	}
	for _, c := range cases {
		if got := Matches(c.values, c.wanted); got != c.want {
			t.Errorf("Matches(%v, %v) = %v", c.values, c.wanted, got)
		}
	}
}
