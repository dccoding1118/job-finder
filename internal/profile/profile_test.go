package profile

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	if len(value.Experiences) != 3 || contents == "" {
		t.Fatalf("unexpected loaded profile: %+v", value)
	}
	if value.Derived.TotalYears != 4.5 {
		t.Fatalf("derived totals were not materialized: %+v", value.Derived)
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string][2]string{
		"education_level":  {"level: master", "level: invalid"},
		"skill_level":      {"level: proficient", "level: guru"},
		"remote":           {"remote: preferred", "remote: sometimes"},
		"negative_years":   {"years: 3", "years: -3"},
		"missing_industry": {"industry: technology services", "industry: \"  \""},
		"direction":        {"title: cloud architecture", "title: \"\""},
		"blank_exclusion":  {"exclude_companies: []", "exclude_companies: [\"  \"]"},
		"duplicate_skill":  {"name: Kubernetes", "name: go"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			contents := strings.Replace(validProfileYAML(), edit[0], edit[1], 1)
			if _, _, err := Load(writeProfile(t, contents)); err == nil {
				t.Fatalf("Load accepted %s", name)
			}
		})
	}
}

// A stored file may carry `derived` because the system wrote it, but a request
// may not: the totals are computed, not stated.
func TestDecodeJSONRejectsSuppliedDerived(t *testing.T) {
	t.Parallel()
	body := `{"derived":{"total_years":99,"management_years":0,"industry_years":{}}}`
	if _, err := DecodeJSON(strings.NewReader(body)); err == nil {
		t.Fatal("DecodeJSON accepted a supplied derived section")
	}
}

func TestDerivedTotals(t *testing.T) {
	t.Parallel()
	value, _, err := Load(writeProfile(t, validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	derived := value.DerivedTotals()
	// The second experience is an excluded internship, so it counts nowhere.
	if derived.TotalYears != 4.5 || derived.ManagementYears != 1.5 {
		t.Fatalf("derived = %+v", derived)
	}
	if derived.IndustryYears["technology services"] != 4.5 || derived.IndustryYears["retail"] != 0 {
		t.Fatalf("industry years = %+v", derived.IndustryYears)
	}
}

func TestGateViewsExcludeTheOtherGate(t *testing.T) {
	t.Parallel()
	value, _, err := Load(writeProfile(t, validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	scoreView, err := MarshalView(value.ScoreView(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"achievements", "org_type", "honesty_bounds", "backend engineer"} {
		if strings.Contains(scoreView, forbidden) {
			t.Fatalf("score view leaked the resume narrative (%q):\n%s", forbidden, scoreView)
		}
	}
	filterView, err := MarshalView(value.FilterView())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(filterView, "content_likes") {
		t.Fatalf("filter view carried the soft rules:\n%s", filterView)
	}
}

func TestLegacyProfileIsMigrated(t *testing.T) {
	t.Parallel()
	value, _, err := Load(writeProfile(t, legacyProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	if value.Requirements.Remote != remoteAcceptable || len(value.Search.Directions) != 1 {
		t.Fatalf("migrated profile = %+v", value)
	}
	if len(value.Qualifications.Skills) != 3 || value.Experiences[0].Industry == "" {
		t.Fatalf("migrated qualifications = %+v", value.Qualifications)
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
	return `search:
  directions:
    - key: P1
      title: cloud architecture
      keywords: [cloud]
requirements:
  salary_min: 0
  locations: [taipei]
  remote: preferred
  employment_types: []
  industry_avoid: []
  exclude_title_keywords: [intern]
  exclude_description_keywords: []
  exclude_companies: []
intents:
  salary_target: 0
  content_likes: [designing operable services]
  content_dislikes: [manual release procedures]
  industry_interests: [developer tooling]
experiences:
  - industry: technology services
    years: 3
    is_management: false
    skills: [Go]
    org_type: technology provider
    role: backend engineer
    achievements: [reliable delivery]
  - industry: technology services
    years: 1.5
    is_management: true
    skills: [Go]
    org_type: technology provider
    role: engineering lead
    achievements: [grew the team]
  - industry: retail
    years: 0.5
    is_management: false
    exclude_from_totals: true
    org_type: retail chain
    role: intern
qualifications:
  education:
    - level: master
      field: computer science
      status: graduated
  skills:
    - name: Go
      level: proficient
    - name: Kubernetes
      level: familiar
  certifications:
    - name: cloud certification
      status: active
  languages:
    - name: English
      level: fluent
honesty_bounds: [configuration focused]
`
}

func legacyProfileYAML() string {
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
  remote: ok
  directions:
    - key: P1
      title: cloud architecture
      keywords: [cloud]
  industry_avoid: []
honesty_bounds: [configuration focused]
`
}

// Every controlled field accepts a key or any wording of that key and stores
// the key, so a file written before the vocabulary existed keeps loading. A
// value outside the vocabulary is rejected rather than silently kept.
func TestControlledFieldsAreNormalizedOntoTheirVocabulary(t *testing.T) {
	for _, testCase := range []struct {
		field, from, want, invalid string
	}{
		{"status: graduated", "status: 畢業", "graduated", "status: 在學"},
		{"status: active", "status: 有效", "active", "status: 申請中"},
		{"level: fluent", "level: 中等", "intermediate", "level: 母語人士"},
	} {
		value, err := DecodeYAML([]byte(strings.Replace(validProfileYAML(), testCase.field, testCase.from, 1)))
		if err != nil {
			t.Fatalf("%s: %v", testCase.from, err)
		}
		got := value.Qualifications.Education[0].Status + value.Qualifications.Certifications[0].Status + value.Qualifications.Languages[0].Level
		if !strings.Contains(got, testCase.want) {
			t.Fatalf("%s normalized to %q, want %q in it", testCase.from, got, testCase.want)
		}
		if _, err := DecodeYAML([]byte(strings.Replace(validProfileYAML(), testCase.field, testCase.invalid, 1))); err == nil {
			t.Fatalf("%s was accepted", testCase.invalid)
		}
	}
}

// A v4 document stated the locality twice — once to widen the search, once to
// screen with. v5 keeps only the screening list, so the search list is folded
// into it on load and its wording is normalized onto the canonical keys.
func TestSearchLocationsAreFoldedIntoRequirements(t *testing.T) {
	v4 := strings.Replace(validProfileYAML(), "      keywords: [cloud]\n", "      keywords: [cloud]\n  locations: [臺北, Tainan]\n", 1)
	value, err := DecodeYAML([]byte(v4))
	if err != nil {
		t.Fatalf("load v4: %v", err)
	}
	if want := []string{"taipei", "tainan"}; !reflect.DeepEqual(value.Requirements.Locations, want) {
		t.Fatalf("locations = %v, want %v", value.Requirements.Locations, want)
	}
}

// A locality is picked from a vocabulary, so its simplified, traditional and
// English wordings all store the same key and anything else is rejected: a
// free-typed locality would silently match no JD.
func TestLocationsAreNormalizedOntoTheVocabulary(t *testing.T) {
	load := func(list string) (Profile, error) {
		return DecodeYAML([]byte(strings.Replace(validProfileYAML(), "locations: [taipei]", "locations: "+list, 1)))
	}

	value, err := load("[臺北, Taipei, 全台, 海外]")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{"taipei", LocationTaiwan, LocationOverseas}
	if got := value.Requirements.Locations; !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %v, want %v", got, want)
	}

	if _, err := load("[新竹科學園區]"); err == nil {
		t.Fatal("a locality outside the vocabulary was accepted")
	}
}

// Every key matches only the locality it names: `taiwan` is the country stated
// without one, not a stand-in for the counties, and the counties are not a
// stand-in for it either.
func TestLocationKeysMatchOnlyTheirOwnLocality(t *testing.T) {
	countryTerms := LocationTerms([]string{LocationTaiwan})
	for _, want := range []string{"台灣", "臺灣", "全台", "Taiwan"} {
		if !slices.Contains(countryTerms, want) {
			t.Fatalf("taiwan terms are missing %q", want)
		}
	}
	for _, unwanted := range []string{"台北", "Kaohsiung", "海外", "Overseas"} {
		if slices.Contains(countryTerms, unwanted) {
			t.Fatalf("taiwan terms include %q, which is a different locality", unwanted)
		}
	}
	if terms := LocationTerms([]string{"taipei"}); slices.Contains(terms, "台灣") {
		t.Fatalf("taipei terms include the country-only wording: %v", terms)
	}
}

// "全部台灣地區" is a list of keys, not a key: it is every locality in Taiwan
// including the country-only wording, and never `overseas`.
func TestTaiwanLocationKeysCoverTheCountryWithoutOverseas(t *testing.T) {
	keys := TaiwanLocationKeys()
	if !slices.Contains(keys, LocationTaiwan) || !slices.Contains(keys, "taipei") {
		t.Fatalf("taiwan keys = %v", keys)
	}
	if slices.Contains(keys, LocationOverseas) {
		t.Fatal("taiwan keys include overseas")
	}
	if len(keys) != len(Locations)-1 {
		t.Fatalf("taiwan keys cover %d of %d vocabulary terms", len(keys), len(Locations))
	}
}

// A v5 file stated the whole country with one retired key. Loading it must keep
// accepting exactly the jobs it accepted, so the key becomes the list it stood
// for rather than the narrower country-only key.
func TestNationwideMigratesToEveryTaiwanLocality(t *testing.T) {
	value, err := DecodeYAML([]byte(strings.Replace(validProfileYAML(), "locations: [taipei]", "locations: [nationwide, overseas]", 1)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := append(TaiwanLocationKeys(), LocationOverseas)
	if got := value.Requirements.Locations; !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %v, want %v", got, want)
	}
}

// Employment type is the vocabulary a list is built from, so it also de-dupes.
func TestEmploymentTypesAreNormalizedOntoTheVocabulary(t *testing.T) {
	load := func(list string) (Profile, error) {
		return DecodeYAML([]byte(strings.Replace(validProfileYAML(), "employment_types: []", "employment_types: "+list, 1)))
	}

	value, err := load("[full_time, 正職, 約聘, INTERNSHIP]")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{EmploymentFullTime, EmploymentContract, EmploymentInternship}
	if got := value.Requirements.EmploymentTypes; !reflect.DeepEqual(got, want) {
		t.Fatalf("employment_types = %v, want %v", got, want)
	}

	if _, err := load("[freelance]"); err == nil {
		t.Fatal("a type outside the vocabulary was accepted")
	}
}
