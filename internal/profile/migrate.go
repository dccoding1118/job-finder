package profile

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// legacyProfile is the pre-v4 shape. It is kept only to migrate a local file
// written before the hard/soft split, so an existing Profile is not re-entered
// by hand. Nothing else reads it.
type legacyProfile struct {
	Summary           string `yaml:"summary"`
	YearsOfExperience int    `yaml:"years_of_experience"`
	Education         struct {
		Degree string `yaml:"degree"`
		Field  string `yaml:"field"`
	} `yaml:"education"`
	Experiences []struct {
		Role         string   `yaml:"role"`
		OrgType      string   `yaml:"org_type"`
		Years        float64  `yaml:"years"`
		Summary      string   `yaml:"summary"`
		Achievements []string `yaml:"achievements"`
		Skills       []string `yaml:"skills"`
	} `yaml:"experiences"`
	Skills struct {
		Expert     []string `yaml:"expert"`
		Proficient []string `yaml:"proficient"`
		Familiar   []string `yaml:"familiar"`
	} `yaml:"skills"`
	Certifications []Certification `yaml:"certifications"`
	Preferences    struct {
		SalaryMin     int         `yaml:"salary_min"`
		SalaryTarget  int         `yaml:"salary_target"`
		Locations     []string    `yaml:"locations"`
		Remote        string      `yaml:"remote"`
		Directions    []Direction `yaml:"directions"`
		IndustryAvoid []string    `yaml:"industry_avoid"`
		Screening     struct {
			ExcludeTitleKeywords       []string `yaml:"exclude_title_keywords"`
			ExcludeDescriptionKeywords []string `yaml:"exclude_description_keywords"`
			RequireAnyKeywords         []string `yaml:"require_any_keywords"`
			ExcludeCompanies           []string `yaml:"exclude_companies"`
		} `yaml:"screening"`
	} `yaml:"preferences"`
	HonestyBounds []string `yaml:"honesty_bounds"`
}

// migrateSearchLocations folds a v4 document's `search.locations` into
// `requirements.locations`, which v5 made the only地區 statement. The rewrite is
// done on the document rather than a mirrored struct so the v4 shape does not
// have to be kept in code: only the one field that moved is touched.
func migrateSearchLocations(contents []byte) ([]byte, error) {
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return contents, nil
	}
	search, ok := document["search"].(map[string]any)
	if !ok {
		return contents, nil
	}
	moved, ok := search["locations"]
	if !ok {
		return contents, nil
	}
	delete(search, "locations")
	requirements, ok := document["requirements"].(map[string]any)
	if !ok {
		requirements = map[string]any{}
		document["requirements"] = requirements
	}
	requirements["locations"] = unionLists(requirements["locations"], moved)
	rewritten, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	return rewritten, nil
}

// legacyNationwideKey is the retired v5 key that stood for every locality in
// Taiwan at once. Only the key itself is migrated, never its old display
// wordings: 全台 and 不限 are aliases of the live `taiwan` key in v6, and a
// stored Profile always holds keys, so matching the wordings too would expand a
// current file that means the country alone.
const legacyNationwideKey = "nationwide"

// migrateNationwideLocations replaces the retired `nationwide` entry with the
// localities it stood for, so a v5 file keeps accepting exactly the jobs it
// accepted before. The key it is replaced by is a list rather than a single
// value because v6 made every key match only the locality it names: the country
// wording `taiwan` is now one locality among the counties, not a superset of
// them.
func migrateNationwideLocations(contents []byte) ([]byte, error) {
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return contents, nil
	}
	requirements, ok := document["requirements"].(map[string]any)
	if !ok {
		return contents, nil
	}
	values, ok := requirements["locations"].([]any)
	if !ok {
		return contents, nil
	}
	stated := make([]string, 0, len(values))
	for _, value := range values {
		stated = append(stated, fmt.Sprint(value))
	}
	replaced, found := expandNationwideKeys(stated)
	if !found {
		return contents, nil
	}
	expanded := make([]any, 0, len(replaced))
	for _, key := range replaced {
		expanded = append(expanded, key)
	}
	requirements["locations"] = unionLists(expanded, nil)
	rewritten, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	return rewritten, nil
}

// expandNationwideKeys rewrites the retired key in place, reporting whether it
// was there at all so an untouched document is left byte-identical.
func expandNationwideKeys(keys []string) ([]string, bool) {
	found := false
	expanded := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.ToLower(strings.TrimSpace(key)) != legacyNationwideKey {
			expanded = append(expanded, key)
			continue
		}
		found = true
		expanded = append(expanded, TaiwanLocationKeys()...)
	}
	return expanded, found
}

// expandNationwide is the pre-v4 path's form of the same rewrite: that document
// carries its地區 under `preferences`, which the document-level migration does
// not reach.
func expandNationwide(keys []string) []string {
	expanded, _ := expandNationwideKeys(keys)
	return expanded
}

// unionLists appends the entries of extra that kept are missing, comparing on the
// rendered value so the two documents' own scalar types do not matter.
func unionLists(kept, extra any) []any {
	out := make([]any, 0)
	seen := make(map[string]bool)
	for _, list := range []any{kept, extra} {
		values, ok := list.([]any)
		if !ok {
			continue
		}
		for _, value := range values {
			key := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

// isLegacy detects a pre-v4 document by the two sections v4 removed outright.
func isLegacy(contents []byte) bool {
	var probe map[string]yaml.Node
	if err := yaml.Unmarshal(contents, &probe); err != nil {
		return false
	}
	_, hasSearch := probe["search"]
	_, hasPreferences := probe["preferences"]
	_, hasSummary := probe["summary"]
	return !hasSearch && (hasPreferences || hasSummary)
}

// migrateLegacy maps the pre-v4 document onto schema v4. Two fields have no
// source and take a stated default rather than a guess: `industry` falls back to
// the organization type the experience already described, and `is_management`
// starts false — it is a user's own declaration, never inferred from a title.
func migrateLegacy(contents []byte) (Profile, error) {
	var legacy legacyProfile
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&legacy); err != nil {
		return Profile{}, err
	}
	value := Profile{
		Search: Search{Directions: legacy.Preferences.Directions},
		Requirements: Requirements{
			SalaryMin: legacy.Preferences.SalaryMin, Locations: expandNationwide(legacy.Preferences.Locations),
			Remote: migrateRemote(legacy.Preferences.Remote), IndustryAvoid: legacy.Preferences.IndustryAvoid,
			ExcludeTitleKeywords:       legacy.Preferences.Screening.ExcludeTitleKeywords,
			ExcludeDescriptionKeywords: legacy.Preferences.Screening.ExcludeDescriptionKeywords,
			ExcludeCompanies:           legacy.Preferences.Screening.ExcludeCompanies,
		},
		Intents:       Intents{SalaryTarget: legacy.Preferences.SalaryTarget},
		HonestyBounds: legacy.HonestyBounds,
	}
	for _, experience := range legacy.Experiences {
		industry := strings.TrimSpace(experience.OrgType)
		if industry == "" {
			industry = "unclassified"
		}
		value.Experiences = append(value.Experiences, Experience{
			Industry: industry, Years: experience.Years, Skills: experience.Skills,
			OrgType: experience.OrgType, Role: experience.Role, Achievements: experience.Achievements,
		})
	}
	if legacy.Education.Degree != "" {
		value.Qualifications.Education = []EducationEntry{{Level: legacy.Education.Degree, Field: legacy.Education.Field, Status: "graduated"}}
	}
	for level, names := range map[string][]string{"expert": legacy.Skills.Expert, "proficient": legacy.Skills.Proficient, "familiar": legacy.Skills.Familiar} {
		for _, name := range names {
			value.Qualifications.Skills = append(value.Qualifications.Skills, SkillEntry{Name: name, Level: level})
		}
	}
	sortSkills(value.Qualifications.Skills)
	value.Qualifications.Certifications = legacy.Certifications
	if len(value.Experiences) == 0 {
		return Profile{}, errors.New("profile: legacy profile has no experiences to migrate")
	}
	return value, nil
}

// migrateRemote maps the three-value legacy enum onto the four-value one. The
// legacy `ok` stated tolerance without preference, which is `acceptable`.
func migrateRemote(value string) string {
	switch value {
	case remoteRequired, remotePreferred:
		return value
	default:
		return remoteAcceptable
	}
}

// sortSkills keeps the migrated total list deterministic; a map iteration must
// not decide the order a revision hashes.
func sortSkills(skills []SkillEntry) {
	for i := 1; i < len(skills); i++ {
		for j := i; j > 0 && strings.ToLower(skills[j].Name) < strings.ToLower(skills[j-1].Name); j-- {
			skills[j], skills[j-1] = skills[j-1], skills[j]
		}
	}
}
