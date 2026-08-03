// Package profile loads and validates the anonymous profile used by agents.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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

// Profile is the schema v6 shape: sections are split by which gate reads them —
// `requirements` decides fitness, `intents` decides recommendation, and `search`
// states the directions a job is looked for under. Locality lives in
// `requirements.locations` alone: it both restricts what counts as fit and is the
// only地區 statement there is.
type Profile struct {
	Search         Search         `json:"search" yaml:"search"`
	Requirements   Requirements   `json:"requirements" yaml:"requirements"`
	Intents        Intents        `json:"intents" yaml:"intents"`
	Experiences    []Experience   `json:"experiences" yaml:"experiences"`
	Qualifications Qualifications `json:"qualifications" yaml:"qualifications"`
	HonestyBounds  []string       `json:"honesty_bounds" yaml:"honesty_bounds"`
	// Derived is materialized by this package on save and rejected on input.
	Derived Derived `json:"derived" yaml:"derived"`
}

type Search struct {
	Directions []Direction `json:"directions" yaml:"directions"`
}

type Direction struct {
	Key      string   `json:"key" yaml:"key"`
	Title    string   `json:"title" yaml:"title"`
	Keywords []string `json:"keywords" yaml:"keywords"`
}

// Requirements are the hard rules: a job that misses one is unfit, not low scoring.
type Requirements struct {
	SalaryMin                  int      `json:"salary_min" yaml:"salary_min"`
	Locations                  []string `json:"locations" yaml:"locations"`
	Remote                     string   `json:"remote" yaml:"remote"`
	EmploymentTypes            []string `json:"employment_types" yaml:"employment_types"`
	IndustryAvoid              []string `json:"industry_avoid" yaml:"industry_avoid"`
	ExcludeTitleKeywords       []string `json:"exclude_title_keywords" yaml:"exclude_title_keywords"`
	ExcludeDescriptionKeywords []string `json:"exclude_description_keywords" yaml:"exclude_description_keywords"`
	ExcludeCompanies           []string `json:"exclude_companies" yaml:"exclude_companies"`
}

// Intents are the soft rules: they move the score, never the fitness verdict.
type Intents struct {
	SalaryTarget      int      `json:"salary_target" yaml:"salary_target"`
	ContentLikes      []string `json:"content_likes" yaml:"content_likes"`
	ContentDislikes   []string `json:"content_dislikes" yaml:"content_dislikes"`
	IndustryInterests []string `json:"industry_interests" yaml:"industry_interests"`
}

// Experience is one job. It serves both gates: the first five fields feed the
// screening totals, the last three are material the Drafter writes from.
type Experience struct {
	Industry          string   `json:"industry" yaml:"industry"`
	Years             float64  `json:"years" yaml:"years"`
	IsManagement      bool     `json:"is_management" yaml:"is_management"`
	ExcludeFromTotals bool     `json:"exclude_from_totals" yaml:"exclude_from_totals"`
	Skills            []string `json:"skills" yaml:"skills"`
	OrgType           string   `json:"org_type" yaml:"org_type"`
	Role              string   `json:"role" yaml:"role"`
	Achievements      []string `json:"achievements" yaml:"achievements"`
}

type Qualifications struct {
	Education      []EducationEntry `json:"education" yaml:"education"`
	Skills         []SkillEntry     `json:"skills" yaml:"skills"`
	Certifications []Certification  `json:"certifications" yaml:"certifications"`
	Languages      []LanguageEntry  `json:"languages" yaml:"languages"`
}

type EducationEntry struct {
	Level  string `json:"level" yaml:"level"`
	Field  string `json:"field" yaml:"field"`
	Status string `json:"status" yaml:"status"`
}

// SkillEntry is the single place a proficiency is stated; experiences list skill
// names only.
type SkillEntry struct {
	Name  string `json:"name" yaml:"name"`
	Level string `json:"level" yaml:"level"`
}

type Certification struct {
	Name   string `json:"name" yaml:"name"`
	Status string `json:"status" yaml:"status"`
}

type LanguageEntry struct {
	Name  string `json:"name" yaml:"name"`
	Level string `json:"level" yaml:"level"`
}

// Derived carries the totals the screening gate compares against, so a year
// count is never stated twice and never computed by an Agent.
type Derived struct {
	TotalYears      float64            `json:"total_years" yaml:"total_years"`
	ManagementYears float64            `json:"management_years" yaml:"management_years"`
	IndustryYears   map[string]float64 `json:"industry_years" yaml:"industry_years"`
}

// Empty reports a Derived that carries no materialized value, which is the only
// shape an input document may have.
func (d Derived) Empty() bool {
	return d.TotalYears == 0 && d.ManagementYears == 0 && len(d.IndustryYears) == 0
}

type Finding struct {
	Kind   string
	Line   int
	Column int
}

// Summary is what `profile show` and the Profile card report: enough to confirm
// which file the system actually read.
type Summary struct {
	TotalYears      float64
	ManagementYears float64
	Education       []EducationEntry
	Skills          []SkillEntry
	ExperienceCount int
	Remote          string
	SalaryMin       int
	Directions      []Direction
}

func Load(path string) (Profile, string, error) {
	contents, err := os.ReadFile(path) // #nosec G304 -- the CLI explicitly accepts a local profile path.
	if err != nil {
		return Profile{}, "", fmt.Errorf("read profile: %w", err)
	}
	value, err := DecodeYAML(contents)
	if err != nil {
		return Profile{}, "", fmt.Errorf("parse profile YAML: %w", err)
	}
	return value, string(contents), nil
}

// DecodeYAML rejects fields outside the published Profile schema. A document
// still written in a pre-v6 shape is migrated before validation, so an existing
// local file keeps working without being re-entered by hand.
func DecodeYAML(contents []byte) (Profile, error) {
	contents, err := migrateSearchLocations(contents)
	if err != nil {
		return Profile{}, err
	}
	contents, err = migrateNationwideLocations(contents)
	if err != nil {
		return Profile{}, err
	}
	if isLegacy(contents) {
		value, err := migrateLegacy(contents)
		if err != nil {
			return Profile{}, err
		}
		value.normalize()
		value.materializeDerived()
		if err := value.Validate(); err != nil {
			return Profile{}, err
		}
		return value, nil
	}
	var value Profile
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&value); err != nil {
		return Profile{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Profile{}, errors.New("profile: multiple YAML documents are not allowed")
		}
		return Profile{}, err
	}
	// `derived` is materialized here, so a stored file may carry it but must agree
	// with the experiences it was computed from.
	value.normalize()
	value.materializeDerived()
	if err := value.Validate(); err != nil {
		return Profile{}, err
	}
	return value, nil
}

const (
	remoteRequired   = "required"
	remotePreferred  = "preferred"
	remoteAcceptable = "acceptable"
	remoteRejected   = "rejected"
)

func (p Profile) Validate() error {
	if err := p.validateSearch(); err != nil {
		return err
	}
	if err := p.validateRequirements(); err != nil {
		return err
	}
	if err := p.validateIntents(); err != nil {
		return err
	}
	if err := p.validateExperiences(); err != nil {
		return err
	}
	if err := p.validateQualifications(); err != nil {
		return err
	}
	return validateTextList("honesty_bounds", p.HonestyBounds, true)
}

func (p Profile) validateSearch() error {
	if len(p.Search.Directions) == 0 {
		return errors.New("profile: at least one search direction is required")
	}
	for index, direction := range p.Search.Directions {
		if strings.TrimSpace(direction.Key) == "" || strings.TrimSpace(direction.Title) == "" || len(direction.Keywords) == 0 {
			return fmt.Errorf("profile: search.directions[%d] has missing fields", index)
		}
		if err := validateTextList(fmt.Sprintf("search.directions[%d].keywords", index), direction.Keywords, true); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) validateRequirements() error {
	r := p.Requirements
	if r.SalaryMin < 0 {
		return errors.New("profile: requirements.salary_min must not be negative")
	}
	if !oneOf(r.Remote, remoteRequired, remotePreferred, remoteAcceptable, remoteRejected) {
		return errors.New("profile: requirements.remote must be required, preferred, acceptable, or rejected")
	}
	for index, value := range r.EmploymentTypes {
		if err := validateTerm(EmploymentTypes, fmt.Sprintf("requirements.employment_types[%d]", index), value); err != nil {
			return err
		}
	}
	for index, value := range r.Locations {
		if err := validateTerm(Locations, fmt.Sprintf("requirements.locations[%d]", index), value); err != nil {
			return err
		}
	}
	for name, values := range map[string][]string{
		"requirements.industry_avoid":               r.IndustryAvoid,
		"requirements.exclude_title_keywords":       r.ExcludeTitleKeywords,
		"requirements.exclude_description_keywords": r.ExcludeDescriptionKeywords,
		"requirements.exclude_companies":            r.ExcludeCompanies,
	} {
		if err := validateTextList(name, values, false); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) validateIntents() error {
	if p.Intents.SalaryTarget < 0 {
		return errors.New("profile: intents.salary_target must not be negative")
	}
	for name, values := range map[string][]string{
		"intents.content_likes":      p.Intents.ContentLikes,
		"intents.content_dislikes":   p.Intents.ContentDislikes,
		"intents.industry_interests": p.Intents.IndustryInterests,
	} {
		if err := validateTextList(name, values, false); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) validateExperiences() error {
	if len(p.Experiences) == 0 {
		return errors.New("profile: at least one experience is required")
	}
	for index, experience := range p.Experiences {
		if strings.TrimSpace(experience.Industry) == "" {
			return fmt.Errorf("profile: experiences[%d].industry is required", index)
		}
		if experience.Years < 0 {
			return fmt.Errorf("profile: experiences[%d].years must not be negative", index)
		}
		for name, values := range map[string][]string{
			fmt.Sprintf("experiences[%d].skills", index):       experience.Skills,
			fmt.Sprintf("experiences[%d].achievements", index): experience.Achievements,
		} {
			if err := validateTextList(name, values, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p Profile) validateQualifications() error {
	q := p.Qualifications
	for index, entry := range q.Education {
		if !oneOf(entry.Level, "bachelor", "master", "phd") {
			return fmt.Errorf("profile: qualifications.education[%d].level must be bachelor, master, or phd", index)
		}
		if strings.TrimSpace(entry.Field) == "" {
			return fmt.Errorf("profile: qualifications.education[%d] has missing fields", index)
		}
		if err := validateTerm(EducationStatuses, fmt.Sprintf("qualifications.education[%d].status", index), entry.Status); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{})
	for index, entry := range q.Skills {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return fmt.Errorf("profile: qualifications.skills[%d].name is required", index)
		}
		if !oneOf(entry.Level, "expert", "proficient", "familiar") {
			return fmt.Errorf("profile: qualifications.skills[%d].level must be expert, proficient, or familiar", index)
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("profile: qualifications.skills contains duplicate skill %q", entry.Name)
		}
		seen[key] = struct{}{}
	}
	if len(q.Skills) == 0 {
		return errors.New("profile: at least one qualifications skill is required")
	}
	for index, entry := range q.Certifications {
		if strings.TrimSpace(entry.Name) == "" {
			return fmt.Errorf("profile: qualifications.certifications[%d] has missing fields", index)
		}
		if err := validateTerm(CertificationStatuses, fmt.Sprintf("qualifications.certifications[%d].status", index), entry.Status); err != nil {
			return err
		}
	}
	for index, entry := range q.Languages {
		if strings.TrimSpace(entry.Name) == "" {
			return fmt.Errorf("profile: qualifications.languages[%d] has missing fields", index)
		}
		if err := validateTerm(LanguageLevels, fmt.Sprintf("qualifications.languages[%d].level", index), entry.Level); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) SummaryView() Summary {
	derived := p.DerivedTotals()
	return Summary{
		TotalYears: derived.TotalYears, ManagementYears: derived.ManagementYears,
		Education: p.Qualifications.Education, Skills: p.Qualifications.Skills,
		ExperienceCount: len(p.Experiences), Remote: p.Requirements.Remote,
		SalaryMin: p.Requirements.SalaryMin, Directions: p.Search.Directions,
	}
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

func validateTextList(name string, values []string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("profile: %s is required", name)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("profile: %s must not contain an empty value", name)
		}
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
