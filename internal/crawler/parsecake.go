package crawler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The Cake parser has no fetching side either. Cake's content is open but its
// search is behind a challenge, so the platform can only be reached the way the
// user reaches it: the extension reads the page they opened themselves and this
// parser turns that material into jobs (see design-crawler §1 and §4).

// CakeCapture is the material of one Cake page: the text of its
// `script#__NEXT_DATA__`, or the DOM harvest the content script fell back to
// when that snapshot no longer matches the conditions on screen.
type CakeCapture struct {
	// URL is the page the user had open.
	URL string
	// NextData is the raw JSON of the page's __NEXT_DATA__ script.
	NextData string
	// Items carries the DOM harvest of a list page; it is used only when
	// __NEXT_DATA__ is absent or stale.
	Items []CakeListItem
	// JobDOM carries the DOM harvest of a detail page. Cake's detail pages carry
	// no listing state in `__NEXT_DATA__`, so this is the JD's only source.
	JobDOM *CakeJobDOM
}

// CakeJobDOM is one Cake detail page as the content script read it off the
// rendered page. Title, company, and the sections are what the JD is made of;
// the metadata lines are harvested verbatim because Cake renders them as free
// text whose position is not stable.
type CakeJobDOM struct {
	Title       string
	CompanyName string
	// Sections are the JD blocks in the order the page shows them, each already
	// reduced to a heading and its plain text.
	Sections []CakeJobSection
	// Meta are the short text lines of the listing's metadata area; the location
	// and the salary are recognized out of them by the same rules the list harvest
	// uses. Lines that match neither are ignored.
	Meta []string
}

// CakeJobSection is one titled block of a Cake JD.
type CakeJobSection struct {
	Title string
	Body  string
}

// CakeListItem is one visible list entry as the content script read it off the
// page, used when the embedded snapshot cannot be trusted.
type CakeListItem struct {
	// Href is the job link, `/companies/{company}/jobs/{job}` relative or absolute.
	Href        string
	Title       string
	CompanyName string
	Location    string
	// SalaryText is the salary line verbatim; only an explicit monthly TWD range
	// is ever parsed out of it.
	SalaryText string
}

// cakeNextData is the part of Cake's Next.js state this parser reads. Everything
// else on the page is ignored.
type cakeNextData struct {
	Props struct {
		PageProps struct {
			SSR struct {
				Search cakeSearchConditions `json:"search"`
			} `json:"ssr"`
			InitialState struct {
				JobSearch struct {
					EntityByPathID map[string]cakeSearchEntity `json:"entityByPathId"`
				} `json:"jobSearch"`
			} `json:"initialState"`
			Job     *cakeJob     `json:"job"`
			Company *cakeCompany `json:"company"`
		} `json:"pageProps"`
	} `json:"props"`
}

// cakeSearchConditions is the set of conditions the page was rendered with. Cake
// carries it as an object rather than as the query string of the URL.
type cakeSearchConditions struct {
	Query   string         `json:"query"`
	Page    int            `json:"page"`
	Filters map[string]any `json:"filters"`
}

type cakeSearchEntity struct {
	Path      string   `json:"path"`
	Title     string   `json:"title"`
	Locations []string `json:"locations"`
	Salary    struct {
		Min      *int   `json:"min"`
		Max      *int   `json:"max"`
		Currency string `json:"currency"`
		Type     string `json:"type"`
	} `json:"salary"`
	Page struct {
		Path    string `json:"path"`
		Name    string `json:"name"`
		Geo     string `json:"geo"`
		Country string `json:"country"`
	} `json:"page"`
}

type cakeJob struct {
	Path             string `json:"path"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	Requirements     string `json:"requirements"`
	InterviewProcess string `json:"interview_process"`
	Locations        []struct {
		FullName   string `json:"full_name"`
		FullNameEN string `json:"full_name_en"`
	} `json:"locations"`
	SalaryMin             *int   `json:"salary_min"`
	SalaryMax             *int   `json:"salary_max"`
	SalaryType            string `json:"salary_type"`
	SalaryCurrency        string `json:"salary_currency"`
	HideSalaryCompletely  bool   `json:"hide_salary_completely"`
	HideSalaryMax         bool   `json:"hide_salary_max"`
	Remote                string `json:"remote"`
	AasmState             string `json:"aasm_state"`
	CompanyPathDeprecated string `json:"company_path"`
}

// cakeCompany deliberately reads no contact fields: a company's contact name,
// email, and phone are PII and never enter the database.
type cakeCompany struct {
	Path               string `json:"path"`
	Name               string `json:"name"`
	ProductsOrServices string `json:"products_or_services"`
	FoundedYear        any    `json:"founded_year"`
	GeoFormattedAddr   string `json:"geo_formatted_address"`
	GeoCity            string `json:"geo_city"`
	GeoStateName       string `json:"geo_state_name"`
	// Cake localizes the same fields under a `_l` suffix, following `geo_l_locale`.
	GeoCityLocalized      string `json:"geo_city_l"`
	GeoStateNameLocalized string `json:"geo_state_name_l"`
}

// cakeSalaryPerMonth is the only salary type Cake figures are trusted for; any
// other period would have to be converted, and a guessed salary turns into a
// false screening rejection.
const cakeSalaryPerMonth = "per_month"

// listed reports the states in which a Cake job is actually open. A closed or
// draft listing is not stored.
func (j cakeJob) listed() bool {
	return j.AasmState == "" || j.AasmState == "published" || j.AasmState == "active" || j.AasmState == "listed"
}

// CakeSnapshotFresh reports whether a page's embedded state still describes the
// conditions on screen. Cake never refreshes __NEXT_DATA__ after the user
// changes a filter or turns a page, so a stale snapshot must not be harvested.
func CakeSnapshotFresh(nextData, currentSearch string) bool {
	var document cakeNextData
	if err := json.Unmarshal([]byte(nextData), &document); err != nil {
		return false
	}
	return sameSearch(document.Props.PageProps.SSR.Search, currentSearch)
}

// sameSearch compares the conditions the page was rendered with against the
// conditions the URL now names. The comparison is deliberately conservative:
// only the keys the object form names are recognized, so any other parameter on
// the URL makes the snapshot stale. Reading the snapshot under conditions that
// were not the ones it was rendered for would attribute other jobs to the
// search on screen.
func sameSearch(rendered cakeSearchConditions, currentSearch string) bool {
	values, err := url.ParseQuery(strings.TrimPrefix(strings.TrimSpace(currentSearch), "?"))
	if err != nil {
		return false
	}
	page := strings.TrimSpace(values.Get("page"))
	if page == "" {
		page = "1"
	}
	query := values.Get("query")
	values.Del("query")
	values.Del("page")
	return len(rendered.Filters) == 0 && len(values) == 0 &&
		text(rendered.Query) == text(query) &&
		strconv.Itoa(max(rendered.Page, 1)) == page
}

// ParseCakeList normalizes one captured Cake list page into partial jobs. The
// embedded snapshot is preferred because its fields are structured; the DOM
// harvest is used when the content script reported that the snapshot is stale.
// Either way the list carries no trustworthy JD, so every job is partial.
func ParseCakeList(capture CakeCapture) ([]RawJob, error) {
	if strings.TrimSpace(capture.NextData) == "" {
		return parseCakeListDOM(capture.Items)
	}
	var document cakeNextData
	if err := json.Unmarshal([]byte(capture.NextData), &document); err != nil {
		return nil, fmt.Errorf("cake list: decode __NEXT_DATA__: %w", err)
	}
	entities := document.Props.PageProps.InitialState.JobSearch.EntityByPathID
	if len(entities) == 0 {
		return parseCakeListDOM(capture.Items)
	}
	jobs := make([]RawJob, 0, len(entities))
	for _, entity := range entities {
		// An entry the page does not describe well enough is skipped rather than
		// failing the capture: one such item must not cost every other item on
		// that page its mark (see design-crawler §2).
		id, link, err := cakeIdentity(entity.Page.Path, entity.Path)
		if err != nil {
			continue
		}
		if text(entity.Title) == "" || text(entity.Page.Name) == "" {
			continue
		}
		job := RawJob{
			Source: "cake", ExternalID: id, URL: link,
			Title: text(entity.Title), CompanyName: text(entity.Page.Name),
			CompanyInfo: cakeCompanyGeo(entity.Page.Country, entity.Page.Geo),
			Location:    cakeListLocation(entity),
			// The list has no remote field and its description serves the search
			// summary, so neither is guessed at.
			RemoteType: "unknown",
		}
		if entity.Salary.Currency == "TWD" && entity.Salary.Type == cakeSalaryPerMonth {
			job.SalaryMin, job.SalaryMax = entity.Salary.Min, entity.Salary.Max
		}
		jobs = append(jobs, job)
	}
	sortRawJobs(jobs)
	return jobs, nil
}

func parseCakeListDOM(items []CakeListItem) ([]RawJob, error) {
	jobs := make([]RawJob, 0, len(items))
	for _, item := range items {
		id, link, err := cakeIdentityFromHref(item.Href)
		if err != nil {
			continue
		}
		if text(item.Title) == "" || text(item.CompanyName) == "" {
			continue
		}
		location := text(item.Location)
		if location == "" {
			location = "unknown"
		}
		job := RawJob{
			Source: "cake", ExternalID: id, URL: link,
			Title: text(item.Title), CompanyName: text(item.CompanyName), CompanyInfo: "public listing",
			Location: location, RemoteType: "unknown",
		}
		job.SalaryMin, job.SalaryMax = cakeSalaryText(item.SalaryText)
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// ParseCakeJob turns one captured Cake detail page into a full job. The DOM
// harvest is the primary path: a Cake detail page carries no listing state in
// `__NEXT_DATA__`, and the client-side navigation the user reaches the page by
// leaves whatever state is there describing the list they came from. The
// embedded path is still honored when a capture supplies it.
func ParseCakeJob(capture CakeCapture) (RawJob, error) {
	if capture.JobDOM != nil {
		return parseCakeJobDOM(capture.URL, *capture.JobDOM)
	}
	if strings.TrimSpace(capture.NextData) == "" {
		return RawJob{}, fmt.Errorf("cake job: the page carries no job content")
	}
	var document cakeNextData
	if err := json.Unmarshal([]byte(capture.NextData), &document); err != nil {
		return RawJob{}, fmt.Errorf("cake job: decode __NEXT_DATA__: %w", err)
	}
	job := document.Props.PageProps.Job
	if job == nil {
		return RawJob{}, fmt.Errorf("cake job: __NEXT_DATA__ carries no pageProps.job")
	}
	if !job.listed() {
		return RawJob{}, fmt.Errorf("cake job: listing state %q is not open", job.AasmState)
	}
	companyPath, jobPath := cakePathsFromURL(capture.URL)
	if jobPath == "" {
		jobPath = job.Path
	}
	if companyPath == "" {
		companyPath = job.CompanyPathDeprecated
		if company := document.Props.PageProps.Company; companyPath == "" && company != nil {
			companyPath = company.Path
		}
	}
	id, link, err := cakeIdentity(companyPath, jobPath)
	if err != nil {
		return RawJob{}, fmt.Errorf("cake job: %w", err)
	}
	description, err := cakeDescription(job)
	if err != nil {
		return RawJob{}, err
	}
	company := document.Props.PageProps.Company
	out := RawJob{
		Source: "cake", ExternalID: id, URL: link,
		Title: text(job.Title), Description: description,
		Location: cakeJobLocation(job, company), RemoteType: cakeRemoteType(job.Remote),
	}
	if company != nil {
		out.CompanyName = text(company.Name)
		out.CompanyInfo = cakeCompanyInfo(*company)
	}
	if out.Title == "" || out.CompanyName == "" {
		return RawJob{}, fmt.Errorf("cake job %s: title and company are required", id)
	}
	if out.Location == "" {
		out.Location = "unknown"
	}
	if out.CompanyInfo == "" {
		out.CompanyInfo = "public listing"
	}
	out.SalaryMin, out.SalaryMax = cakeSalary(job)
	return out, nil
}

// cakeJobLocationPattern recognizes a metadata line that names a place. It is
// deliberately loose: a line wrongly read as a location screens the job against
// the Profile's location rule, where a missed one only leaves it unknown.
var cakeJobLocationPattern = regexp.MustCompile(`(?i)市|縣|區|Taiwan|Taipei|Remote|遠端`)

// cakeJobRemotePattern recognizes the remote wording Cake shows on a listing.
var cakeJobRemotePattern = regexp.MustCompile(`(?i)完全遠端|full remote`)

// cakeJobHybridPattern recognizes the partial-remote wording.
var cakeJobHybridPattern = regexp.MustCompile(`(?i)部分遠端|混合|hybrid|partial remote`)

// parseCakeJobDOM turns the rendered detail page into a job. The title, the
// company, and at least one JD section are required, because those are what
// scoring reads; the location and the salary stay unknown when the metadata
// carries neither, which leaves the job assessable rather than wrongly screened.
func parseCakeJobDOM(pageURL string, dom CakeJobDOM) (RawJob, error) {
	companyPath, jobPath := cakePathsFromURL(pageURL)
	id, link, err := cakeIdentity(companyPath, jobPath)
	if err != nil {
		return RawJob{}, fmt.Errorf("cake job: %w", err)
	}
	description := cakeDOMDescription(dom.Sections)
	if description == "" {
		return RawJob{}, fmt.Errorf("cake job %s: the page carries no job description", id)
	}
	out := RawJob{
		Source: "cake", ExternalID: id, URL: link,
		Title: text(dom.Title), CompanyName: text(dom.CompanyName), CompanyInfo: "public listing",
		Description: description, Location: "unknown", RemoteType: "unknown",
	}
	if out.Title == "" || out.CompanyName == "" {
		return RawJob{}, fmt.Errorf("cake job %s: title and company are required", id)
	}
	for _, line := range dom.Meta {
		value := text(line)
		if value == "" {
			continue
		}
		if out.Location == "unknown" && cakeJobLocationPattern.MatchString(value) {
			out.Location = value
		}
		if out.SalaryMin == nil && out.SalaryMax == nil {
			out.SalaryMin, out.SalaryMax = cakeSalaryText(value)
		}
		switch {
		case cakeJobHybridPattern.MatchString(value):
			out.RemoteType = "hybrid"
		case cakeJobRemotePattern.MatchString(value):
			out.RemoteType = "remote"
		}
	}
	return out, nil
}

// cakeDOMDescription joins the JD blocks the page showed, keeping their headings
// so the Scorer reads requirements as requirements. A block with a body and no
// heading is kept; a heading with no body is dropped.
func cakeDOMDescription(sections []CakeJobSection) string {
	parts := []string{}
	for _, section := range sections {
		body := text(section.Body)
		if body == "" {
			continue
		}
		if title := text(section.Title); title != "" {
			parts = append(parts, title+"\n"+body)
			continue
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n")
}

// cakeDescription joins the three HTML sections of a Cake listing into one plain
// text JD. An empty section is skipped rather than titled: `requirements` is
// often blank.
func cakeDescription(job *cakeJob) (string, error) {
	sections := []struct{ title, html string }{
		{"工作內容", job.Description},
		{"條件要求", job.Requirements},
		{"面試流程", job.InterviewProcess},
	}
	parts := []string{}
	for _, section := range sections {
		if strings.TrimSpace(section.html) == "" {
			continue
		}
		// decodeJobDescription errors on an empty result, which a section that was
		// pure markup would produce; such a section is skipped like a blank one.
		decoded, err := decodeJobDescription(section.html)
		if err != nil {
			continue
		}
		parts = append(parts, section.title+"\n"+decoded)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("cake job: the listing carries no job description")
	}
	return strings.Join(parts, "\n\n"), nil
}

// cakeSalary trusts only an explicit monthly TWD figure, and honors both salary
// hiding flags: a hidden figure must not be read off the underlying value.
func cakeSalary(job *cakeJob) (*int, *int) {
	if job.HideSalaryCompletely {
		return nil, nil
	}
	if job.SalaryType != "" && job.SalaryType != cakeSalaryPerMonth {
		return nil, nil
	}
	if job.SalaryCurrency != "" && !strings.EqualFold(job.SalaryCurrency, "TWD") {
		return nil, nil
	}
	min, max := job.SalaryMin, job.SalaryMax
	if job.HideSalaryMax {
		max = nil
	}
	if min != nil && max != nil && *max < *min {
		return nil, nil
	}
	return min, max
}

// cakeSalaryText reads a salary line off the DOM harvest. Only an explicit
// monthly range counts; anything else stays unknown.
func cakeSalaryText(value string) (*int, *int) {
	folded := strings.ReplaceAll(text(value), " ", "")
	if folded == "" || !strings.Contains(folded, "月") {
		return nil, nil
	}
	return salaryRange(folded)
}

// cakeRemoteType maps Cake's remote field. An unknown value stays unknown rather
// than being read as onsite: a wrong onsite would reject a remote job.
func cakeRemoteType(value string) string {
	switch {
	case value == "no_remote_work":
		return "onsite"
	case value == "full_remote_work":
		return "remote"
	case strings.Contains(value, "partial") || strings.Contains(value, "hybrid"):
		return "hybrid"
	default:
		return "unknown"
	}
}

func cakeListLocation(entity cakeSearchEntity) string {
	for _, location := range entity.Locations {
		if value := text(location); value != "" {
			return value
		}
	}
	return "unknown"
}

// cakeJobLocation reads the listing's own locations, falling back to the
// company's registered city. A Cake listing often carries no location of its own
// — a remote or hybrid one in particular — and an unknown location is screened
// out by the Profile's location rule, so the company's city is what keeps such a
// listing assessable rather than silently rejected.
func cakeJobLocation(job *cakeJob, company *cakeCompany) string {
	names := []string{}
	for _, location := range job.Locations {
		value := text(location.FullName)
		if value == "" {
			value = text(location.FullNameEN)
		}
		if value != "" {
			names = append(names, value)
		}
	}
	if len(names) > 0 {
		return strings.Join(names, "、")
	}
	if company == nil {
		return ""
	}
	// Only the city and the administrative area are read: a full street address
	// adds nothing to screening.
	for _, value := range []string{
		cakeCompanyCity(company.GeoStateNameLocalized, company.GeoCityLocalized),
		cakeCompanyCity(company.GeoStateName, company.GeoCity),
	} {
		if value != "" {
			return value
		}
	}
	return ""
}

func cakeCompanyCity(state, city string) string {
	state, city = text(state), text(city)
	if state == "" {
		return city
	}
	if city == "" || city == state {
		return state
	}
	return state + city
}

func cakeCompanyGeo(country, geo string) string {
	parts := []string{}
	for _, value := range []string{text(country), text(geo)} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "／")
}

// cakeCompanyInfo summarizes a company in one line from its public description
// fields only.
func cakeCompanyInfo(company cakeCompany) string {
	parts := []string{}
	for _, value := range []string{text(company.ProductsOrServices), foundedYear(company.FoundedYear), text(company.GeoFormattedAddr)} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "／")
}

// foundedYear accepts the year whether Cake renders it as a number or a string.
func foundedYear(value any) string {
	switch typed := value.(type) {
	case string:
		return text(typed)
	case float64:
		if typed <= 0 {
			return ""
		}
		return fmt.Sprintf("%d", int(typed))
	default:
		return ""
	}
}

// cakeIdentity builds the stable key of a Cake job. Cake has no numeric job id,
// so the company and job path segments together are the unique key, and the same
// pair is what the detail page yields.
func cakeIdentity(companyPath, jobPath string) (string, string, error) {
	companyPath, jobPath = strings.Trim(strings.TrimSpace(companyPath), "/"), strings.Trim(strings.TrimSpace(jobPath), "/")
	if companyPath == "" || jobPath == "" {
		return "", "", fmt.Errorf("company and job path segments are required")
	}
	return companyPath + "/" + jobPath, "https://www.cake.me/companies/" + companyPath + "/jobs/" + jobPath, nil
}

func cakeIdentityFromHref(href string) (string, string, error) {
	companyPath, jobPath := cakePathsFromURL(href)
	if companyPath == "" || jobPath == "" {
		return "", "", fmt.Errorf("job link %q has no /companies/{company}/jobs/{job} path", href)
	}
	return cakeIdentity(companyPath, jobPath)
}

// cakePathsFromURL reads the company and job segments out of a Cake job link.
func cakePathsFromURL(value string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 4 || segments[0] != "companies" || segments[2] != "jobs" {
		return "", ""
	}
	return segments[1], segments[3]
}

// sortRawJobs keeps the output of a map-backed snapshot deterministic.
func sortRawJobs(jobs []RawJob) {
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ExternalID < jobs[j].ExternalID })
}
