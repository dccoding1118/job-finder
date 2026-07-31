package profile

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DerivedTotals computes the year totals the screening gate compares against.
// The totals exist only here: an Agent is never asked to add years up, and the
// user never states a total that could disagree with the list it came from.
func (p Profile) DerivedTotals() Derived {
	derived := Derived{IndustryYears: map[string]float64{}}
	for _, experience := range p.Experiences {
		if experience.ExcludeFromTotals {
			continue
		}
		derived.TotalYears += experience.Years
		if experience.IsManagement {
			derived.ManagementYears += experience.Years
		}
		derived.IndustryYears[experience.Industry] += experience.Years
	}
	return derived
}

func (p *Profile) materializeDerived() { p.Derived = p.DerivedTotals() }

// normalize rewrites controlled-vocabulary fields onto their canonical keys
// before validation, so an input still written with a display label is accepted.
func (p *Profile) normalize() {
	p.Requirements.EmploymentTypes = EmploymentTypes.NormalizeList(p.Requirements.EmploymentTypes)
	p.Requirements.Locations = Locations.NormalizeList(p.Requirements.Locations)
	for index := range p.Qualifications.Education {
		p.Qualifications.Education[index].Status = EducationStatuses.Normalize(p.Qualifications.Education[index].Status)
	}
	for index := range p.Qualifications.Certifications {
		p.Qualifications.Certifications[index].Status = CertificationStatuses.Normalize(p.Qualifications.Certifications[index].Status)
	}
	for index := range p.Qualifications.Languages {
		p.Qualifications.Languages[index].Level = LanguageLevels.Normalize(p.Qualifications.Languages[index].Level)
	}
}

// IndustryKeys lists the industries the profile has experience in, in the order
// the Filter Agent is asked to map a JD requirement onto.
func (p Profile) IndustryKeys() []string {
	seen := map[string]bool{}
	keys := make([]string, 0, len(p.Experiences))
	for _, experience := range p.Experiences {
		if key := experience.Industry; key != "" && !seen[key] {
			seen[key], keys = true, append(keys, key)
		}
	}
	return keys
}

// SkillNames is the guard whitelist source: every skill the profile states,
// whether on the totals list or on one experience.
func (p Profile) SkillNames() []string {
	seen := map[string]bool{}
	names := make([]string, 0)
	add := func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key != "" && !seen[key] {
			seen[key], names = true, append(names, name)
		}
	}
	for _, skill := range p.Qualifications.Skills {
		add(skill.Name)
	}
	for _, experience := range p.Experiences {
		for _, skill := range experience.Skills {
			add(skill)
		}
	}
	return names
}

// FilterView is what the Filter Agent sees. It carries the facts a hard rule is
// checked against and nothing else — no intents, no achievements.
type FilterView struct {
	Qualifications Qualifications `yaml:"qualifications"`
	IndustryKeys   []string       `yaml:"industry_keys"`
	Derived        Derived        `yaml:"derived"`
}

// ScoreView is what the Scorer sees. The resume narrative is deliberately
// absent: an achievement reads as "good at it, therefore a good fit", which is
// exactly the inference that made work the user no longer wants score high.
type ScoreView struct {
	Intents        Intents         `yaml:"intents"`
	Remote         string          `yaml:"remote"`
	Locations      []string        `yaml:"locations"`
	Skills         []SkillEntry    `yaml:"skills"`
	Certifications []Certification `yaml:"certifications"`
	Languages      []LanguageEntry `yaml:"languages"`
	BonusCondition []string        `yaml:"bonus_conditions,omitempty"`
}

// LetterView is what the Drafter and Reviewer see: the facts a letter may be
// written from, and the bounds it must not exceed.
type LetterView struct {
	Experiences    []Experience   `yaml:"experiences"`
	Qualifications Qualifications `yaml:"qualifications"`
	HonestyBounds  []string       `yaml:"honesty_bounds"`
}

func (p Profile) FilterView() FilterView {
	return FilterView{Qualifications: p.Qualifications, IndustryKeys: p.IndustryKeys(), Derived: p.DerivedTotals()}
}

// ScoreView returns the scoring subset. bonusConditions are the JD conditions
// the screening gate already marked as bonus, reused rather than re-extracted.
func (p Profile) ScoreView(bonusConditions []string) ScoreView {
	return ScoreView{
		Intents: p.Intents, Remote: p.Requirements.Remote, Locations: p.Requirements.Locations,
		Skills: p.Qualifications.Skills, Certifications: p.Qualifications.Certifications,
		Languages: p.Qualifications.Languages, BonusCondition: bonusConditions,
	}
}

func (p Profile) LetterView() LetterView {
	return LetterView{Experiences: p.Experiences, Qualifications: p.Qualifications, HonestyBounds: p.HonestyBounds}
}

// MarshalView renders one gate subset as the YAML that goes into a prompt.
func MarshalView(view any) (string, error) {
	encoded, err := yaml.Marshal(view)
	if err != nil {
		return "", fmt.Errorf("encode profile view: %w", err)
	}
	return string(encoded), nil
}

// SortedIndustryYears reports industry totals in a stable order, which is what
// makes a derived section render and hash identically across saves.
func (d Derived) SortedIndustryYears() []struct {
	Industry string
	Years    float64
} {
	keys := make([]string, 0, len(d.IndustryYears))
	for key := range d.IndustryYears {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]struct {
		Industry string
		Years    float64
	}, 0, len(keys))
	for _, key := range keys {
		out = append(out, struct {
			Industry string
			Years    float64
		}{key, d.IndustryYears[key]})
	}
	return out
}
