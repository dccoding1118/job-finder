package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	t.Parallel()
	path := writeProfile(t, validProfileYAML())
	value, contents, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if value.YearsOfExperience != 8 || contents == "" {
		t.Fatalf("unexpected loaded profile: %+v", value)
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	for name, replacement := range map[string]string{
		"degree": "degree: invalid", "remote": "remote: invalid", "duplicate_skill": "- Go\n  familiar:\n    - Go", "negative_years": "years_of_experience: -1",
	} {
		t.Run(name, func(t *testing.T) {
			contents := validProfileYAML()
			switch name {
			case "degree":
				contents = strings.Replace(contents, "degree: master", replacement, 1)
			case "remote":
				contents = strings.Replace(contents, "remote: preferred", replacement, 1)
			case "negative_years":
				contents = strings.Replace(contents, "years_of_experience: 8", replacement, 1)
			case "duplicate_skill":
				contents = strings.Replace(contents, "proficient: [Go]\n  familiar:", "proficient: [Go]\n  familiar: [Go]\n#", 1)
			}
			if _, _, err := Load(writeProfile(t, contents)); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestValidateRejectsBlankScreeningTerm(t *testing.T) {
	t.Parallel()
	contents := strings.Replace(validProfileYAML(), "  industry_avoid: []", "  industry_avoid: []\n  screening:\n    exclude_companies: [\"  \"]", 1)
	if _, _, err := Load(writeProfile(t, contents)); err == nil {
		t.Fatal("Load accepted a blank screening term")
	}
}

func TestLintTextFindsDenylistAndPatterns(t *testing.T) {
	t.Parallel()
	clean := "anonymous engineering profile"
	if err := LintText(clean, []string{"blocked-word"}); err != nil {
		t.Fatal(err)
	}
	email := strings.Join([]string{"mailbox", "@", "example", ".", "invalid"}, "")
	mobile := strings.Join([]string{"09", "12", "345", "678"}, "")
	text := "BLOCKED-WORD\n" + email + "\n" + mobile
	findings := FindPII(text, []string{"blocked-word"})
	if len(findings) < 3 || findings[0].Line != 1 {
		t.Fatalf("findings = %+v", findings)
	}
	if err := LintText(text, []string{"blocked-word"}); err == nil {
		t.Fatal("LintText succeeded")
	}
}

func TestLoadDenylist(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "denylist.txt")
	if err := os.WriteFile(path, []byte("# comment\nAlpha\nalpha\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	words, err := LoadDenylist(path)
	if err != nil || len(words) != 1 || words[0] != "Alpha" {
		t.Fatalf("words=%v err=%v", words, err)
	}
}

func writeProfile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validProfileYAML() string {
	return `summary: anonymous engineering profile
years_of_experience: 8
education:
  degree: master
  field: computer science
experiences:
  - role: backend engineer
    org_type: technology provider
    years: 4
    summary: service delivery
    achievements: [reliable delivery]
    skills: [Go]
skills:
  expert: [Java]
  proficient: [Go]
  familiar: [Kubernetes]
certifications: []
preferences:
  salary_min: 0
  salary_target: 0
  locations: [Taipei]
  remote: preferred
  directions:
    - key: P1
      title: cloud architecture
      keywords: [cloud]
  industry_avoid: []
honesty_bounds: [configuration focused]
`
}
