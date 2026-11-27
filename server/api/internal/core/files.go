package core

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/anilcharles/requara/server/api/internal/runner"
	"gopkg.in/yaml.v3"
)

// A collection as a folder of plain YAML files, for keeping in Git next to
// the code and reviewing in pull requests:
//
//	orders-api/
//	  collection.yaml          name, description, variables, auth, scripts
//	  001-list-orders.yaml     a request outside any folder
//	  payments/
//	    001-create-charge.yaml one request per file; the number keeps the order
//
// Fields are written in a fixed order and empty ones are left out, so a
// change to one request is a small diff in one file. ExportFiles zips the
// folder; ParseFiles reads such a zip back.

const filesVersion = 1

type fileCollection struct {
	Requara     int       `yaml:"requara"` // format version
	Name        string    `yaml:"name"`
	Description string    `yaml:"description,omitempty"`
	Variables   []fileKV  `yaml:"variables,omitempty"`
	Auth        *fileAuth `yaml:"auth,omitempty"`
	PreScript   string    `yaml:"preRequestScript,omitempty"`
	PostScript  string    `yaml:"testScript,omitempty"`
	Folders     []string  `yaml:"folders,omitempty"` // folder names in order (directories are slugs)
}

type fileRequest struct {
	Name       string        `yaml:"name"`
	Folder     string        `yaml:"folder,omitempty"`
	Method     string        `yaml:"method"`
	URL        string        `yaml:"url"`
	Params     []fileKV      `yaml:"params,omitempty"`
	Headers    []fileKV      `yaml:"headers,omitempty"`
	Auth       *fileAuth     `yaml:"auth,omitempty"`
	Body       *fileBody     `yaml:"body,omitempty"`
	PreScript  string        `yaml:"preRequestScript,omitempty"`
	PostScript string        `yaml:"testScript,omitempty"`
	Examples   []fileExample `yaml:"examples,omitempty"`
}

type fileKV struct {
	Key         string `yaml:"key"`
	Value       string `yaml:"value"`
	Description string `yaml:"description,omitempty"`
	Disabled    bool   `yaml:"disabled,omitempty"`
}

type fileBody struct {
	Mode     string     `yaml:"mode"`
	Content  string     `yaml:"content,omitempty"`
	FormData []fileForm `yaml:"formData,omitempty"`
}

type fileForm struct {
	Key         string `yaml:"key"`
	Value       string `yaml:"value,omitempty"`
	Type        string `yaml:"type,omitempty"` // text (default) | file
	FileName    string `yaml:"fileName,omitempty"`
	ContentType string `yaml:"contentType,omitempty"`
	Disabled    bool   `yaml:"disabled,omitempty"`
}

type fileExample struct {
	Name    string   `yaml:"name"`
	Status  int      `yaml:"status"`
	Headers []fileKV `yaml:"headers,omitempty"`
	Body    string   `yaml:"body,omitempty"`
}

// fileAuth is runner.Auth with its empty fields left out, and without the
// tokens Requara got by signing in (they belong to a person, not the repo).
type fileAuth struct {
	runner.Auth `yaml:",inline"`
}

func (a *fileAuth) MarshalYAML() (any, error) {
	x := a.Auth
	x.RefreshToken, x.ExpiresAt = "", 0
	if x.GrantType == "authorization_code" {
		x.Token = ""
	}
	// Through JSON field names, dropping empty ones.
	m := yaml.Node{Kind: yaml.MappingNode}
	add := func(k, v string) {
		if v != "" {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
		}
	}
	add("type", x.Type)
	add("token", x.Token)
	add("username", x.Username)
	add("password", x.Password)
	add("key", x.Key)
	add("value", x.Value)
	add("in", x.In)
	add("grantType", x.GrantType)
	add("authUrl", x.AuthURL)
	add("tokenUrl", x.TokenURL)
	add("clientId", x.ClientID)
	add("clientSecret", x.ClientSecret)
	add("scope", x.Scope)
	add("clientAuth", x.ClientAuth)
	add("pkce", x.PKCE)
	add("audience", x.Audience)
	add("accessKey", x.AccessKey)
	add("secretKey", x.SecretKey)
	add("sessionToken", x.SessionToken)
	add("region", x.Region)
	add("service", x.Service)
	add("algorithm", x.Algorithm)
	add("secret", x.Secret)
	add("payload", x.Payload)
	add("headerPrefix", x.HeaderPrefix)
	return &m, nil
}

func (a *fileAuth) UnmarshalYAML(n *yaml.Node) error {
	var m map[string]string
	if err := n.Decode(&m); err != nil {
		return err
	}
	x := &a.Auth
	for k, v := range m {
		switch k {
		case "type":
			x.Type = v
		case "token":
			x.Token = v
		case "username":
			x.Username = v
		case "password":
			x.Password = v
		case "key":
			x.Key = v
		case "value":
			x.Value = v
		case "in":
			x.In = v
		case "grantType":
			x.GrantType = v
		case "authUrl":
			x.AuthURL = v
		case "tokenUrl":
			x.TokenURL = v
		case "clientId":
			x.ClientID = v
		case "clientSecret":
			x.ClientSecret = v
		case "scope":
			x.Scope = v
		case "clientAuth":
			x.ClientAuth = v
		case "pkce":
			x.PKCE = v
		case "audience":
			x.Audience = v
		case "accessKey":
			x.AccessKey = v
		case "secretKey":
			x.SecretKey = v
		case "sessionToken":
			x.SessionToken = v
		case "region":
			x.Region = v
		case "service":
			x.Service = v
		case "algorithm":
			x.Algorithm = v
		case "secret":
			x.Secret = v
		case "payload":
			x.Payload = v
		case "headerPrefix":
			x.HeaderPrefix = v
		}
	}
	return nil
}

func toFileKVs(in []runner.KV) []fileKV {
	var out []fileKV
	for _, kv := range in {
		if kv.Key == "" && kv.Value == "" {
			continue
		}
		out = append(out, fileKV{Key: kv.Key, Value: kv.Value, Description: kv.Description, Disabled: !kv.Enabled})
	}
	return out
}

func fromFileKVs(in []fileKV) []runner.KV {
	out := []runner.KV{}
	for _, kv := range in {
		out = append(out, runner.KV{Key: kv.Key, Value: kv.Value, Description: kv.Description, Enabled: !kv.Disabled})
	}
	return out
}

func authOrNil(a runner.Auth) *fileAuth {
	if a.Type == "" || a.Type == "inherit" {
		return nil
	}
	return &fileAuth{a}
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is a file-name-safe version of a name.
func Slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

// ExportFiles writes c as a zip of YAML files under a folder named after it.
func ExportFiles(c Collection) ([]byte, error) {
	root := Slug(c.Name)
	fc := fileCollection{Requara: filesVersion, Name: c.Name, Description: c.Description, Variables: toFileKVs(c.Variables),
		Auth: authOrNil(c.Auth), PreScript: c.PreScript, PostScript: c.PostScript}
	dirs := map[string]string{} // folder name -> directory
	used := map[string]bool{}
	count := map[string]int{}
	type file struct {
		name string
		data []byte
	}
	var files []file
	for _, r := range c.Requests {
		dir := ""
		if r.Folder != "" {
			d, ok := dirs[r.Folder]
			if !ok {
				d = Slug(r.Folder)
				for i := 2; used[d]; i++ {
					d = fmt.Sprintf("%s-%d", Slug(r.Folder), i)
				}
				dirs[r.Folder], used[d] = d, true
				fc.Folders = append(fc.Folders, r.Folder)
			}
			dir = d
		}
		count[dir]++
		fr := fileRequest{Name: r.Name, Folder: r.Folder, Method: strings.ToUpper(r.Method), URL: r.URL, Params: toFileKVs(r.Params), Headers: toFileKVs(r.Headers),
			Auth: authOrNil(r.Auth), PreScript: r.PreScript, PostScript: r.PostScript}
		if r.BodyMode != "" && r.BodyMode != "none" {
			b := &fileBody{Mode: r.BodyMode, Content: r.Body}
			if r.BodyMode == "form-data" {
				b.Content = ""
				for _, f := range r.FormData {
					ff := fileForm{Key: f.Key, Value: f.Value, Type: f.Type, FileName: f.FileName, ContentType: f.ContentType, Disabled: !f.Enabled}
					if ff.Type == "file" {
						ff.Value = "" // file contents stay in Requara
					}
					if ff.Type == "text" {
						ff.Type = ""
					}
					b.FormData = append(b.FormData, ff)
				}
			}
			fr.Body = b
		}
		for _, e := range r.Examples {
			fr.Examples = append(fr.Examples, fileExample{Name: e.Name, Status: e.Status, Headers: toFileKVs(e.Headers), Body: e.Body})
		}
		data, err := marshalYAML(fr)
		if err != nil {
			return nil, err
		}
		files = append(files, file{path.Join(root, dir, fmt.Sprintf("%03d-%s.yaml", count[dir], Slug(r.Name))), data})
	}
	head, err := marshalYAML(fc)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	write := func(name string, data []byte) error {
		w, err := z.Create(name)
		if err == nil {
			_, err = w.Write(data)
		}
		return err
	}
	if err := write(path.Join(root, "collection.yaml"), head); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := write(f.name, f.data); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	e := yaml.NewEncoder(&buf)
	e.SetIndent(2)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), e.Close()
}

const maxFilesZip = 20 << 20

// ParseFiles reads a zip written by ExportFiles (possibly re-zipped from a
// Git checkout): collection.yaml and the request files under it.
func ParseFiles(data []byte) (*Collection, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("that isn't a zip file")
	}
	read := func(f *zip.File) ([]byte, error) {
		if f.UncompressedSize64 > maxFilesZip {
			return nil, fmt.Errorf("%s is too large", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, maxFilesZip))
	}
	// The shallowest collection.yaml is the collection's root.
	var head *zip.File
	for _, f := range z.File {
		if path.Base(f.Name) == "collection.yaml" && (head == nil || strings.Count(f.Name, "/") < strings.Count(head.Name, "/")) {
			head = f
		}
	}
	if head == nil {
		return nil, errors.New("no collection.yaml in the zip: export a collection as files from Requara first")
	}
	root := path.Dir(head.Name)
	b, err := read(head)
	if err != nil {
		return nil, err
	}
	var fc fileCollection
	if err := yaml.Unmarshal(b, &fc); err != nil {
		return nil, fmt.Errorf("collection.yaml: %w", err)
	}
	c := &Collection{Name: fc.Name, Description: fc.Description, Variables: fromFileKVs(fc.Variables), Auth: runner.Auth{Type: "inherit"},
		PreScript: fc.PreScript, PostScript: fc.PostScript, Requests: []Request{}}
	if fc.Auth != nil {
		c.Auth = fc.Auth.Auth
	}
	if c.Name == "" {
		c.Name = "Imported collection"
	}
	type entry struct {
		dir, name string
		r         Request
	}
	var entries []entry
	for _, f := range z.File {
		if f.FileInfo().IsDir() || f == head || !strings.HasSuffix(f.Name, ".yaml") {
			continue
		}
		rel := f.Name
		if root != "." {
			if !strings.HasPrefix(f.Name, root+"/") {
				continue
			}
			rel = strings.TrimPrefix(f.Name, root+"/")
		}
		if strings.Count(rel, "/") > 1 || path.Base(rel) == "collection.yaml" {
			continue
		}
		b, err := read(f)
		if err != nil {
			return nil, err
		}
		var fr fileRequest
		if err := yaml.Unmarshal(b, &fr); err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		r := Request{Folder: fr.Folder, Name: fr.Name, Method: strings.ToUpper(fr.Method), URL: fr.URL, Params: fromFileKVs(fr.Params), Headers: fromFileKVs(fr.Headers),
			BodyMode: "none", Auth: runner.Auth{Type: "inherit"}, PreScript: fr.PreScript, PostScript: fr.PostScript, FormData: []runner.FormField{}, Examples: []Example{}}
		if r.Method == "" {
			r.Method = "GET"
		}
		if r.Name == "" {
			r.Name = strings.TrimSuffix(path.Base(rel), ".yaml")
		}
		if fr.Auth != nil {
			r.Auth = fr.Auth.Auth
		}
		if fr.Body != nil {
			r.BodyMode, r.Body = fr.Body.Mode, fr.Body.Content
			for _, ff := range fr.Body.FormData {
				t := ff.Type
				if t == "" {
					t = "text"
				}
				r.FormData = append(r.FormData, runner.FormField{Key: ff.Key, Value: ff.Value, Type: t, FileName: ff.FileName, ContentType: ff.ContentType, Enabled: !ff.Disabled})
			}
		}
		for _, e := range fr.Examples {
			r.Examples = append(r.Examples, Example{Name: e.Name, Status: e.Status, Headers: fromFileKVs(e.Headers), Body: e.Body})
		}
		entries = append(entries, entry{path.Dir(rel), path.Base(rel), r})
	}
	// Folders in collection.yaml's order, then any others; files by name.
	rank := map[string]int{"": -1}
	for i, f := range fc.Folders {
		rank[f] = i
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ri, iok := rank[entries[i].r.Folder]
		rj, jok := rank[entries[j].r.Folder]
		if !iok {
			ri = len(rank) + 1
		}
		if !jok {
			rj = len(rank) + 1
		}
		if ri != rj {
			return ri < rj
		}
		if entries[i].r.Folder != entries[j].r.Folder {
			return entries[i].r.Folder < entries[j].r.Folder
		}
		return entries[i].name < entries[j].name
	})
	for _, e := range entries {
		c.Requests = append(c.Requests, e.r)
	}
	return c, nil
}
