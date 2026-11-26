package spec

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"github.com/daveshanley/vacuum/motor"
	"github.com/daveshanley/vacuum/rulesets"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/oasdiff/checker"
	"github.com/oasdiff/oasdiff/diff"
	"github.com/oasdiff/oasdiff/load"
)

// ----- linting (vacuum, Spectral-compatible "recommended" ruleset) -----

type LintIssue struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // error | warn | info | hint
	Message  string `json:"message"`
	Path     string `json:"path"`
}

type LintReport struct {
	Issues []LintIssue `json:"issues"`
	Passed []string    `json:"passed"` // descriptions of rules with no findings
	Rules  int         `json:"rules"`
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func Lint(doc []byte) LintReport {
	rs := rulesets.BuildDefaultRuleSetsWithLogger(quiet).GenerateOpenAPIRecommendedRuleSet()
	result := motor.ApplyRulesToRuleSet(&motor.RuleSetExecution{RuleSet: rs, Spec: doc, SilenceLogs: true, Logger: quiet})
	rep := LintReport{Rules: len(rs.Rules)}
	failed := map[string]bool{}
	seen := map[string]bool{}
	for _, r := range result.Results {
		failed[r.RuleId] = true
		k := r.RuleId + "|" + r.Message
		if seen[k] {
			continue
		}
		seen[k] = true
		rep.Issues = append(rep.Issues, LintIssue{Rule: r.RuleId, Severity: r.RuleSeverity, Message: r.Message, Path: r.Path})
	}
	sev := map[string]int{"error": 0, "warn": 1, "info": 2, "hint": 3}
	sort.SliceStable(rep.Issues, func(i, j int) bool { return sev[rep.Issues[i].Severity] < sev[rep.Issues[j].Severity] })
	for id, rule := range rs.Rules {
		if !failed[id] && rule.Description != "" {
			rep.Passed = append(rep.Passed, rule.Description)
		}
	}
	sort.Strings(rep.Passed)
	return rep
}

// ----- change detection (oasdiff) -----

type Change struct {
	Kind      string `json:"kind"` // added | changed | removed
	Text      string `json:"text"`
	Operation string `json:"operation"`
	Breaking  bool   `json:"breaking"`
}

func Diff(published, draft *openapi3.T) ([]Change, error) {
	if published == nil {
		return nil, nil
	}
	d, src, err := diff.GetWithOperationsSourcesMap(diff.NewConfig(),
		&load.SpecInfo{Url: "published", Spec: published},
		&load.SpecInfo{Url: "draft", Spec: draft})
	if err != nil {
		return nil, fmt.Errorf("diff: %w", err)
	}
	changes := checker.CheckBackwardCompatibilityUntilLevel(checker.NewConfig(checker.GetAllChecks()), d, src, checker.INFO)
	loc := checker.NewDefaultLocalizer()
	var out []Change
	for _, c := range changes {
		if c.GetOperation() == "" && strings.Contains(c.GetId(), "version") {
			continue // "major version did not increase": Speklo suggests the version itself
		}
		text := c.GetUncolorizedText(loc)
		kind := "changed"
		id := c.GetId()
		switch {
		case strings.Contains(id, "added") || strings.HasPrefix(id, "new-"):
			kind = "added"
		case strings.Contains(id, "removed") || strings.Contains(id, "deleted"):
			kind = "removed"
		}
		op := strings.TrimSpace(c.GetOperation() + " " + c.GetPath())
		out = append(out, Change{Kind: kind, Text: text, Operation: op, Breaking: c.IsBreaking()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Breaking && !out[j].Breaking })
	return out, nil
}
