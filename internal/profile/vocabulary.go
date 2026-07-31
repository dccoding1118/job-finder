package profile

import (
	"fmt"
	"strings"
)

// A controlled vocabulary is a fixed set of choices the editor offers as a
// dropdown. The stored value is always the key; the label is only what a form
// shows, and the aliases are the other wordings a value may arrive as — an
// older file that stored the Chinese label, or in the employment case the
// wording a JD itself uses.
type Term struct {
	Key     string
	Label   string
	Aliases []string
}

// Vocabulary is one such set, in the order a form should present it.
type Vocabulary []Term

const (
	EmploymentFullTime   = "full_time"
	EmploymentPartTime   = "part_time"
	EmploymentContract   = "contract"
	EmploymentInternship = "internship"
)

// EmploymentTypes is the only vocabulary whose aliases are also matched against
// JD text: a user picks 全職 and a JD that says 正職 must still count.
var EmploymentTypes = Vocabulary{
	{EmploymentFullTime, "全職", []string{"全職", "正職", "full-time", "full time", "fulltime"}},
	{EmploymentPartTime, "兼職", []string{"兼職", "工讀", "part-time", "part time", "parttime"}},
	{EmploymentContract, "約聘", []string{"約聘", "約僱", "派遣", "契約", "contract"}},
	{EmploymentInternship, "實習", []string{"實習", "internship", "intern"}},
}

const (
	// LocationNationwide is Taiwan without a locality restriction; LocationOverseas
	// is everything outside it, kept whole because a single-person job search does
	// not act on which country.
	LocationNationwide = "nationwide"
	LocationOverseas   = "overseas"
)

// Locations is the one地區 vocabulary: the user picks from it, and its aliases are
// matched against the locality a JD states. Simplified/traditional 台臺 and the
// English romanization are aliases of the same key, so a source's own wording
// never has to be guessed at. `新竹`／`嘉義` without 市／縣 stays an alias of both
// the city and the county: an ambiguous wording must not reject a job.
var Locations = Vocabulary{
	{"taipei", "台北市", []string{"台北", "臺北", "Taipei"}},
	{"new_taipei", "新北市", []string{"新北", "New Taipei", "NewTaipei"}},
	{"keelung", "基隆市", []string{"基隆", "Keelung"}},
	{"taoyuan", "桃園市", []string{"桃園", "Taoyuan"}},
	{"hsinchu_city", "新竹市", []string{"新竹", "Hsinchu"}},
	{"hsinchu_county", "新竹縣", []string{"新竹", "Hsinchu"}},
	{"miaoli", "苗栗縣", []string{"苗栗", "Miaoli"}},
	{"taichung", "台中市", []string{"台中", "臺中", "Taichung"}},
	{"changhua", "彰化縣", []string{"彰化", "Changhua"}},
	{"nantou", "南投縣", []string{"南投", "Nantou"}},
	{"yunlin", "雲林縣", []string{"雲林", "Yunlin"}},
	{"chiayi_city", "嘉義市", []string{"嘉義", "Chiayi"}},
	{"chiayi_county", "嘉義縣", []string{"嘉義", "Chiayi"}},
	{"tainan", "台南市", []string{"台南", "臺南", "Tainan"}},
	{"kaohsiung", "高雄市", []string{"高雄", "Kaohsiung"}},
	{"pingtung", "屏東縣", []string{"屏東", "Pingtung"}},
	{"yilan", "宜蘭縣", []string{"宜蘭", "Yilan"}},
	{"hualien", "花蓮縣", []string{"花蓮", "Hualien"}},
	{"taitung", "台東縣", []string{"台東", "臺東", "Taitung"}},
	{"penghu", "澎湖縣", []string{"澎湖", "Penghu"}},
	{"kinmen", "金門縣", []string{"金門", "Kinmen"}},
	{"lienchiang", "連江縣", []string{"連江", "馬祖", "Lienchiang", "Matsu"}},
	{LocationNationwide, "全台", []string{"全台", "全臺", "全國", "不限", "Taiwan"}},
	{LocationOverseas, "海外", []string{"海外", "國外", "Overseas", "Abroad"}},
}

// EducationStatuses distinguishes a completed degree from an incomplete one,
// because the Filter Agent may only credit a degree that was finished.
var EducationStatuses = Vocabulary{
	{"graduated", "畢業", []string{"畢業", "graduated"}},
	{"attended", "肄業", []string{"肄業", "attended", "未畢業"}},
}

// CertificationStatuses separates a certification that currently counts from
// one that has lapsed.
var CertificationStatuses = Vocabulary{
	{"active", "有效", []string{"有效", "active", "valid"}},
	{"expired", "過期", []string{"過期", "expired"}},
	{"renewing", "過期重考中", []string{"過期重考中", "重考中", "renewing"}},
}

// LanguageLevels is a four-step scale, coarse on purpose: a JD asks for working
// proficiency, not a test score.
var LanguageLevels = Vocabulary{
	{"native", "母語", []string{"母語", "native"}},
	{"fluent", "流利", []string{"流利", "精通", "fluent", "advanced"}},
	{"intermediate", "中等", []string{"中等", "intermediate"}},
	{"basic", "基礎", []string{"基礎", "初級", "basic", "beginner"}},
}

// LocationTerms expands the user's chosen location keys into every wording a JD
// may state those localities with. `nationwide` stands for any locality in
// Taiwan, so it expands to all of them — but never to `overseas`, which is the
// one key it deliberately excludes.
func LocationTerms(keys []string) []string {
	terms := make([]string, 0, len(keys)*3)
	for _, key := range keys {
		if key != LocationNationwide {
			terms = append(terms, Locations.Aliases(key)...)
			continue
		}
		for _, term := range Locations {
			if term.Key != LocationOverseas {
				terms = append(terms, term.Aliases...)
			}
		}
	}
	return terms
}

// Keys lists the vocabulary's keys, for error messages and validation.
func (v Vocabulary) Keys() []string {
	keys := make([]string, 0, len(v))
	for _, term := range v {
		keys = append(keys, term.Key)
	}
	return keys
}

// Aliases returns every wording of one key, or nil for an unknown key.
func (v Vocabulary) Aliases(key string) []string {
	for _, term := range v {
		if term.Key == key {
			return term.Aliases
		}
	}
	return nil
}

// AllAliases is every wording of every term in the vocabulary.
func (v Vocabulary) AllAliases() []string {
	all := make([]string, 0, len(v)*4)
	for _, term := range v {
		all = append(all, term.Aliases...)
	}
	return all
}

// Resolve maps a value onto its key, matching the key itself or any alias,
// ignoring case and surrounding space.
func (v Vocabulary) Resolve(value string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, term := range v {
		if strings.EqualFold(value, term.Key) {
			return term.Key, true
		}
		for _, alias := range term.Aliases {
			if strings.EqualFold(value, alias) {
				return term.Key, true
			}
		}
	}
	return "", false
}

// Normalize rewrites a value onto its key where it resolves, and otherwise
// leaves it untouched so validation can reject it by name.
func (v Vocabulary) Normalize(value string) string {
	if key, ok := v.Resolve(value); ok {
		return key
	}
	return strings.TrimSpace(value)
}

// NormalizeList normalizes every entry and drops duplicates, preserving order.
func (v Vocabulary) NormalizeList(values []string) []string {
	if len(values) == 0 {
		return values
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		key := v.Normalize(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, key)
	}
	return normalized
}

// validateTerm rejects a value outside the vocabulary, naming the legal keys.
func validateTerm(v Vocabulary, field, value string) error {
	if _, ok := v.Resolve(value); !ok {
		return fmt.Errorf("profile: %s must be one of %s", field, strings.Join(v.Keys(), ", "))
	}
	return nil
}
