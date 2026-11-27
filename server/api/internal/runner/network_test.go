package runner

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRequestProxy(t *testing.T) {
	var seen string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.String() // a proxy gets the absolute URL
		fmt.Fprint(w, "via proxy")
	}))
	defer proxy.Close()
	res := send(t, Spec{Method: "GET", URL: "http://api.example.test/orders", Settings: Settings{Proxy: "{{proxy}}"}}, map[string]string{"proxy": proxy.URL})
	if res.Error != "" || res.Body != "via proxy" || seen != "http://api.example.test/orders" {
		t.Fatalf("body %q err %q proxy saw %q", res.Body, res.Error, seen)
	}
	res = send(t, Spec{Method: "GET", URL: "http://api.example.test/", Settings: Settings{Proxy: "ftp://nope"}}, nil)
	if !strings.Contains(res.Error, "Proxy:") {
		t.Fatalf("a bad proxy URL should say so, got %q", res.Error)
	}
}

func TestProxyDoesNotBypassGuard(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "reached") }))
	defer proxy.Close()
	// The operator's proxy may be private; the target still may not be.
	r := &Runner{Proxy: http.ProxyURL(mustURL(proxy.URL))}
	res := r.Send(context.Background(), Spec{Method: "GET", URL: "http://169.254.169.254/latest/meta-data"}, map[string]string{}, nil)
	if !strings.Contains(res.Error, "Blocked") {
		t.Fatalf("a private target through the proxy should be blocked, got %q %q", res.Body, res.Error)
	}
	// A person's own proxy on a private address is blocked too.
	res = (&Runner{}).Send(context.Background(), Spec{Method: "GET", URL: "http://api.example.test/", Settings: Settings{Proxy: proxy.URL}}, map[string]string{}, nil)
	if !strings.Contains(res.Error, "Blocked") {
		t.Fatalf("a private request proxy should be blocked, got %q %q", res.Body, res.Error)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// selfSigned makes a CA-capable certificate and its key, in PEM.
func selfSigned(t *testing.T, cn string) (certPEM, keyPEM string, cert tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"}, IPAddresses: nil,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalPKCS8PrivateKey(key)
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}))
	cert, err = tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	return certPEM, keyPEM, cert
}

func TestClientCertificateAndCustomCA(t *testing.T) {
	clientPEM, clientKey, clientCert := selfSigned(t, "requara-client")
	pool := x509.NewCertPool()
	pool.AddCert(clientCert.Leaf)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello ", r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	defer srv.Close()
	serverCA := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))

	// Trusting the server's CA and presenting the client certificate works,
	// with SSL verification left on.
	ok := Settings{ClientCert: "{{cert}}", ClientKey: "{{key}}", CACert: serverCA}
	res := send(t, Spec{Method: "GET", URL: srv.URL, Settings: ok}, map[string]string{"cert": clientPEM, "key": clientKey})
	if res.Error != "" || res.Body != "hello requara-client" {
		t.Fatalf("body %q err %q", res.Body, res.Error)
	}
	// Without the CA, verification fails.
	res = send(t, Spec{Method: "GET", URL: srv.URL, Settings: Settings{ClientCert: clientPEM, ClientKey: clientKey}}, nil)
	if res.Error == "" {
		t.Fatal("an unknown CA should fail verification")
	}
	// Without a client certificate, the server refuses the handshake.
	res = send(t, Spec{Method: "GET", URL: srv.URL, Settings: Settings{CACert: serverCA}}, nil)
	if res.Error == "" {
		t.Fatal("the server requires a client certificate")
	}
	// A key that doesn't match says so.
	_, otherKey, _ := selfSigned(t, "other")
	res = send(t, Spec{Method: "GET", URL: srv.URL, Settings: Settings{ClientCert: clientPEM, ClientKey: otherKey}}, nil)
	if !strings.Contains(res.Error, "Client certificate") {
		t.Fatalf("got %q", res.Error)
	}
}
