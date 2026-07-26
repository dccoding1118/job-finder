package store

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// The cross-source identity of a job is derived by program rules only: no Agent
// is involved, so the same two listings always group the same way and every rule
// here is unit-testable.

// companySuffixes are the legal and branch suffixes that carry no identity. They
// are stripped from the end repeatedly, because "Co., Ltd." collapses into two
// of them once the punctuation is gone.
var companySuffixes = []string{
	"股份有限公司", "有限公司", "台灣分公司", "臺灣分公司", "分公司", "公司",
	"incorporated", "limited", "corp", "corporation", "inc", "ltd", "co",
}

// senioritySuffixes are the experience modifiers two postings of the same job
// disagree on. `實習`／`intern` is deliberately absent: an internship is a
// different job, not the same one at another level.
var seniorityModifiers = []string{
	"senior", "staff", "principal", "lead", "junior", "sr", "jr",
	"資深", "中高階", "高級", "初級",
}

// titleSynonyms maps the Chinese and English wordings of the same role onto one
// token. It is a hand-maintained list of the common pairs; a title it does not
// cover falls into the grey zone for the user to decide rather than merging.
var titleSynonyms = [][2]string{
	{"後端", "backend"},
	{"后端", "backend"},
	{"back-end", "backend"},
	{"back end", "backend"},
	{"前端", "frontend"},
	{"前段", "frontend"},
	{"front-end", "frontend"},
	{"front end", "frontend"},
	{"全端", "fullstack"},
	{"全棧", "fullstack"},
	{"full-stack", "fullstack"},
	{"full stack", "fullstack"},
	{"軟體", "software"},
	{"软件", "software"},
	{"資料", "data"},
	{"數據", "data"},
	{"数据", "data"},
	{"雲端", "cloud"},
	{"云端", "cloud"},
	{"平台", "platform"},
	{"網站", "web"},
	{"網頁", "web"},
	{"行動", "mobile"},
	{"移動", "mobile"},
	{"測試", "qa"},
	{"品保", "qa"},
	{"維運", "sre"},
	{"維運工程", "sre"},
	{"站台可靠性", "sre"},
	{"資安", "security"},
	{"安全", "security"},
	{"機器學習", "ml"},
	{"深度學習", "ml"},
	{"演算法", "algorithm"},
	{"架構師", "architect"},
	{"實習生", "intern"},
	{"實習", "intern"},
	{"工程師", "engineer"},
	{"開發者", "engineer"},
	{"開發人員", "engineer"},
	{"developer", "engineer"},
	{"programmer", "engineer"},
	{"engineering", "engineer"},
}

// cityAliases fold the county-level name of a location onto one token whichever
// language and spelling a platform used.
var cityAliases = map[string]string{
	"台北": "taipei", "臺北": "taipei", "taipei": "taipei",
	"新北": "newtaipei", "newtaipei": "newtaipei",
	"桃園": "taoyuan", "taoyuan": "taoyuan",
	"台中": "taichung", "臺中": "taichung", "taichung": "taichung",
	"台南": "tainan", "臺南": "tainan", "tainan": "tainan",
	"高雄": "kaohsiung", "kaohsiung": "kaohsiung",
	"基隆": "keelung", "keelung": "keelung",
	"新竹": "hsinchu", "hsinchu": "hsinchu",
	"苗栗": "miaoli", "miaoli": "miaoli",
	"彰化": "changhua", "changhua": "changhua",
	"南投": "nantou", "nantou": "nantou",
	"雲林": "yunlin", "yunlin": "yunlin",
	"嘉義": "chiayi", "chiayi": "chiayi",
	"屏東": "pingtung", "pingtung": "pingtung",
	"宜蘭": "yilan", "yilan": "yilan",
	"花蓮": "hualien", "hualien": "hualien",
	"台東": "taitung", "臺東": "taitung", "taitung": "taitung",
}

var (
	parentheticalPattern = regexp.MustCompile(`[（(\[【][^）)\]】]*[）)\]】]`)
	nonWordPattern       = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	chineseCityPattern   = regexp.MustCompile(`([\p{Han}]{2,3})[市縣]`)
)

// DedupeKey is the normalized identity of one job: two jobs with the same key
// are the same listing published twice.
type DedupeKey struct {
	Company string
	Title   string
	// Titles are the title tokens the grey-zone similarity is measured on.
	Titles []string
	// City is empty when the job is remote or its location is unknown, which
	// makes it compatible with any other location.
	City string
}

// Hash is the indexed form of the key; it is stored on the group so an exact
// match is an index lookup rather than a comparison against every other job.
func (k DedupeKey) Hash() string {
	if k.Company == "" || k.Title == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(k.Company + "|" + k.Title + "|" + k.City))
	return hex.EncodeToString(digest[:])
}

// LocationCompatible reports whether two keys describe locations that can belong
// to the same listing. An unknown or remote location is compatible with any.
func (k DedupeKey) LocationCompatible(other DedupeKey) bool {
	return k.City == "" || other.City == "" || k.City == other.City
}

// Similarity is the Jaccard overlap of the two title token sets.
func (k DedupeKey) Similarity(other DedupeKey) float64 {
	if len(k.Titles) == 0 || len(other.Titles) == 0 {
		return 0
	}
	left := map[string]bool{}
	for _, token := range k.Titles {
		left[token] = true
	}
	intersection := 0
	right := map[string]bool{}
	for _, token := range other.Titles {
		if right[token] {
			continue
		}
		right[token] = true
		if left[token] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// NewDedupeKey normalizes one job's company, title, and location into the key
// cross-source identity is decided on.
func NewDedupeKey(companyName, title, location, remoteType string) DedupeKey {
	tokens := titleTokens(title)
	key := DedupeKey{
		Company: normalizeCompany(companyName),
		Title:   strings.Join(tokens, ""),
		Titles:  tokens,
	}
	if remoteType != "remote" {
		key.City = normalizeCity(location)
	}
	return key
}

func normalizeCompany(value string) string {
	folded := nonWordPattern.ReplaceAllString(strings.ToLower(halfwidth(value)), "")
	for stripped := true; stripped; {
		stripped = false
		for _, suffix := range companySuffixes {
			trimmed := strings.TrimSuffix(folded, suffix)
			if trimmed != folded && trimmed != "" {
				folded, stripped = trimmed, true
				break
			}
		}
	}
	return folded
}

// titleTokens reduces a job title to the tokens that carry the role, dropping
// the parenthetical notes and experience modifiers two platforms word
// differently and folding the Chinese and English names of a role together.
func titleTokens(value string) []string {
	folded := strings.ToLower(halfwidth(value))
	folded = parentheticalPattern.ReplaceAllString(folded, " ")
	for _, pair := range titleSynonyms {
		folded = strings.ReplaceAll(folded, pair[0], " "+pair[1]+" ")
	}
	tokens := []string{}
	for _, token := range nonWordPattern.Split(folded, -1) {
		if token == "" || isSeniorityModifier(token) {
			continue
		}
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

func isSeniorityModifier(token string) bool {
	for _, modifier := range seniorityModifiers {
		if token == modifier {
			return true
		}
	}
	return false
}

// normalizeCity keeps a location at county level, which is the granularity two
// platforms agree on: one prints a full street address, the other a city name.
func normalizeCity(value string) string {
	folded := strings.ToLower(halfwidth(value))
	if match := chineseCityPattern.FindStringSubmatch(folded); len(match) == 2 {
		if alias, ok := cityAliases[match[1]]; ok {
			return alias
		}
		return match[1]
	}
	for _, token := range nonWordPattern.Split(folded, -1) {
		if alias, ok := cityAliases[token]; ok {
			return alias
		}
	}
	// "New Taipei" and the like arrive as separate tokens; the joined form is the
	// last chance to recognize a known city. The longest name is tried first, so
	// "New Taipei City" never reads as Taipei.
	joined := nonWordPattern.ReplaceAllString(folded, "")
	for _, name := range cityNamesByLength {
		if strings.Contains(joined, name) {
			return cityAliases[name]
		}
	}
	return ""
}

// cityNamesByLength orders the recognized city names longest first, which keeps
// the match deterministic where one name contains another.
var cityNamesByLength = func() []string {
	names := make([]string, 0, len(cityAliases))
	for name := range cityAliases {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	return names
}()

// halfwidth folds the fullwidth Latin block and the ideographic space onto their
// ASCII equivalents, so a platform's typography never splits one job in two.
func halfwidth(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '　':
			return ' '
		case r >= '！' && r <= '～':
			return r - 0xFEE0
		default:
			return r
		}
	}, value)
}
