package agents

import (
	"context"
	"strings"
	"testing"
)

func TestFilterScreenParsesConditions(t *testing.T) {
	answer := `{"conditions":[
		{"text":"碩士以上資工相關","kind":"required","group":1,"category":"education","verdict":"pass","years_required":null,"years_max":null,"industry_keys":[]},
		{"text":"五年以上後端經驗","kind":"required","group":2,"category":"experience_years","verdict":"fail","years_required":5,"years_max":null,"industry_keys":[]},
		{"text":"有金融業經驗尤佳","kind":"bonus","group":3,"category":"industry","verdict":"unknown","years_required":null,"years_max":null,"industry_keys":["finance"]}
	]}`
	result, err := Filter{Primary: &fakeRunner{name: "claude", replies: []string{answer}}}.Screen(context.Background(), "qualifications: {}", Job{Title: "Backend Engineer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conditions) != 3 || result.Runner != "claude" {
		t.Fatalf("screen result = %+v", result)
	}
	if result.Conditions[1].YearsRequired == nil || *result.Conditions[1].YearsRequired != 5 {
		t.Fatalf("the JD's year floor was not read: %+v", result.Conditions[1])
	}
	if bonus := result.BonusTexts(); len(bonus) != 1 || bonus[0] != "有金融業經驗尤佳" {
		t.Fatalf("bonus conditions = %v", bonus)
	}
}

func TestFilterRejectsConditionsOutsideTheContract(t *testing.T) {
	for name, answer := range map[string]string{
		"unknown category": `{"conditions":[{"text":"x","kind":"required","group":1,"category":"vibes","verdict":"pass"}]}`,
		"unknown kind":     `{"conditions":[{"text":"x","kind":"maybe","group":1,"category":"skill","verdict":"pass"}]}`,
		"unknown verdict":  `{"conditions":[{"text":"x","kind":"required","group":1,"category":"skill","verdict":"probably"}]}`,
		"zero group":       `{"conditions":[{"text":"x","kind":"required","group":0,"category":"skill","verdict":"pass"}]}`,
		"negative years":   `{"conditions":[{"text":"x","kind":"required","group":1,"category":"experience_years","verdict":"pass","years_required":-2}]}`,
		"empty text":       `{"conditions":[{"text":" ","kind":"required","group":1,"category":"skill","verdict":"pass"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFilter(answer); err == nil {
				t.Fatalf("parseFilter accepted %s", name)
			}
			if kind := ClassifyFailure("filter", answer); kind != FailureInvalidCondition {
				t.Fatalf("failure kind = %q, want %q", kind, FailureInvalidCondition)
			}
		})
	}
}

// An empty condition list is a valid answer: a JD may state no requirement the
// screening gate can act on.
func TestFilterAcceptsAnEmptyConditionList(t *testing.T) {
	result, err := parseFilter(`{"conditions":[]}`)
	if err != nil || len(result.Conditions) != 0 {
		t.Fatalf("parseFilter = %+v, %v", result, err)
	}
}

// The screening prompt carries the hard rules only, and asks the Agent not to
// do the arithmetic the program does.
func TestFilterPromptScope(t *testing.T) {
	prompt := filterPrompt("qualifications:\n  skills: []\n", Job{Title: "Backend Engineer", Description: "JD"})
	for _, forbidden := range []string{"content_likes", "salary_target", "industry_interests"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("filter prompt carried the soft rules (%q)", forbidden)
		}
	}
	for _, required := range []string{"unknown", "years_required", "industry_keys"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("filter prompt is missing %q", required)
		}
	}
}

// The scoring prompt must never see the resume narrative: an achievement reads
// as "good at it, therefore a good fit", which is the inference being removed.
func TestScorePromptExcludesResumeNarrative(t *testing.T) {
	prompt := scorePrompt("intents:\n  content_likes: [x]\n", Job{Title: "Backend Engineer"}, 75)
	for _, forbidden := range []string{"achievements", "org_type", "honesty_bounds"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("score prompt carried %q", forbidden)
		}
	}
	if !strings.Contains(prompt, "只加不減") || !strings.Contains(prompt, "75") {
		t.Fatalf("score prompt lost the bonus rule or the baseline:\n%s", prompt)
	}
}
