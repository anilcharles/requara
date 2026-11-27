package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/anilcharles/requara/server/api/internal/runner"
)

// Collection is a collection as Requara imports and exports it.
type Collection struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Variables   []runner.KV `json:"variables"`
	Auth        runner.Auth `json:"auth"`
	PreScript   string      `json:"preScript"`
	PostScript  string      `json:"postScript"`
	Requests    []Request   `json:"requests"`
}

// Request is one request in an imported or exported collection.
type Request struct {
	Folder     string             `json:"folder"`
	Name       string             `json:"name"`
	Method     string             `json:"method"`
	URL        string             `json:"url"`
	Params     []runner.KV        `json:"params"`
	Headers    []runner.KV        `json:"headers"`
	BodyMode   string             `json:"bodyMode"`
	Body       string             `json:"body"`
	FormData   []runner.FormField `json:"formData"`
	Auth       runner.Auth        `json:"auth"`
	PreScript  string             `json:"preScript"`
	PostScript string             `json:"postScript"`
	Examples   []Example          `json:"examples"`
}

// Example is a saved response.
type Example struct {
	Name    string      `json:"name"`
	Status  int         `json:"status"`
	Headers []runner.KV `json:"headers"`
	Body    string      `json:"body"`
}

// ----- Postman Collection v2.0 / v2.1 -----

type pmKV struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Description any    `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	Type        string `json:"type,omitempty"`
	Src         any    `json:"src,omitempty"`
}

type pmAuth struct {
	Type   string `json:"type"`
	Bearer []pmKV `json:"bearer,omitempty"`
	Basic  []pmKV `json:"basic,omitempty"`
	APIKey []pmKV `json:"apikey,omitempty"`
	OAuth2 []pmKV `json:"oauth2,omitempty"`
	AWSv4  []pmKV `json:"awsv4,omitempty"`
	Digest []pmKV `json:"digest,omitempty"`
	JWT    []pmKV `json:"jwt,omitempty"`
}

type pmEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Exec any    `json:"exec"`
		Type string `json:"type,omitempty"`
	} `json:"script"`
}

type pmURL struct {
	Raw   string `json:"raw"`
	Query []pmKV `json:"query,omitempty"`
}

type pmBody struct {
	Mode       string         `json:"mode"`
	Raw        string         `json:"raw,omitempty"`
	URLEncoded []pmKV         `json:"urlencoded,omitempty"`
	FormData   []pmKV         `json:"formdata,omitempty"`
	GraphQL    map[string]any `json:"graphql,omitempty"`
	Options    map[string]any `json:"options,omitempty"`
}

type pmRequest struct {
	Method string          `json:"method"`
	Header []pmKV          `json:"header"`
	URL    json.RawMessage `json:"url"`
	Body   *pmBody         `json:"body,omitempty"`
	Auth   *pmAuth         `json:"auth,omitempty"`
}

type pmResponse struct {
	Name   string `json:"name"`
	Code   int    `json:"code"`
	Header []pmKV `json:"header"`
	Body   string `json:"body"`
}

type pmItem struct {
	Name     string          `json:"name"`
	Item     []pmItem        `json:"item,omitempty"`
	Request  json.RawMessage `json:"request,omitempty"`
	Response []pmResponse    `json:"response,omitempty"`
	Event    []pmEvent       `json:"event,omitempty"`
	Auth     *pmAuth         `json:"auth,omitempty"`
}

type pmCollection struct {
	Info struct {
		Name        string `json:"name"`
		Description any    `json:"description"`
		Schema      string `json:"schema"`
	} `json:"info"`
	Item     []pmItem  `json:"item"`
	Variable []pmKV    `json:"variable,omitempty"`
	Auth     *pmAuth   `json:"auth,omitempty"`
	Event    []pmEvent `json:"event,omitempty"`
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case map[string]any: // {"content": "...", "type": "text/markdown"}
		return str(x["content"])
	case []any:
		var parts []string
		for _, p := range x {
			parts = append(parts, str(p))
		}
		return strings.Join(parts, "\n")
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func kvs(in []pmKV) []runner.KV {
	out := []runner.KV{}
	for _, k := range in {
		out = append(out, runner.KV{Key: k.Key, Value: str(k.Value), Description: str(k.Description), Enabled: !k.Disabled})
	}
	return out
}

func authField(kv []pmKV, key string) string {
	for _, k := range kv {
		if k.Key == key {
			return str(k.Value)
		}
	}
	return ""
}

func fromPMAuth(a *pmAuth) runner.Auth {
	if a == nil {
		return runner.Auth{Type: "inherit"}
	}
	switch a.Type {
	case "noauth":
		return runner.Auth{Type: "none"}
	case "bearer":
		return runner.Auth{Type: "bearer", Token: authField(a.Bearer, "token")}
	case "basic":
		return runner.Auth{Type: "basic", Username: authField(a.Basic, "username"), Password: authField(a.Basic, "password")}
	case "apikey":
		in := authField(a.APIKey, "in")
		if in != "query" {
			in = "header"
		}
		return runner.Auth{Type: "apikey", Key: authField(a.APIKey, "key"), Value: authField(a.APIKey, "value"), In: in}
	case "oauth2":
		grant := authField(a.OAuth2, "grant_type")
		switch grant {
		case "client_credentials", "password":
		case "password_credentials":
			grant = "password"
		default:
			grant = "manual"
		}
		auth := runner.Auth{
			Type: "oauth2", GrantType: grant, Token: authField(a.OAuth2, "accessToken"),
			TokenURL: authField(a.OAuth2, "accessTokenUrl"), ClientID: authField(a.OAuth2, "clientId"),
			ClientSecret: authField(a.OAuth2, "clientSecret"), Scope: authField(a.OAuth2, "scope"),
			Username: authField(a.OAuth2, "username"), Password: authField(a.OAuth2, "password"),
		}
		if authField(a.OAuth2, "client_authentication") == "body" {
			auth.ClientAuth = "body"
		}
		return auth
	case "awsv4":
		return runner.Auth{Type: "awsv4", AccessKey: authField(a.AWSv4, "accessKey"), SecretKey: authField(a.AWSv4, "secretKey"),
			SessionToken: authField(a.AWSv4, "sessionToken"), Region: authField(a.AWSv4, "region"), Service: authField(a.AWSv4, "service")}
	case "digest":
		return runner.Auth{Type: "digest", Username: authField(a.Digest, "username"), Password: authField(a.Digest, "password")}
	case "jwt":
		return runner.Auth{Type: "jwt", Algorithm: authField(a.JWT, "algorithm"), Secret: authField(a.JWT, "secret"),
			Payload: authField(a.JWT, "payload"), HeaderPrefix: authField(a.JWT, "headerPrefix")}
	}
	return runner.Auth{Type: "inherit"}
}

func scripts(events []pmEvent) (pre, post string) {
	for _, e := range events {
		code := str(e.Script.Exec)
		switch e.Listen {
		case "prerequest":
			pre = code
		case "test":
			post = code
		}
	}
	return
}

// ParsePostman reads a Postman Collection (v2.0 or v2.1) export.
func ParsePostman(data []byte) (*Collection, error) {
	var pc pmCollection
	if err := json.Unmarshal(data, &pc); err != nil {
		return nil, fmt.Errorf("not a Postman collection: %w", err)
	}
	if pc.Info.Name == "" && len(pc.Item) == 0 {
		return nil, errors.New("not a Postman collection (no info.name or item)")
	}
	if pc.Info.Schema != "" && !strings.Contains(pc.Info.Schema, "v2.") {
		return nil, errors.New("only Postman Collection v2.0 and v2.1 are supported; export again as v2.1")
	}
	c := &Collection{Name: pc.Info.Name, Description: str(pc.Info.Description), Variables: kvs(pc.Variable), Auth: fromPMAuth(pc.Auth), Requests: []Request{}}
	if c.Name == "" {
		c.Name = "Imported collection"
	}
	c.PreScript, c.PostScript = scripts(pc.Event)
	var walk func(items []pmItem, folder string, inherited *pmAuth)
	walk = func(items []pmItem, folder string, inherited *pmAuth) {
		for _, it := range items {
			if len(it.Request) == 0 {
				sub := it.Name
				if folder != "" {
					sub = folder + " / " + it.Name
				}
				a := inherited
				if it.Auth != nil {
					a = it.Auth
				}
				walk(it.Item, sub, a)
				continue
			}
			r := Request{Folder: folder, Name: it.Name, Params: []runner.KV{}, Headers: []runner.KV{}, FormData: []runner.FormField{}, Examples: []Example{}}
			var pr pmRequest
			var rawURL string
			if json.Unmarshal(it.Request, &rawURL) == nil { // "request": "https://…"
				pr.Method, pr.URL = "GET", it.Request
			} else if err := json.Unmarshal(it.Request, &pr); err != nil {
				continue
			}
			r.Method = strings.ToUpper(pr.Method)
			if r.Method == "" {
				r.Method = "GET"
			}
			var u pmURL
			if json.Unmarshal(pr.URL, &rawURL) == nil {
				u.Raw = rawURL
			} else {
				_ = json.Unmarshal(pr.URL, &u)
			}
			// Query parameters live in the params table, not the URL.
			r.URL = u.Raw
			if i := strings.Index(r.URL, "?"); i >= 0 {
				r.URL = r.URL[:i]
			}
			if len(u.Query) > 0 {
				r.Params = kvs(u.Query)
			} else if i := strings.Index(u.Raw, "?"); i >= 0 {
				for _, pair := range strings.Split(u.Raw[i+1:], "&") {
					k, v, _ := strings.Cut(pair, "=")
					r.Params = append(r.Params, runner.KV{Key: k, Value: v, Enabled: true})
				}
			}
			r.Headers = kvs(pr.Header)
			r.BodyMode = "none"
			if b := pr.Body; b != nil {
				switch b.Mode {
				case "raw":
					r.Body = b.Raw
					r.BodyMode = "raw"
					lang, _ := b.Options["raw"].(map[string]any)
					if lang != nil && lang["language"] == "json" || strings.HasPrefix(strings.TrimSpace(b.Raw), "{") || strings.HasPrefix(strings.TrimSpace(b.Raw), "[") {
						r.BodyMode = "json"
					}
				case "urlencoded":
					var parts []string
					for _, k := range b.URLEncoded {
						if !k.Disabled {
							parts = append(parts, url.QueryEscape(k.Key)+"="+url.QueryEscape(str(k.Value)))
						}
					}
					r.BodyMode, r.Body = "form-urlencoded", strings.Join(parts, "&")
				case "formdata":
					r.BodyMode = "form-data"
					for _, k := range b.FormData {
						f := runner.FormField{Key: k.Key, Value: str(k.Value), Enabled: !k.Disabled, Type: "text"}
						if k.Type == "file" {
							// File contents aren't in the export; pick the file again after import.
							f.Type, f.Value, f.FileName = "file", "", str(k.Src)
						}
						r.FormData = append(r.FormData, f)
					}
				case "graphql":
					b2, _ := json.MarshalIndent(map[string]any{"query": b.GraphQL["query"], "variables": parseMaybeJSON(b.GraphQL["variables"])}, "", "  ")
					r.BodyMode, r.Body = "graphql", string(b2)
				}
			}
			a := pr.Auth
			if a == nil {
				a = it.Auth
			}
			if a == nil && inherited != pc.Auth {
				a = inherited // a folder's auth applies to its requests
			}
			r.Auth = fromPMAuth(a)
			r.PreScript, r.PostScript = scripts(it.Event)
			for _, ex := range it.Response {
				r.Examples = append(r.Examples, Example{Name: ex.Name, Status: ex.Code, Headers: kvs(ex.Header), Body: ex.Body})
			}
			if r.Name == "" {
				r.Name = r.Method + " " + r.URL
			}
			c.Requests = append(c.Requests, r)
		}
	}
	walk(pc.Item, "", pc.Auth)
	return c, nil
}

func parseMaybeJSON(v any) any {
	if s, ok := v.(string); ok {
		var out any
		if json.Unmarshal([]byte(s), &out) == nil {
			return out
		}
		return s
	}
	return v
}

func toPMAuth(a runner.Auth) *pmAuth {
	kv := func(pairs ...string) []pmKV {
		var out []pmKV
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, pmKV{Key: pairs[i], Value: pairs[i+1], Type: "string"})
		}
		return out
	}
	switch a.Type {
	case "none":
		return &pmAuth{Type: "noauth"}
	case "bearer":
		return &pmAuth{Type: "bearer", Bearer: kv("token", a.Token)}
	case "basic":
		return &pmAuth{Type: "basic", Basic: kv("username", a.Username, "password", a.Password)}
	case "apikey":
		return &pmAuth{Type: "apikey", APIKey: kv("key", a.Key, "value", a.Value, "in", a.In)}
	case "oauth2":
		grant := a.GrantType
		if grant == "password" {
			grant = "password_credentials"
		}
		return &pmAuth{Type: "oauth2", OAuth2: kv("grant_type", grant, "accessToken", a.Token, "accessTokenUrl", a.TokenURL, "clientId", a.ClientID,
			"clientSecret", a.ClientSecret, "scope", a.Scope, "username", a.Username, "password", a.Password)}
	case "awsv4":
		return &pmAuth{Type: "awsv4", AWSv4: kv("accessKey", a.AccessKey, "secretKey", a.SecretKey, "sessionToken", a.SessionToken, "region", a.Region, "service", a.Service)}
	case "digest":
		return &pmAuth{Type: "digest", Digest: kv("username", a.Username, "password", a.Password)}
	case "jwt":
		return &pmAuth{Type: "jwt", JWT: kv("algorithm", a.Algorithm, "secret", a.Secret, "payload", a.Payload, "headerPrefix", a.HeaderPrefix)}
	}
	return nil
}

func toPMKV(in []runner.KV) []pmKV {
	out := []pmKV{}
	for _, k := range in {
		out = append(out, pmKV{Key: k.Key, Value: k.Value, Description: k.Description, Disabled: !k.Enabled})
	}
	return out
}

func toPMEvents(pre, post string) []pmEvent {
	var out []pmEvent
	add := func(listen, code string) {
		if strings.TrimSpace(code) == "" {
			return
		}
		e := pmEvent{Listen: listen}
		e.Script.Exec = strings.Split(code, "\n")
		e.Script.Type = "text/javascript"
		out = append(out, e)
	}
	add("prerequest", pre)
	add("test", post)
	return out
}

// ExportPostman writes c as a Postman Collection v2.1.
func ExportPostman(c Collection) ([]byte, error) {
	var pc pmCollection
	pc.Info.Name = c.Name
	pc.Info.Description = c.Description
	pc.Info.Schema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
	pc.Variable = toPMKV(c.Variables)
	pc.Auth = toPMAuth(c.Auth)
	pc.Event = toPMEvents(c.PreScript, c.PostScript)
	pc.Item = []pmItem{}
	// Folders "A / B" become nested folders.
	type node struct {
		item     *pmItem
		children map[string]*node
		order    []string
	}
	root := &node{item: &pmItem{}, children: map[string]*node{}}
	folderNode := func(path string) *node {
		n := root
		if path == "" {
			return n
		}
		for _, part := range strings.Split(path, " / ") {
			child, ok := n.children[part]
			if !ok {
				child = &node{item: &pmItem{Name: part}, children: map[string]*node{}}
				n.children[part] = child
				n.order = append(n.order, "f:"+part)
			}
			n = child
		}
		return n
	}
	for i, r := range c.Requests {
		req := pmRequest{Method: r.Method, Header: toPMKV(r.Headers), Auth: toPMAuth(r.Auth)}
		raw := r.URL
		var q []string
		for _, p := range r.Params {
			if p.Enabled {
				q = append(q, p.Key+"="+p.Value)
			}
		}
		if len(q) > 0 {
			raw += "?" + strings.Join(q, "&")
		}
		u, _ := json.Marshal(pmURL{Raw: raw, Query: toPMKV(r.Params)})
		req.URL = u
		switch r.BodyMode {
		case "json", "raw":
			req.Body = &pmBody{Mode: "raw", Raw: r.Body}
			if r.BodyMode == "json" {
				req.Body.Options = map[string]any{"raw": map[string]any{"language": "json"}}
			}
		case "form-urlencoded":
			vals, _ := url.ParseQuery(r.Body)
			keys := make([]string, 0, len(vals))
			for k := range vals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var enc []pmKV
			for _, k := range keys {
				for _, v := range vals[k] {
					enc = append(enc, pmKV{Key: k, Value: v})
				}
			}
			req.Body = &pmBody{Mode: "urlencoded", URLEncoded: enc}
		case "form-data":
			var fd []pmKV
			for _, f := range r.FormData {
				if f.Type == "file" {
					fd = append(fd, pmKV{Key: f.Key, Type: "file", Src: f.FileName, Disabled: !f.Enabled})
				} else {
					fd = append(fd, pmKV{Key: f.Key, Value: f.Value, Type: "text", Disabled: !f.Enabled})
				}
			}
			req.Body = &pmBody{Mode: "formdata", FormData: fd}
		case "graphql":
			var g map[string]any
			_ = json.Unmarshal([]byte(r.Body), &g)
			vars, _ := json.Marshal(g["variables"])
			req.Body = &pmBody{Mode: "graphql", GraphQL: map[string]any{"query": g["query"], "variables": string(vars)}}
		}
		rb, _ := json.Marshal(req)
		it := pmItem{Name: r.Name, Request: rb, Event: toPMEvents(r.PreScript, r.PostScript)}
		for _, ex := range r.Examples {
			it.Response = append(it.Response, pmResponse{Name: ex.Name, Code: ex.Status, Header: toPMKV(ex.Headers), Body: ex.Body})
		}
		n := folderNode(r.Folder)
		key := fmt.Sprintf("r:%d", i)
		n.children[key] = &node{item: &it}
		n.order = append(n.order, key)
	}
	var build func(n *node) []pmItem
	build = func(n *node) []pmItem {
		out := []pmItem{}
		for _, k := range n.order {
			if name, ok := strings.CutPrefix(k, "f:"); ok {
				child := n.children[name]
				child.item.Item = build(child)
				out = append(out, *child.item)
			} else {
				out = append(out, *n.children[k].item)
			}
		}
		return out
	}
	pc.Item = build(root)
	return json.MarshalIndent(pc, "", "  ")
}
