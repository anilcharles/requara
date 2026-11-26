// Command requara-cli runs Requara collections from the command line and CI,
// like Newman or the Postman CLI. It runs them on your organization's
// Requara server with an API key (scopes collections:read and runs:write).
//
//	requara-cli run "Orders API" --env Staging --data users.csv --reporter junit --out results.xml
//
// Settings can also come from REQUARA_URL, REQUARA_API_KEY and REQUARA_WORKSPACE.
// It exits with 1 when a test fails or a request errors, so CI fails too.
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usage = `Run Requara collections from the command line and CI.

Usage:
  requara-cli run <collection> [flags]     run a collection (name or id; "Scratch" for loose requests)
  requara-cli collections [flags]          list the collections you can run

Flags:
  --server URL        your Requara server (env REQUARA_URL)
  --key KEY           an API key with collections:read and runs:write (env REQUARA_API_KEY)
  --workspace ID      the workspace (env REQUARA_WORKSPACE; default: your current one)
  --env NAME          environment name or id
  --data FILE         CSV or JSON file: one iteration per row, columns become variables
  --iterations N      how many times to run (default: one per data row, else 1)
  --delay MS          wait between iterations
  --reporter NAME     cli (default), junit or json
  --out FILE          write the report to FILE instead of standard output
`

type client struct {
	base, key, workspace string
	http                 *http.Client
}

func (c *client) do(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	if c.workspace != "" {
		req.Header.Set("X-Workspace", c.workspace)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e struct{ Error string }
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	return json.Unmarshal(raw, out)
}

type collection struct {
	ID, Kind, Name string
}

type result struct {
	Name       string `json:"name"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error"`
	Tests      []struct {
		Name  string `json:"name"`
		Pass  bool   `json:"pass"`
		Error string `json:"error"`
	} `json:"tests"`
}

type runResponse struct {
	Iterations []struct {
		Iteration int               `json:"iteration"`
		Data      map[string]string `json:"data"`
		Results   []result          `json:"results"`
	} `json:"iterations"`
	Passed     int   `json:"passed"`
	Failed     int   `json:"failed"`
	Errors     int   `json:"errors"`
	DurationMs int64 `json:"durationMs"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "requara-cli:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	server := fs.String("server", os.Getenv("REQUARA_URL"), "")
	key := fs.String("key", os.Getenv("REQUARA_API_KEY"), "")
	workspace := fs.String("workspace", os.Getenv("REQUARA_WORKSPACE"), "")
	env := fs.String("env", "", "")
	dataFile := fs.String("data", "", "")
	iterations := fs.Int("iterations", 0, "")
	delay := fs.Int("delay", 0, "")
	reporter := fs.String("reporter", "cli", "")
	outFile := fs.String("out", "", "")
	// Flags may come before or after the collection name.
	var positional []string
	rest := args[1:]
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if *server == "" || *key == "" {
		return errors.New("set --server and --key (or REQUARA_URL and REQUARA_API_KEY)")
	}
	c := &client{base: strings.TrimRight(*server, "/"), key: *key, workspace: *workspace, http: &http.Client{Timeout: 30 * time.Minute}}

	var cols []collection
	if err := c.do("GET", "/api/collections", nil, &cols); err != nil {
		return err
	}
	switch cmd {
	case "collections":
		for _, col := range cols {
			fmt.Printf("%-38s %-11s %s\n", col.ID, col.Kind, col.Name)
		}
		return nil
	case "run":
	default:
		return fmt.Errorf("unknown command %q (run, collections)", cmd)
	}
	if len(positional) != 1 {
		return errors.New(`name the collection to run: requara-cli run "Orders API"`)
	}
	target := ""
	for _, col := range cols {
		if col.ID == positional[0] || strings.EqualFold(col.Name, positional[0]) {
			target = col.ID
			break
		}
	}
	if target == "" {
		return fmt.Errorf("no collection %q (see requara-cli collections)", positional[0])
	}
	body := map[string]any{"iterations": *iterations, "delayMs": *delay}
	if *env != "" {
		var envs struct {
			Items []struct{ ID, Name string } `json:"items"`
		}
		if err := c.do("GET", "/api/environments", nil, &envs); err != nil {
			return err
		}
		found := ""
		for _, e := range envs.Items {
			if e.ID == *env || strings.EqualFold(e.Name, *env) {
				found = e.ID
			}
		}
		if found == "" {
			return fmt.Errorf("no environment %q", *env)
		}
		body["environmentId"] = found
	}
	if *dataFile != "" {
		rows, err := readData(*dataFile)
		if err != nil {
			return err
		}
		body["data"] = rows
	}
	var res runResponse
	if err := c.do("POST", "/api/collections/"+url.PathEscape(target)+"/run", body, &res); err != nil {
		return err
	}

	var out io.Writer = os.Stdout
	if *outFile != "" {
		f, err := os.Create(*outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	switch *reporter {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	case "junit":
		writeJUnit(out, positional[0], res)
	default:
		writeCLI(out, res)
	}
	if *outFile != "" && *reporter != "cli" {
		writeCLI(os.Stdout, res)
	}
	if res.Failed > 0 || res.Errors > 0 {
		os.Exit(1)
	}
	return nil
}

// readData reads iteration data: CSV with a header row, or a JSON array of objects.
func readData(path string) ([]map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".json") {
		var rows []map[string]any
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("%s: a JSON data file is an array of objects: %w", path, err)
		}
		out := make([]map[string]string, len(rows))
		for i, r := range rows {
			out[i] = map[string]string{}
			for k, v := range r {
				if s, ok := v.(string); ok {
					out[i][k] = s
				} else {
					b, _ := json.Marshal(v)
					out[i][k] = string(b)
				}
			}
		}
		return out, nil
	}
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("%s: needs a header row and at least one data row", path)
	}
	var out []map[string]string
	for _, rec := range records[1:] {
		row := map[string]string{}
		for i, h := range records[0] {
			if i < len(rec) {
				row[strings.TrimSpace(h)] = rec[i]
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func writeCLI(w io.Writer, res runResponse) {
	for _, it := range res.Iterations {
		if len(res.Iterations) > 1 {
			fmt.Fprintf(w, "\nIteration %d\n", it.Iteration)
		}
		for _, r := range it.Results {
			mark := "✓"
			if r.Error != "" || r.Status == 0 {
				mark = "✗"
			}
			for _, t := range r.Tests {
				if !t.Pass {
					mark = "✗"
				}
			}
			fmt.Fprintf(w, "%s %s %s  %d  %dms\n", mark, r.Method, r.Name, r.Status, r.DurationMs)
			if r.Error != "" {
				fmt.Fprintf(w, "    error: %s\n", r.Error)
			}
			for _, t := range r.Tests {
				if t.Pass {
					fmt.Fprintf(w, "    ✓ %s\n", t.Name)
				} else {
					fmt.Fprintf(w, "    ✗ %s: %s\n", t.Name, t.Error)
				}
			}
		}
	}
	fmt.Fprintf(w, "\n%d passed, %d failed, %d errors in %.1fs\n", res.Passed, res.Failed, res.Errors, float64(res.DurationMs)/1000)
}

func writeJUnit(w io.Writer, name string, res runResponse) {
	type failure struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	}
	type testcase struct {
		Name      string   `xml:"name,attr"`
		ClassName string   `xml:"classname,attr"`
		Time      float64  `xml:"time,attr"`
		Failure   *failure `xml:"failure,omitempty"`
		Error     *failure `xml:"error,omitempty"`
	}
	type suite struct {
		XMLName  xml.Name   `xml:"testsuite"`
		Name     string     `xml:"name,attr"`
		Tests    int        `xml:"tests,attr"`
		Failures int        `xml:"failures,attr"`
		Errors   int        `xml:"errors,attr"`
		Time     float64    `xml:"time,attr"`
		Cases    []testcase `xml:"testcase"`
	}
	type suites struct {
		XMLName xml.Name `xml:"testsuites"`
		Suites  []suite  `xml:"testsuite"`
	}
	var all suites
	for _, it := range res.Iterations {
		for _, r := range it.Results {
			s := suite{Name: fmt.Sprintf("%s › %s", name, r.Name), Time: float64(r.DurationMs) / 1000}
			if len(res.Iterations) > 1 {
				s.Name += fmt.Sprintf(" (iteration %d)", it.Iteration)
			}
			if r.Error != "" || r.Status == 0 {
				s.Errors++
				s.Cases = append(s.Cases, testcase{Name: r.Method + " " + r.Name, ClassName: name, Error: &failure{Message: r.Error, Text: r.URL}})
			}
			for _, t := range r.Tests {
				tc := testcase{Name: t.Name, ClassName: r.Name}
				if !t.Pass {
					s.Failures++
					tc.Failure = &failure{Message: t.Error, Text: t.Error}
				}
				s.Cases = append(s.Cases, tc)
			}
			s.Tests = len(s.Cases)
			all.Suites = append(all.Suites, s)
		}
	}
	fmt.Fprint(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(all)
	fmt.Fprintln(w)
}
