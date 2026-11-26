// Package bao wraps the OpenBao client for the two things the backend needs:
// reading its own platform secrets (KV v2) and encrypting customer secrets
// (Transit). The backend signs in with AppRole, never with a root token.
package bao

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openbao/openbao/api/v2"
)

type Client struct {
	api      *api.Client
	roleID   string
	secretID string
	mu       sync.Mutex
	keys     map[string]bool // transit keys known to exist
}

// New signs in with AppRole and keeps the token fresh in the background.
func New(ctx context.Context, addr, roleID, secretID string) (*Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = addr
	c, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	b := &Client{api: c, roleID: roleID, secretID: secretID, keys: map[string]bool{}}
	ttl, err := b.login(ctx)
	if err != nil {
		return nil, err
	}
	go b.renewLoop(ctx, ttl)
	return b, nil
}

func (b *Client) login(ctx context.Context) (time.Duration, error) {
	s, err := b.api.Logical().WriteWithContext(ctx, "auth/approle/login", map[string]any{
		"role_id": b.roleID, "secret_id": b.secretID,
	})
	if err != nil {
		return 0, fmt.Errorf("openbao approle login: %w", err)
	}
	if s == nil || s.Auth == nil {
		return 0, fmt.Errorf("openbao approle login: no auth in response")
	}
	b.api.SetToken(s.Auth.ClientToken)
	return time.Duration(s.Auth.LeaseDuration) * time.Second, nil
}

// renewLoop signs in again before the token expires.
func (b *Client) renewLoop(ctx context.Context, ttl time.Duration) {
	for {
		wait := ttl / 2
		if wait < 30*time.Second {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		next, err := b.login(ctx)
		if err != nil {
			slog.Error("openbao re-login failed", "err", err)
			next = time.Minute
		}
		ttl = next
	}
}

// KV reads a KV v2 secret at mount "secret".
func (b *Client) KV(ctx context.Context, path string) (map[string]string, error) {
	s, err := b.api.KVv2("secret").Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read secret/%s: %w", path, err)
	}
	out := map[string]string{}
	for k, v := range s.Data {
		out[k] = fmt.Sprint(v)
	}
	return out, nil
}

func (b *Client) ensureKey(ctx context.Context, key string) error {
	b.mu.Lock()
	known := b.keys[key]
	b.mu.Unlock()
	if known {
		return nil
	}
	// Creating an existing key is a no-op in Transit.
	if _, err := b.api.Logical().WriteWithContext(ctx, "transit/keys/"+key, map[string]any{"type": "aes256-gcm96"}); err != nil {
		return fmt.Errorf("ensure transit key %s: %w", key, err)
	}
	b.mu.Lock()
	b.keys[key] = true
	b.mu.Unlock()
	return nil
}

// Encrypt returns Transit ciphertext ("vault:v1:...") for plaintext under key.
func (b *Client) Encrypt(ctx context.Context, key, plaintext string) (string, error) {
	if err := b.ensureKey(ctx, key); err != nil {
		return "", err
	}
	s, err := b.api.Logical().WriteWithContext(ctx, "transit/encrypt/"+key, map[string]any{
		"plaintext": base64.StdEncoding.EncodeToString([]byte(plaintext)),
	})
	if err != nil {
		return "", fmt.Errorf("transit encrypt: %w", err)
	}
	return fmt.Sprint(s.Data["ciphertext"]), nil
}

// DecryptBatch decrypts many values under one key in a single call. ok[i] is false when
// value i can't be decrypted, e.g. after a dev-mode OpenBao restart lost the key; err is
// only for failures that affect the whole call (network, auth).
func (b *Client) DecryptBatch(ctx context.Context, key string, ciphertexts []string) (out []string, ok []bool, err error) {
	out, ok = make([]string, len(ciphertexts)), make([]bool, len(ciphertexts))
	if len(ciphertexts) == 0 {
		return out, ok, nil
	}
	items := make([]map[string]any, len(ciphertexts))
	for i, c := range ciphertexts {
		items[i] = map[string]any{"ciphertext": c}
	}
	s, err := b.api.Logical().WriteWithContext(ctx, "transit/decrypt/"+key, map[string]any{"batch_input": items})
	if err != nil {
		if strings.Contains(err.Error(), "encryption key not found") {
			slog.Warn("transit key missing; secrets under it can't be decrypted", "key", key)
			return out, ok, nil
		}
		return nil, nil, fmt.Errorf("transit decrypt: %w", err)
	}
	res, _ := s.Data["batch_results"].([]any)
	for i, r := range res {
		if i >= len(out) {
			break
		}
		m, _ := r.(map[string]any)
		if e, bad := m["error"]; bad && e != nil && fmt.Sprint(e) != "" {
			slog.Warn("transit decrypt item failed", "key", key, "item", i, "err", e)
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(fmt.Sprint(m["plaintext"])))
		out[i], ok[i] = string(raw), true
	}
	return out, ok, nil
}
