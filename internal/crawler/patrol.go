package crawler

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Patrol URLs are the semi-passive counterpart of a search request: the same
// Profile directions that drive an automated source are printed as links for the
// user to open in their own browser, where the extension harvests what the page
// shows. Nothing here sends a request — for Cake a server-side request to these
// very URLs is what the platform challenges (see design-crawler §1).

// PatrolPage is one search URL of one direction.
type PatrolPage struct {
	Direction string
	Keywords  []string
	Page      int
	URL       string
}

// patrolArea maps a Profile location onto each platform's own way of naming it.
// A location the table does not cover is left out of the URL rather than guessed:
// a wrong area parameter silently narrows the results to nothing.
type patrolArea struct {
	aliases []string
	// code104 is the 104 `area` parameter.
	code104 string
	// cake is the Cake `location_list[]` value.
	cake string
}

var patrolAreas = []patrolArea{
	{[]string{"台北", "臺北", "taipei"}, "6001001000", "Taipei City, Taiwan"},
	{[]string{"新北", "new taipei", "newtaipei"}, "6001002000", "New Taipei City, Taiwan"},
	{[]string{"桃園", "taoyuan"}, "6001005000", "Taoyuan City, Taiwan"},
	{[]string{"新竹", "hsinchu"}, "6001006000", "Hsinchu City, Taiwan"},
	{[]string{"台中", "臺中", "taichung"}, "6001008000", "Taichung City, Taiwan"},
	{[]string{"台南", "臺南", "tainan"}, "6001014000", "Tainan City, Taiwan"},
	{[]string{"高雄", "kaohsiung"}, "6001016000", "Kaohsiung City, Taiwan"},
}

// cakeProfessions is the supplementary net: a coarse job category to run beside
// the skill-keyword net. It is keyed by the keywords a direction actually uses,
// and a direction matching nothing simply gets no category parameter.
var cakeProfessions = []struct {
	profession string
	keywords   []string
}{
	{"it_back-end-engineer", []string{"backend", "back-end", "後端", "go", "golang", "java", "python", "node", "rails", "api"}},
	{"it_front-end-engineer", []string{"frontend", "front-end", "前端", "react", "vue", "angular", "typescript"}},
	{"it_devops-engineer", []string{"devops", "sre", "kubernetes", "k8s", "terraform", "iac", "platform", "reliability", "cloud", "aws", "gcp", "azure"}},
	{"it_data-engineer", []string{"data engineer", "資料工程", "etl", "spark", "airflow", "bigquery"}},
	{"it_qa-engineer", []string{"qa", "test", "測試", "automation"}},
	{"it_information-security-engineer", []string{"security", "資安", "appsec", "soc"}},
}

// PatrolURLs generates the search URLs of one semi-passive platform from the
// same spec an automated source would fetch with. pages is how many result pages
// of each direction to offer.
func PatrolURLs(source string, spec SearchSpec, pages int) ([]PatrolPage, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if pages <= 0 {
		pages = spec.MaxPages
	}
	if pages <= 0 {
		pages = 1
	}
	build, err := patrolBuilder(source)
	if err != nil {
		return nil, err
	}
	out := make([]PatrolPage, 0, len(spec.Queries)*pages)
	for _, query := range spec.Queries {
		for page := 1; page <= pages; page++ {
			out = append(out, PatrolPage{
				Direction: query.Direction,
				Keywords:  append([]string(nil), query.Keywords...),
				Page:      page,
				URL:       build(query, spec.Area, page),
			})
		}
	}
	return out, nil
}

func patrolBuilder(source string) (func(SearchQuery, []string, int) string, error) {
	switch source {
	case "104":
		return patrol104URL, nil
	case "cake":
		return patrolCakeURL, nil
	default:
		return nil, fmt.Errorf("crawler: %q has no patrol URLs; only the semi-passive sources 104 and cake do", source)
	}
}

// patrol104URL orders by most recently updated, because a patrol is a repeated
// pass over the same conditions and only the new listings are worth the user's
// scroll. The job category parameter is left out: 104's category codes are not
// mapped here, and a wrong code would silently empty the result page.
func patrol104URL(query SearchQuery, locations []string, page int) string {
	values := url.Values{}
	values.Set("keyword", strings.Join(query.Keywords, " "))
	if areas := areaCodes(locations); len(areas) > 0 {
		values.Set("area", strings.Join(areas, ","))
	}
	values.Set("order", "15")
	values.Set("mode", "s")
	values.Set("page", strconv.Itoa(page))
	return "https://www.104.com.tw/jobs/search/?" + values.Encode()
}

func patrolCakeURL(query SearchQuery, locations []string, page int) string {
	values := url.Values{}
	values.Set("query", strings.Join(query.Keywords, " "))
	for _, location := range cakeLocations(locations) {
		values.Add("location_list[]", location)
	}
	if profession := cakeProfession(query.Keywords); profession != "" {
		values.Add("profession[]", profession)
	}
	values.Set("page", strconv.Itoa(page))
	return "https://www.cake.me/jobs?" + values.Encode()
}

func areaCodes(locations []string) []string {
	codes := []string{}
	for _, area := range matchAreas(locations) {
		codes = append(codes, area.code104)
	}
	return codes
}

func cakeLocations(locations []string) []string {
	values := []string{}
	for _, area := range matchAreas(locations) {
		values = append(values, area.cake)
	}
	return values
}

func matchAreas(locations []string) []patrolArea {
	matched := []patrolArea{}
	seen := map[string]bool{}
	for _, location := range locations {
		folded := strings.ToLower(strings.TrimSpace(location))
		for _, area := range patrolAreas {
			if seen[area.code104] {
				continue
			}
			for _, alias := range area.aliases {
				if strings.Contains(folded, alias) {
					matched, seen[area.code104] = append(matched, area), true
					break
				}
			}
		}
	}
	return matched
}

// cakeProfession picks the single category that best fits a direction: the one
// the most of its keywords belong to.
func cakeProfession(keywords []string) string {
	folded := strings.ToLower(strings.Join(keywords, " "))
	type hit struct {
		profession string
		count      int
	}
	hits := []hit{}
	for _, candidate := range cakeProfessions {
		count := 0
		for _, keyword := range candidate.keywords {
			if strings.Contains(folded, keyword) {
				count++
			}
		}
		if count > 0 {
			hits = append(hits, hit{candidate.profession, count})
		}
	}
	if len(hits) == 0 {
		return ""
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].count != hits[j].count {
			return hits[i].count > hits[j].count
		}
		return hits[i].profession < hits[j].profession
	})
	return hits[0].profession
}
