package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileLintAndShow(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	profilePath := filepath.Join(directory, "profile.yaml")
	denylistPath := filepath.Join(directory, "denylist.txt")
	if err := os.WriteFile(profilePath, []byte(testProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(denylistPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"profile", "lint"}, {"profile", "show"}} {
		cmd := newRootCmd()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs(append(arguments, "--profile", profilePath, "--denylist", denylistPath))
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if output.Len() == 0 {
			t.Fatalf("no output for %v", arguments)
		}
	}
}

func TestProfileLintRejectsDenylistMatch(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	profilePath := filepath.Join(directory, "profile.yaml")
	denylistPath := filepath.Join(directory, "denylist.txt")
	if err := os.WriteFile(profilePath, []byte(testProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(denylistPath, []byte("ANONYMOUS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd()
	cmd.SetArgs([]string{"profile", "lint", "--profile", profilePath, "--denylist", denylistPath})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "PII detected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultScoringWeights(t *testing.T) {
	want := [5]float64{.30, .15, .15, .20, .20}
	if got := defaultScoringWeights(); got != want {
		t.Fatalf("defaultScoringWeights() = %v, want %v", got, want)
	}
}

const testProfileYAML = `summary: anonymous engineering profile
years_of_experience: 8
education: {degree: master, field: computer science}
experiences:
  - role: backend engineer
    org_type: technology provider
    years: 4
    summary: service delivery
    achievements: [reliable delivery]
    skills: [Go]
skills: {expert: [Java], proficient: [Go], familiar: [Kubernetes]}
certifications: []
preferences:
  salary_min: 0
  salary_target: 0
  locations: [Taipei]
  remote: preferred
  directions: [{key: P1, title: cloud architecture, keywords: [cloud]}]
  industry_avoid: []
honesty_bounds: [configuration focused]
`
