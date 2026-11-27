package runner

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// multipartBody builds a multipart/form-data body. File fields carry base64 data.
func multipartBody(fields []FormField, vars map[string]string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range fields {
		if !f.Enabled || f.Key == "" {
			continue
		}
		key := Substitute(f.Key, vars)
		if f.Type != "file" {
			if err := w.WriteField(key, Substitute(f.Value, vars)); err != nil {
				return nil, "", err
			}
			continue
		}
		data, err := base64.StdEncoding.DecodeString(f.Value)
		if err != nil {
			return nil, "", fmt.Errorf("the file for %q couldn't be read", f.Key)
		}
		h := textproto.MIMEHeader{}
		name := f.FileName
		if name == "" {
			name = "file"
		}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(key), escapeQuotes(name)))
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

var quoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }

// cookieHeader is the Cookie header for rawURL from the stored cookies.
func cookieHeader(cookies []Cookie, rawURL string, now time.Time) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	var parts []string
	for _, c := range cookies {
		d := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if host != d && !strings.HasSuffix(host, "."+d) {
			continue
		}
		cp := c.Path
		if cp == "" {
			cp = "/"
		}
		if !strings.HasPrefix(path, cp) {
			continue
		}
		if c.Secure && u.Scheme != "https" {
			continue
		}
		if c.Expires != nil && c.Expires.Before(now) {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// responseCookies reads Set-Cookie headers, defaulting domain and path.
func responseCookies(resp *http.Response) []Cookie {
	var out []Cookie
	for _, c := range resp.Cookies() {
		d := c.Domain
		if d == "" {
			d = resp.Request.URL.Hostname()
		}
		p := c.Path
		if p == "" {
			p = "/"
		}
		x := Cookie{Domain: strings.TrimPrefix(d, "."), Path: p, Name: c.Name, Value: c.Value, Secure: c.Secure, HTTPOnly: c.HttpOnly}
		switch {
		case c.MaxAge < 0:
			t := time.Unix(0, 0)
			x.Expires = &t // delete
		case c.MaxAge > 0:
			t := time.Now().Add(time.Duration(c.MaxAge) * time.Second)
			x.Expires = &t
		case !c.Expires.IsZero():
			t := c.Expires
			x.Expires = &t
		}
		out = append(out, x)
	}
	return out
}
