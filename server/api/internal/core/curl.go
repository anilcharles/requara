package core

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	"github.com/anilcharles/requara/server/api/internal/runner"
)

// shellWords splits a command line like a POSIX shell: quotes, escapes and
// backslash-newline continuations (also Windows ^ continuations).
func shellWords(s string) ([]string, error) {
	s = strings.NewReplacer("\\\r\n", " ", "\\\n", " ", "^\r\n", " ", "^\n", " ").Replace(s)
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			inWord = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated ' quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			inWord = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune("\"\\$`", rune(s[i+1])) {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New(`unterminated " quote`)
			}
		case c == '$' && i+1 < len(s) && s[i+1] == '\'': // $'…' with \n escapes
			inWord = true
			i += 2
			for ; i < len(s) && s[i] != '\''; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					switch s[i] {
					case 'n':
						cur.WriteByte('\n')
					case 't':
						cur.WriteByte('\t')
					default:
						cur.WriteByte(s[i])
					}
					continue
				}
				cur.WriteByte(s[i])
			}
		case c == '\\' && i+1 < len(s):
			inWord = true
			i++
			cur.WriteByte(s[i])
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

// ParseCurl turns a curl command into a request.
func ParseCurl(cmd string) (*Request, error) {
	words, err := shellWords(strings.TrimSpace(cmd))
	if err != nil {
		return nil, err
	}
	if len(words) == 0 || (words[0] != "curl" && !strings.HasSuffix(words[0], "/curl") && words[0] != "curl.exe") {
		return nil, errors.New("paste a command that starts with curl")
	}
	r := &Request{Method: "", Params: []runner.KV{}, Headers: []runner.KV{}, FormData: []runner.FormField{}, Examples: []Example{}, BodyMode: "none", Auth: runner.Auth{Type: "none"}}
	var data []string
	get := false
	isJSON := false
	takes := map[string]bool{
		"-X": true, "--request": true, "-H": true, "--header": true, "-d": true, "--data": true, "--data-raw": true, "--data-binary": true,
		"--data-ascii": true, "--data-urlencode": true, "--json": true, "-u": true, "--user": true, "-F": true, "--form": true, "--form-string": true,
		"-b": true, "--cookie": true, "-A": true, "--user-agent": true, "-e": true, "--referer": true, "--url": true, "-o": true, "--output": true,
		"-m": true, "--max-time": true, "--connect-timeout": true, "-x": true, "--proxy": true, "--retry": true, "-w": true, "--write-out": true,
		"--cacert": true, "--cert": true, "--key": true, "-c": true, "--cookie-jar": true,
	}
	for i := 1; i < len(words); i++ {
		w := words[i]
		flag, val, inline := w, "", false
		if strings.HasPrefix(w, "--") && strings.Contains(w, "=") {
			flag, val, _ = strings.Cut(w, "=")
			inline = true
		} else if len(w) > 2 && w[0] == '-' && w[1] != '-' && takes[w[:2]] {
			flag, val, inline = w[:2], w[2:], true // -XPOST, -H'…'
		}
		if takes[flag] && !inline {
			if i+1 >= len(words) {
				return nil, errors.New(flag + " needs a value")
			}
			i++
			val = words[i]
		}
		switch flag {
		case "-X", "--request":
			r.Method = strings.ToUpper(val)
		case "-H", "--header":
			k, v, _ := strings.Cut(val, ":")
			r.Headers = append(r.Headers, runner.KV{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v), Enabled: true})
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii":
			data = append(data, strings.TrimPrefix(val, "$"))
		case "--data-urlencode":
			if k, v, ok := strings.Cut(val, "="); ok {
				data = append(data, k+"="+url.QueryEscape(v))
			} else {
				data = append(data, url.QueryEscape(val))
			}
		case "--json":
			data = append(data, val)
			isJSON = true
		case "-u", "--user":
			u, p, _ := strings.Cut(val, ":")
			r.Auth = runner.Auth{Type: "basic", Username: u, Password: p}
		case "-F", "--form", "--form-string":
			k, v, _ := strings.Cut(val, "=")
			if strings.HasPrefix(v, "@") && flag != "--form-string" {
				r.FormData = append(r.FormData, runner.FormField{Key: k, Type: "file", FileName: strings.TrimPrefix(v, "@"), Enabled: true})
			} else {
				r.FormData = append(r.FormData, runner.FormField{Key: k, Value: v, Type: "text", Enabled: true})
			}
		case "-b", "--cookie":
			r.Headers = append(r.Headers, runner.KV{Key: "Cookie", Value: val, Enabled: true})
		case "-A", "--user-agent":
			r.Headers = append(r.Headers, runner.KV{Key: "User-Agent", Value: val, Enabled: true})
		case "-e", "--referer":
			r.Headers = append(r.Headers, runner.KV{Key: "Referer", Value: val, Enabled: true})
		case "--url":
			r.URL = val
		case "-G", "--get":
			get = true
		case "-I", "--head":
			r.Method = "HEAD"
		default:
			if !strings.HasPrefix(w, "-") && r.URL == "" {
				r.URL = w
			}
		}
	}
	if r.URL == "" {
		return nil, errors.New("the curl command has no URL")
	}
	if !strings.Contains(r.URL, "://") {
		r.URL = "http://" + r.URL
	}
	if u, err := url.Parse(r.URL); err == nil && u.RawQuery != "" {
		for _, pair := range strings.Split(u.RawQuery, "&") {
			k, v, _ := strings.Cut(pair, "=")
			kk, _ := url.QueryUnescape(k)
			vv, _ := url.QueryUnescape(v)
			r.Params = append(r.Params, runner.KV{Key: kk, Value: vv, Enabled: true})
		}
		u.RawQuery = ""
		r.URL = u.String()
	}
	body := strings.Join(data, "&")
	switch {
	case get && body != "":
		for _, pair := range strings.Split(body, "&") {
			k, v, _ := strings.Cut(pair, "=")
			r.Params = append(r.Params, runner.KV{Key: k, Value: v, Enabled: true})
		}
	case len(r.FormData) > 0:
		r.BodyMode = "form-data"
	case body != "":
		r.Body = body
		ct := ""
		for _, h := range r.Headers {
			if strings.EqualFold(h.Key, "Content-Type") {
				ct = strings.ToLower(h.Value)
			}
		}
		trimmed := strings.TrimSpace(body)
		switch {
		case isJSON || strings.Contains(ct, "json") || ((strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && ct == ""):
			r.BodyMode = "json"
		case ct == "" || strings.Contains(ct, "x-www-form-urlencoded"):
			r.BodyMode = "form-urlencoded"
		default:
			r.BodyMode = "raw"
		}
	}
	if isJSON {
		r.Headers = append(r.Headers, runner.KV{Key: "Content-Type", Value: "application/json", Enabled: true}, runner.KV{Key: "Accept", Value: "application/json", Enabled: true})
	}
	if r.Method == "" {
		r.Method = "GET"
		if (body != "" && !get) || len(r.FormData) > 0 {
			r.Method = "POST"
		}
	}
	// An Authorization: Bearer header becomes the request's auth.
	for i, h := range r.Headers {
		if strings.EqualFold(h.Key, "Authorization") {
			if tok, ok := strings.CutPrefix(h.Value, "Bearer "); ok {
				r.Auth = runner.Auth{Type: "bearer", Token: tok}
				r.Headers = append(r.Headers[:i], r.Headers[i+1:]...)
			} else if b, ok := strings.CutPrefix(h.Value, "Basic "); ok {
				if dec, err := base64.StdEncoding.DecodeString(b); err == nil {
					u, p, _ := strings.Cut(string(dec), ":")
					r.Auth = runner.Auth{Type: "basic", Username: u, Password: p}
					r.Headers = append(r.Headers[:i], r.Headers[i+1:]...)
				}
			}
			break
		}
	}
	u, _ := url.Parse(r.URL)
	r.Name = r.Method + " " + u.Path
	return r, nil
}
