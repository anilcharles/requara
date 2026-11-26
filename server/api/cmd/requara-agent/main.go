// Command requara-agent lets Requara in the browser send requests from your
// computer without the browser's limits (CORS, cookies, localhost), like
// the Postman Agent. It listens on 127.0.0.1 only.
//
// A web page can only use it with the pairing token it prints (paste it into
// Requara → Preferences → Local agent once), so other websites you visit
// can't use it to reach your machine or your network.
//
//	requara-agent            # prints the token and listens on 127.0.0.1:47321
//	requara-agent -new-token # makes a new token (unpairs every browser)
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxBody = 10 << 20

type sendIn struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"` // base64
	TimeoutMs       int               `json:"timeoutMs"`
	FollowRedirects *bool             `json:"followRedirects"`
	VerifyTLS       *bool             `json:"verifyTLS"`
}

type sendOut struct {
	Status     int         `json:"status"`
	StatusText string      `json:"statusText"`
	Headers    [][2]string `json:"headers"`
	Body       string      `json:"body"` // base64
	DurationMs int64       `json:"durationMs"`
	Error      string      `json:"error,omitempty"`
}

func tokenFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "requara-agent", "token")
}

func loadToken(renew bool) (string, error) {
	path := tokenFile()
	if !renew {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
			return strings.TrimSpace(string(b)), nil
		}
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := "ska_" + hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return tok, os.WriteFile(path, []byte(tok+"\n"), 0o600)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:47321", "where to listen (keep it on 127.0.0.1)")
	renew := flag.Bool("new-token", false, "make a new pairing token")
	flag.Parse()
	if !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") && !strings.HasPrefix(*addr, "[::1]:") {
		log.Fatal("requara-agent only listens on this computer (127.0.0.1)")
	}
	token, err := loadToken(*renew)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		cors(w, r)
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "requara-agent", "version": "1"})
	})
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		cors(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		got := r.Header.Get("X-Requara-Agent-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(sendOut{Error: "This page isn't paired with the agent. Paste the agent's token in Requara → Preferences."})
			return
		}
		var in sendIn
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody*2)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(send(in))
	})
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("Requara agent listening on http://%s\n\nPairing token (paste it in Requara → Preferences → Local agent):\n\n  %s\n\n", *addr, token)
	log.Fatal(srv.ListenAndServe())
}

// cors lets any page talk to the agent; only paired pages (with the token) can send.
func cors(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	if o := r.Header.Get("Origin"); o != "" {
		h.Set("Access-Control-Allow-Origin", o)
		h.Add("Vary", "Origin")
	}
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, X-Requara-Agent-Token")
	h.Set("Access-Control-Allow-Private-Network", "true") // Chrome's Private Network Access
	h.Set("Access-Control-Max-Age", "600")
	h.Set("Content-Type", "application/json")
}

func send(in sendIn) sendOut {
	body, err := base64.StdEncoding.DecodeString(in.Body)
	if err != nil {
		return sendOut{Error: "body isn't base64"}
	}
	timeout := time.Duration(in.TimeoutMs) * time.Millisecond
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 30 * time.Second
	}
	verify := in.VerifyTLS == nil || *in.VerifyTLS
	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !verify}, Proxy: http.ProxyFromEnvironment}, //nolint:gosec // user setting
	}
	if in.FollowRedirects != nil && !*in.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(in.Method, in.URL, rd)
	if err != nil {
		return sendOut{Error: err.Error()}
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		var ue interface{ Timeout() bool }
		if errors.As(err, &ue) && ue.Timeout() {
			return sendOut{Error: "timed out after " + timeout.String(), DurationMs: time.Since(start).Milliseconds()}
		}
		return sendOut{Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	out := sendOut{Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode), Body: base64.StdEncoding.EncodeToString(b), DurationMs: time.Since(start).Milliseconds()}
	for k, vs := range resp.Header {
		for _, v := range vs {
			out.Headers = append(out.Headers, [2]string{k, v})
		}
	}
	return out
}
