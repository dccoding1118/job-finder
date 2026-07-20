// Package profile loads and validates the anonymous profile used by agents.
package profile

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	emailPattern    = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	mobilePattern   = regexp.MustCompile(`(?:\+886[- ]?)?09\d{2}[- ]?\d{3}[- ]?\d{3}`)
	landlinePattern = regexp.MustCompile(`(?:\+886[- ]?|0)\d{1,2}[- ]?\d{3,4}[- ]?\d{4}`)
	idPattern       = regexp.MustCompile(`(?i)\b[A-Z][12]\d{8}\b`)
)

type Profile struct {
	Summary           string          `yaml:"summary"`
	YearsOfExperience int             `yaml:"years_of_experience"`
	Education         Education       `yaml:"education"`
	Experiences       []Experience    `yaml:"experiences"`
	Skills            Skills          `yaml:"skills"`
	Certifications    []Certification `yaml:"certifications"`
	Preferences       Preferences     `yaml:"preferences"`
	HonestyBounds     []string        `yaml:"honesty_bounds"`
}

type Education struct {
	Degree string `yaml:"degree"`
	Field  string `yaml:"field"`
}

type Experience struct {
	Role         string   `yaml:"role"`
	OrgType      string   `yaml:"org_type"`
	Years        float64  `yaml:"years"`
	Summary      string   `yaml:"summary"`
	Achievements []string `yaml:"achievements"`
	Skills       []string `yaml:"skills"`
}

type Skills struct {
	Expert     []string `yaml:"expert"`
	Proficient []string `yaml:"proficient"`
	Familiar   []string `yaml:"familiar"`
}

type Certification struct {
	Name   string `yaml:"name"`
	Status string `yaml:"status"`
}

type Preferences struct {
	SalaryMin     int         `yaml:"salary_min"`
	SalaryTarget  int         `yaml:"salary_target"`
	Locations     []string    `yaml:"locations"`
	Remote        string      `yaml:"remote"`
	Directions    []Direction `yaml:"directions"`
	IndustryAvoid []string    `yaml:"industry_avoid"`
	Screening     Screening   `yaml:"screening"`
}

// Screening contains the deterministic job rejection rules owned by a Profile.
type Screening struct {
	ExcludeTitleKeywords       []string `yaml:"exclude_title_keywords"`
	ExcludeDescriptionKeywords []string `yaml:"exclude_description_keywords"`
	RequireAnyKeywords         []string `yaml:"require_any_keywords"`
	ExcludeCompanies           []string `yaml:"exclude_companies"`
}

type Direction struct {
	Key      string   `yaml:"key"`
	Title    string   `yaml:"title"`
	Keywords []string `yaml:"keywords"`
}

type Finding struct {
	Kind   string
	Line   int
	Column int
}

type Summary struct {
	YearsOfExperience int
	Degree            string
	Field             string
	Expert            []string
	Proficient        []string
	Familiar          []string
	Directions        []Direction
}

func Load(path string) (Profile, string, error) {
	contents, err := os.ReadFile(path) // #nosec G304 -- the CLI explicitly accepts a local profile path.
	if err != nil {
		return Profile{}, "", fmt.Errorf("read profile: %w", err)
	}
	var value Profile
	if err := yaml.Unmarshal(contents, &value); err != nil {
		return Profile{}, "", fmt.Errorf("parse profile YAML: %w", err)
	}
	if err := value.Validate(); err != nil {
		return Profile{}, "", err
	}
	return value, string(contents), nil
}

func (p Profile) Validate() error {
	if strings.TrimSpace(p.Summary) == "" || p.YearsOfExperience < 0 {
		return errors.New("profile: summary is required and years_of_experience must not be negative")
	}
	if !oneOf(p.Education.Degree, "bachelor", "master", "phd") || strings.TrimSpace(p.Education.Field) == "" {
		return errors.New("profile: education.degree must be bachelor, master, or phd and education.field is required")
	}
	if len(p.Experiences) == 0 {
		return errors.New("profile: at least one experience is required")
	}
	for name, values := range map[string][]string{
		"preferences.screening.exclude_title_keywords":       p.Preferences.Screening.ExcludeTitleKeywords,
		"preferences.screening.exclude_description_keywords": p.Preferences.Screening.ExcludeDescriptionKeywords,
		"preferences.screening.require_any_keywords":         p.Preferences.Screening.RequireAnyKeywords,
		"preferences.screening.exclude_companies":            p.Preferences.Screening.ExcludeCompanies,
	} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("profile: %s must not contain an empty value", name)
			}
		}
	}
	for index, experience := range p.Experiences {
		if strings.TrimSpace(experience.Role) == "" || strings.TrimSpace(experience.OrgType) == "" || experience.Years < 0 || strings.TrimSpace(experience.Summary) == "" {
			return fmt.Errorf("profile: experience %d has missing or invalid fields", index)
		}
	}
	if err := validateSkills(p.Skills); err != nil {
		return err
	}
	if p.Preferences.SalaryMin < 0 || p.Preferences.SalaryTarget < 0 || !oneOf(p.Preferences.Remote, "required", "preferred", "ok") || len(p.Preferences.Locations) == 0 {
		return errors.New("profile: preferences has missing or invalid fields")
	}
	if len(p.Preferences.Directions) == 0 {
		return errors.New("profile: at least one preference direction is required")
	}
	for index, direction := range p.Preferences.Directions {
		if strings.TrimSpace(direction.Key) == "" || strings.TrimSpace(direction.Title) == "" || len(direction.Keywords) == 0 {
			return fmt.Errorf("profile: direction %d has missing fields", index)
		}
	}
	if len(p.HonestyBounds) == 0 {
		return errors.New("profile: honesty_bounds is required")
	}
	return nil
}

func (p Profile) SummaryView() Summary {
	return Summary{p.YearsOfExperience, p.Education.Degree, p.Education.Field, p.Skills.Expert, p.Skills.Proficient, p.Skills.Familiar, p.Preferences.Directions}
}

func LoadDenylist(path string) ([]string, error) {
	contents, err := os.ReadFile(path) // #nosec G304 -- the CLI explicitly accepts a local denylist path.
	if err != nil {
		return nil, fmt.Errorf("read denylist: %w", err)
	}
	seen := make(map[string]struct{})
	words := make([]string, 0)
	for _, line := range strings.Split(string(contents), "\n") {
		word := strings.TrimSpace(line)
		if word == "" || strings.HasPrefix(word, "#") {
			continue
		}
		key := strings.ToLower(word)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			words = append(words, word)
		}
	}
	return words, nil
}

func FindPII(text string, denylist []string) []Finding {
	findings := make([]Finding, 0)
	lower := strings.ToLower(text)
	for _, word := range denylist {
		needle := strings.ToLower(word)
		for offset := 0; ; {
			position := strings.Index(lower[offset:], needle)
			if position < 0 {
				break
			}
			position += offset
			findings = append(findings, findingAt(text, position, "denylist"))
			offset = position + len(needle)
		}
	}
	for _, pattern := range []struct {
		kind       string
		expression *regexp.Regexp
	}{{"email", emailPattern}, {"taiwan_mobile", mobilePattern}, {"taiwan_landline", landlinePattern}, {"taiwan_id", idPattern}} {
		for _, match := range pattern.expression.FindAllStringIndex(text, -1) {
			findings = append(findings, findingAt(text, match[0], pattern.kind))
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Line < findings[j].Line || findings[i].Line == findings[j].Line && findings[i].Column < findings[j].Column
	})
	return findings
}

func LintText(text string, denylist []string) error {
	findings := FindPII(text, denylist)
	if len(findings) == 0 {
		return nil
	}
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, fmt.Sprintf("%s at %d:%d", finding.Kind, finding.Line, finding.Column))
	}
	return fmt.Errorf("PII detected: %s", strings.Join(parts, ", "))
}

func validateSkills(skills Skills) error {
	seen := make(map[string]string)
	for level, values := range map[string][]string{"expert": skills.Expert, "proficient": skills.Proficient, "familiar": skills.Familiar} {
		for _, skill := range values {
			key := strings.ToLower(strings.TrimSpace(skill))
			if key == "" {
				return fmt.Errorf("profile: %s skill must not be empty", level)
			}
			if prior, exists := seen[key]; exists {
				return fmt.Errorf("profile: skill %q appears in both %s and %s", skill, prior, level)
			}
			seen[key] = level
		}
	}
	if len(seen) == 0 {
		return errors.New("profile: at least one skill is required")
	}
	return nil
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func findingAt(text string, offset int, kind string) Finding {
	line, column := 1, 1
	for _, runeValue := range text[:offset] {
		if runeValue == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return Finding{Kind: kind, Line: line, Column: column}
}
