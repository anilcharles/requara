package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crewjam/saml"

	"github.com/anilcharles/requara/server/api/internal/config"
)

type memStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *memStore) Put(_ context.Context, id string, b []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = b
	return nil
}

func (s *memStore) Get(_ context.Context, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[id]
	if !ok {
		return nil, errors.New("none")
	}
	return b, nil
}

func (s *memStore) Take(ctx context.Context, id string) ([]byte, error) {
	b, err := s.Get(ctx, id)
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
	return b, err
}

func TestEmailFlow(t *testing.T) {
	store := &memStore{m: map[string][]byte{}}
	m, err := New(context.Background(), []config.AuthProvider{{ID: "email", Type: "email", Name: "Email", TrustEmail: true}},
		"http://requara.test", []byte(strings.Repeat("k", 32)), store)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	challenge := strings.Repeat("A", 43)
	if err := m.Begin(rec, httptest.NewRequest("GET", "/auth/login?provider=email&redirect=/apis&desktop_port=50123&desktop_challenge="+challenge, nil), false); err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Path != "/signin/email" || loc.Query().Get("flow") == "" {
		t.Fatalf("redirect = %s", loc)
	}
	flow := loc.Query().Get("flow")
	prov, desktop, err := m.EmailFlow(context.Background(), flow)
	if err != nil || prov != "email" || !desktop {
		t.Fatalf("EmailFlow = %q %v %v", prov, desktop, err)
	}
	res, err := m.FinishEmail(context.Background(), flow, " Ana@Acme.com ")
	if err != nil {
		t.Fatal(err)
	}
	if res.Claims.Subject != "email:ana@acme.com" || res.Claims.Email != "ana@acme.com" || !res.Claims.EmailVerified ||
		res.Claims.Name != "ana" || res.DesktopPort != 50123 || res.DesktopChallenge != challenge || res.Redirect != "/apis" {
		t.Errorf("result: %+v %+v", res, res.Claims)
	}
	if _, err := m.FinishEmail(context.Background(), flow, "ana@acme.com"); err == nil {
		t.Error("a flow must be usable only once")
	}
}

func TestSAMLClaims(t *testing.T) {
	cfg, _ := samlCfg()
	as := &saml.Assertion{
		Subject: &saml.Subject{NameID: &saml.NameID{Format: string(saml.EmailAddressNameIDFormat), Value: "ana@acme.com"}},
		AttributeStatements: []saml.AttributeStatement{{Attributes: []saml.Attribute{
			{Name: "http://schemas.microsoft.com/identity/claims/displayname", Values: []saml.AttributeValue{{Value: "Ana Lima"}}},
			{Name: "groups", Values: []saml.AttributeValue{{Value: "requara-admin"}, {Value: "eng"}, {Value: "eng"}}},
		}}},
	}
	cl, err := samlClaims(as, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Subject != "ana@acme.com" || cl.Email != "ana@acme.com" || cl.Name != "Ana Lima" || !cl.EmailVerified ||
		strings.Join(cl.Groups, ",") != "requara-admin,eng" {
		t.Errorf("claims: %+v", cl)
	}
	as.Subject.NameID = &saml.NameID{Format: string(saml.PersistentNameIDFormat), Value: "00u1abcd"}
	cl, _ = samlClaims(as, cfg)
	if cl.Email != "" {
		t.Errorf("an opaque NameID is not an email: %+v", cl)
	}
}

func samlCfg() (config.AuthProvider, error) {
	ps := config.AuthProvider{
		ID: "corp", Type: "saml", Name: "Corp SSO", TrustEmail: true,
		SAMLEmailAttrs:  []string{"email", "mail"},
		SAMLNameAttrs:   []string{"displayName", "http://schemas.microsoft.com/identity/claims/displayname"},
		SAMLGroupsAttrs: []string{"groups"},
	}
	return ps, nil
}

func TestSAMLRequiresStoredState(t *testing.T) {
	store := &memStore{m: map[string][]byte{}}
	cfg, _ := samlCfg()
	m, err := New(context.Background(), []config.AuthProvider{cfg}, "http://requara.test", []byte(strings.Repeat("k", 32)), store)
	if err != nil {
		t.Fatal(err)
	}
	// An IdP-initiated post (no RelayState) and an unknown RelayState are refused.
	for _, body := range []string{"SAMLResponse=x", "SAMLResponse=x&RelayState=forged"} {
		req := httptest.NewRequest("POST", "/auth/saml/corp/acs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := m.FinishSAML(req, "corp"); err == nil {
			t.Errorf("%s: accepted", body)
		}
	}
}
