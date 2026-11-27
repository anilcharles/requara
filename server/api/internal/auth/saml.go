package auth

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"

	"github.com/anilcharles/requara/server/api/internal/config"
)

// samlProvider signs in with a SAML 2.0 identity provider (Okta, Entra ID,
// Google Workspace, ADFS, Keycloak, OneLogin, JumpCloud, …), SP-initiated.
//
// The identity provider posts its response to /auth/saml/<id>/acs from its
// own origin, so the browser leaves Requara's SameSite login cookie behind.
// The login state therefore lives in the database, keyed by the RelayState,
// and is deleted when the response arrives: each response is accepted once.
type samlProvider struct {
	cfg      config.AuthProvider
	frontend string

	mu sync.Mutex
	sp *saml.ServiceProvider
}

func newSAML(cfg config.AuthProvider, frontend string) *samlProvider {
	return &samlProvider{cfg: cfg, frontend: frontend}
}

func (p *samlProvider) info() config.AuthProvider { return p.cfg }

func (p *samlProvider) acsURL() string { return p.frontend + "/auth/saml/" + p.cfg.ID + "/acs" }

// MetadataURL is this service provider's metadata, for the identity provider.
func (p *samlProvider) metadataURL() string {
	return p.frontend + "/auth/saml/" + p.cfg.ID + "/metadata"
}

func (p *samlProvider) entityID() string {
	if p.cfg.SAMLEntityID != "" {
		return p.cfg.SAMLEntityID
	}
	return p.metadataURL()
}

// pemOrFile accepts PEM text or a path to a PEM file.
func pemOrFile(v string) ([]byte, error) {
	if strings.Contains(v, "-----BEGIN") {
		return []byte(v), nil
	}
	return os.ReadFile(v)
}

// idpCertBase64 turns a PEM (or bare base64) certificate into the base64 DER
// that SAML metadata carries.
func idpCertBase64(v string) (string, error) {
	raw, err := pemOrFile(v)
	if err != nil {
		if !strings.ContainsAny(v, "/\\") {
			raw = []byte(v) // bare base64, as some admin consoles show it
		} else {
			return "", err
		}
	}
	if block, _ := pem.Decode(raw); block != nil {
		raw = block.Bytes
	} else {
		der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(raw)), ""))
		if err != nil {
			return "", errors.New("the IdP certificate is neither PEM nor base64")
		}
		raw = der
	}
	if _, err := x509.ParseCertificate(raw); err != nil {
		return "", fmt.Errorf("the IdP certificate doesn't parse: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// load builds the service provider on first use (and retries on the next
// sign-in if the identity provider's metadata couldn't be fetched).
func (p *samlProvider) load(ctx context.Context) (*saml.ServiceProvider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sp != nil {
		return p.sp, nil
	}
	var idp *saml.EntityDescriptor
	var err error
	switch {
	case p.cfg.SAMLMetadataURL != "":
		u, perr := url.Parse(p.cfg.SAMLMetadataURL)
		if perr != nil {
			return nil, fmt.Errorf("%s metadata URL: %w", p.cfg.Name, perr)
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		idp, err = samlsp.FetchMetadata(ctx, http.DefaultClient, *u)
	case p.cfg.SAMLMetadataFile != "":
		var raw []byte
		if raw, err = os.ReadFile(p.cfg.SAMLMetadataFile); err == nil {
			idp, err = samlsp.ParseMetadata(raw)
		}
	default:
		var cert string
		if cert, err = idpCertBase64(p.cfg.SAMLIdPCert); err == nil {
			idp = &saml.EntityDescriptor{
				EntityID: p.cfg.SAMLIdPEntityID,
				IDPSSODescriptors: []saml.IDPSSODescriptor{{
					SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{
						ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol",
						KeyDescriptors: []saml.KeyDescriptor{{
							Use:     "signing",
							KeyInfo: saml.KeyInfo{X509Data: saml.X509Data{X509Certificates: []saml.X509Certificate{{Data: cert}}}},
						}},
					}},
					SingleSignOnServices: []saml.Endpoint{{Binding: saml.HTTPRedirectBinding, Location: p.cfg.SAMLSSOURL}},
				}},
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("can't load %s SAML metadata: %w", p.cfg.Name, err)
	}
	acs, _ := url.Parse(p.acsURL())
	meta, _ := url.Parse(p.metadataURL())
	sp := &saml.ServiceProvider{
		EntityID:          p.entityID(),
		MetadataURL:       *meta,
		AcsURL:            *acs,
		IDPMetadata:       idp,
		AuthnNameIDFormat: saml.UnspecifiedNameIDFormat,
		// Only SP-initiated sign-ins: an unsolicited response has no
		// stored state to tie it to, so it is refused.
		AllowIDPInitiated:    false,
		RejectSHA1Signatures: true,
	}
	if p.cfg.SAMLSPCertFile != "" {
		pair, err := tls.LoadX509KeyPair(p.cfg.SAMLSPCertFile, p.cfg.SAMLSPKeyFile)
		if err != nil {
			return nil, fmt.Errorf("%s SAML signing key: %w", p.cfg.Name, err)
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, err
		}
		signer, ok := pair.PrivateKey.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("%s SAML signing key can't sign", p.cfg.Name)
		}
		sp.Key, sp.Certificate = signer, cert
		sp.SignatureMethod = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
	}
	p.sp = sp
	return sp, nil
}

// authURL returns the identity provider's sign-in address with an
// AuthnRequest; st.SAMLRequestID records its ID for the response check.
func (p *samlProvider) authURL(ctx context.Context, st *loginState, relayState string) (string, error) {
	sp, err := p.load(ctx)
	if err != nil {
		return "", err
	}
	loc := sp.GetSSOBindingLocation(saml.HTTPRedirectBinding)
	if loc == "" {
		return "", fmt.Errorf("%s has no HTTP-Redirect sign-in endpoint in its metadata", p.cfg.Name)
	}
	req, err := sp.MakeAuthenticationRequest(loc, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return "", err
	}
	st.SAMLRequestID = req.ID
	u, err := req.Redirect(relayState, sp)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// parse checks the posted response and turns its assertion into claims.
func (p *samlProvider) parse(r *http.Request, st *loginState) (*Claims, error) {
	sp, err := p.load(r.Context())
	if err != nil {
		return nil, err
	}
	as, err := sp.ParseResponse(r, []string{st.SAMLRequestID})
	if err != nil {
		var ire *saml.InvalidResponseError
		if errors.As(err, &ire) && ire.PrivateErr != nil {
			err = ire.PrivateErr
		}
		return nil, fmt.Errorf("%s sign-in response rejected: %w", p.cfg.Name, err)
	}
	return samlClaims(as, p.cfg)
}

// attrs gathers attribute values by name or friendly name.
func attrs(as *saml.Assertion) map[string][]string {
	out := map[string][]string{}
	for _, stmt := range as.AttributeStatements {
		for _, a := range stmt.Attributes {
			var vals []string
			for _, v := range a.Values {
				if s := strings.TrimSpace(v.Value); s != "" {
					vals = append(vals, s)
				}
			}
			for _, k := range []string{a.Name, a.FriendlyName} {
				if k != "" {
					out[strings.ToLower(k)] = append(out[strings.ToLower(k)], vals...)
				}
			}
		}
	}
	return out
}

func samlClaims(as *saml.Assertion, cfg config.AuthProvider) (*Claims, error) {
	if as.Subject == nil || as.Subject.NameID == nil || strings.TrimSpace(as.Subject.NameID.Value) == "" {
		return nil, fmt.Errorf("%s sent no NameID", cfg.Name)
	}
	nameID := strings.TrimSpace(as.Subject.NameID.Value)
	a := attrs(as)
	first := func(names []string) string {
		for _, n := range names {
			if v := a[strings.ToLower(n)]; len(v) > 0 {
				return v[0]
			}
		}
		return ""
	}
	cl := &Claims{
		Subject:       nameID,
		Email:         first(cfg.SAMLEmailAttrs),
		Name:          first(cfg.SAMLNameAttrs),
		GivenName:     first([]string{"givenName", "firstName", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname", "urn:oid:2.5.4.42"}),
		FamilyName:    first([]string{"sn", "surname", "lastName", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/surname", "urn:oid:2.5.4.4"}),
		EmailVerified: cfg.TrustEmail,
	}
	if cl.Email == "" && (as.Subject.NameID.Format == string(saml.EmailAddressNameIDFormat) || strings.Contains(nameID, "@")) {
		cl.Email = nameID
	}
	cl.Username = cl.Email
	seen := map[string]bool{}
	for _, n := range cfg.SAMLGroupsAttrs {
		for _, g := range a[strings.ToLower(n)] {
			if !seen[g] {
				seen[g] = true
				cl.Groups = append(cl.Groups, g)
			}
		}
	}
	return cl, nil
}

// Metadata is this service provider's SAML metadata XML.
func (p *samlProvider) metadata(ctx context.Context) (*saml.EntityDescriptor, error) {
	sp, err := p.load(ctx)
	if err != nil {
		return nil, err
	}
	return sp.Metadata(), nil
}
