package crawler

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The 104 parser has no fetching side: 104 is a semi-passive source whose pages
// only reach this package as material the extension captured from what the user
// themself browsed (see design-extension §4).

// ListItem is one visible entry of a 104 search or notification list, as the
// content script read it off the page.
type ListItem struct {
	// Href is the item link, tracking query included.
	Href string
	// Title comes from the link's title attribute: the node text is broken up
	// by keyword highlight spans.
	Title       string
	CompanyName string
	CompanyInfo string
	Location    string
	// SalaryText is the salary tag verbatim, e.g. "月薪40,000~60,000元" or
	// "待遇面議".
	SalaryText string
	// Remote reports a remote-work tag on the item.
	Remote bool
}

// JobDOM carries the detail-page fields read from the DOM. It is the fallback
// for pages without JSON-LD.
type JobDOM struct {
	Title, CompanyName, CompanyInfo, Location, Description, SalaryText string
	Remote                                                             bool
}

// JobCapture is the material of one 104 detail page.
type JobCapture struct {
	// URL is the page the user had open.
	URL string
	// JSONLD holds the raw contents of the page's ld+json script blocks.
	JSONLD []string
	// DOM is used only when the page carries no JSON-LD at all.
	DOM *JobDOM
}

var (
	jobIDPattern     = regexp.MustCompile(`/job/([A-Za-z0-9]+)`)
	monthlyRange     = regexp.MustCompile(`月薪\s*([\d,]+)\s*[~～\-–—至]\s*([\d,]+)\s*元`)
	amountRange      = regexp.MustCompile(`([\d,]+)\s*[~～\-–—至]\s*([\d,]+)`)
	monthlyFloor     = regexp.MustCompile(`月薪\s*([\d,]+)\s*元(以上)?`)
	lineBreakTags    = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|li|tr|h[1-6])>`)
	htmlTagPattern   = regexp.MustCompile(`<[^>]*>`)
	blankLinePattern = regexp.MustCompile(`\n{3,}`)
	partialRemote    = []string{"每月", "天", "部分", "混合"}
)

// ParseListItems normalizes captured list entries into partial jobs. List pages
// carry no JD, so every job it returns has a nil description and is stored as
// `discovered`.
//
// An entry the page does not describe well enough is skipped rather than failing
// the capture: one such card on screen must not cost every other item on that
// page its mark (see design-crawler §2).
func ParseListItems(items []ListItem) ([]RawJob, error) {
	jobs := make([]RawJob, 0, len(items))
	for _, item := range items {
		if sponsored(item.Href) {
			continue
		}
		id, link, err := jobIdentity(item.Href)
		if err != nil {
			continue
		}
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.CompanyName) == "" || strings.TrimSpace(item.Location) == "" {
			continue
		}
		remote := "unknown"
		if item.Remote {
			remote = "remote"
		}
		min, max := parse104Salary(item.SalaryText)
		jobs = append(jobs, RawJob{
			Source: "104", ExternalID: id, URL: link,
			Title: text(item.Title), CompanyName: text(item.CompanyName), CompanyInfo: text(item.CompanyInfo),
			Location: text(item.Location), RemoteType: remote, SalaryMin: min, SalaryMax: max,
		})
	}
	return jobs, nil
}

// ParseJobCapture turns one captured detail page into a full job. The JSON-LD
// JobPosting is authoritative; a malformed one is an error rather than a silent
// fallback, because only a missing block justifies reading the DOM.
func ParseJobCapture(capture JobCapture) (RawJob, error) {
	posting, found, err := findJobPosting(capture.JSONLD)
	if err != nil {
		return RawJob{}, err
	}
	if !found {
		return jobFromDOM(capture)
	}
	description, err := decodeJobDescription(posting.Description)
	if err != nil {
		return RawJob{}, err
	}
	link := strings.TrimSpace(posting.MainEntityOfPage.ID)
	if link == "" {
		link = capture.URL
	}
	id, link, err := jobIdentity(link)
	if err != nil {
		return RawJob{}, err
	}
	if value := strings.TrimSpace(string(posting.Identifier.Value)); value != "" {
		id = value
	}
	job := RawJob{
		Source: "104", ExternalID: id, URL: link,
		Title: text(posting.Title), CompanyName: text(posting.HiringOrganization.Name), CompanyInfo: text(posting.Industry),
		Description: description, Location: text(posting.JobLocation.Address.AddressLocality),
		RemoteType: postingRemoteType(posting.JobLocationType, description),
	}
	job.SalaryMin, job.SalaryMax = postingSalary(posting)
	if job.Title == "" || job.CompanyName == "" || job.Location == "" {
		return RawJob{}, fmt.Errorf("104 job %s: JobPosting is missing title, company, or location", id)
	}
	if job.CompanyInfo == "" {
		job.CompanyInfo = "public listing"
	}
	return job, nil
}

func jobFromDOM(capture JobCapture) (RawJob, error) {
	if capture.DOM == nil {
		return RawJob{}, fmt.Errorf("104 job: the page carries neither a JobPosting nor DOM material")
	}
	id, link, err := jobIdentity(capture.URL)
	if err != nil {
		return RawJob{}, err
	}
	description, err := decodeJobDescription(capture.DOM.Description)
	if err != nil {
		return RawJob{}, err
	}
	dom := capture.DOM
	if strings.TrimSpace(dom.Title) == "" || strings.TrimSpace(dom.CompanyName) == "" || strings.TrimSpace(dom.Location) == "" {
		return RawJob{}, fmt.Errorf("104 job %s: DOM material is missing title, company, or location", id)
	}
	remote := "onsite"
	if dom.Remote {
		remote = "remote"
	}
	min, max := parse104Salary(dom.SalaryText)
	info := text(dom.CompanyInfo)
	if info == "" {
		info = "public listing"
	}
	return RawJob{
		Source: "104", ExternalID: id, URL: link,
		Title: text(dom.Title), CompanyName: text(dom.CompanyName), CompanyInfo: info,
		Description: description, Location: text(dom.Location), RemoteType: remote,
		SalaryMin: min, SalaryMax: max,
	}, nil
}

// looseString accepts the identifier value whether 104 renders it as a string
// or a number.
type looseString string

func (s *looseString) UnmarshalJSON(data []byte) error {
	value := strings.TrimSpace(string(data))
	if value == "null" {
		return nil
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		*s = looseString(unquoted)
		return nil
	}
	*s = looseString(value)
	return nil
}

type jobPosting struct {
	Type       json.RawMessage `json:"@type"`
	Identifier struct {
		Value looseString `json:"value"`
	} `json:"identifier"`
	MainEntityOfPage struct {
		ID string `json:"@id"`
	} `json:"mainEntityOfPage"`
	Title              string `json:"title"`
	HiringOrganization struct {
		Name string `json:"name"`
	} `json:"hiringOrganization"`
	Industry    string `json:"industry"`
	Description string `json:"description"`
	JobLocation struct {
		Address struct {
			AddressLocality string `json:"addressLocality"`
		} `json:"address"`
	} `json:"jobLocation"`
	BaseSalary struct {
		Value struct {
			// Value carries the figure when 104 renders a single scalar instead
			// of a min/max pair; only an explicit range in it is trusted.
			Value    looseString `json:"value"`
			MinValue looseString `json:"minValue"`
			MaxValue looseString `json:"maxValue"`
			UnitText string      `json:"unitText"`
		} `json:"value"`
	} `json:"baseSalary"`
	JobLocationType string `json:"jobLocationType"`
}

func findJobPosting(blocks []string) (jobPosting, bool, error) {
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var document json.RawMessage
		if err := json.Unmarshal([]byte(block), &document); err != nil {
			return jobPosting{}, false, fmt.Errorf("104 job: decode JSON-LD: %w", err)
		}
		posting, found, err := searchJobPosting(document)
		if err != nil || found {
			return posting, found, err
		}
	}
	return jobPosting{}, false, nil
}

func searchJobPosting(document json.RawMessage) (jobPosting, bool, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(document, &list); err == nil {
		for _, entry := range list {
			posting, found, err := searchJobPosting(entry)
			if err != nil || found {
				return posting, found, err
			}
		}
		return jobPosting{}, false, nil
	}
	var posting jobPosting
	if err := json.Unmarshal(document, &posting); err != nil {
		return jobPosting{}, false, nil
	}
	if !isJobPosting(posting.Type) {
		return jobPosting{}, false, nil
	}
	return posting, true, nil
}

func isJobPosting(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == "JobPosting"
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, value := range many {
			if value == "JobPosting" {
				return true
			}
		}
	}
	return false
}

// decodeJobDescription undoes the two escaping layers 104 applies, strips the
// markup, and normalizes the whitespace; the database keeps plain text only.
func decodeJobDescription(value string) (string, error) {
	decoded := html.UnescapeString(value)
	decoded = lineBreakTags.ReplaceAllString(decoded, "\n")
	decoded = htmlTagPattern.ReplaceAllString(decoded, "")
	decoded = html.UnescapeString(decoded)
	lines := strings.Split(strings.ReplaceAll(decoded, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
	}
	decoded = blankLinePattern.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	decoded = strings.TrimSpace(decoded)
	if decoded == "" {
		return "", fmt.Errorf("104 job: description decoded to empty text")
	}
	return decoded, nil
}

// postingSalary parses only an explicit monthly range. A negotiable listing
// carries a placeholder floor that contradicts its own JD, so anything else is
// left unknown rather than guessed into a salary screening hit.
func postingSalary(posting jobPosting) (*int, *int) {
	if !strings.EqualFold(posting.BaseSalary.Value.UnitText, "MONTH") {
		return nil, nil
	}
	// Standard schema.org shape: an explicit min/max pair.
	min, minOK := amount(string(posting.BaseSalary.Value.MinValue))
	max, maxOK := amount(string(posting.BaseSalary.Value.MaxValue))
	if minOK && maxOK && max >= min {
		return &min, &max
	}
	// 104's live detail pages sometimes carry the figure in a single `value`
	// string (e.g. "50000~70000" or "40000元以上") rather than min/max. Only an
	// explicit range there is trusted; a lone figure or a negotiable floor stays
	// unknown rather than being guessed into a salary screening hit.
	return salaryRange(string(posting.BaseSalary.Value.Value))
}

// salaryRange reads an explicit low~high pair out of a free-form salary string,
// returning unknown for anything that is not a clear range.
func salaryRange(value string) (*int, *int) {
	m := amountRange.FindStringSubmatch(strings.ReplaceAll(text(value), " ", ""))
	if len(m) != 3 {
		return nil, nil
	}
	min, minOK := amount(m[1])
	max, maxOK := amount(m[2])
	if !minOK || !maxOK || max < min {
		return nil, nil
	}
	return &min, &max
}

// postingRemoteType reads jobLocationType against the JD, because 104 marks
// listings that are only partly remote as TELECOMMUTE too.
func postingRemoteType(locationType, description string) string {
	if !strings.EqualFold(locationType, "TELECOMMUTE") {
		return "onsite"
	}
	for _, term := range partialRemote {
		if strings.Contains(description, term) {
			return "hybrid"
		}
	}
	return "remote"
}

func parse104Salary(value string) (*int, *int) {
	value = strings.ReplaceAll(text(value), " ", "")
	if m := monthlyRange.FindStringSubmatch(value); len(m) == 3 {
		min, minOK := amount(m[1])
		max, maxOK := amount(m[2])
		if minOK && maxOK && max >= min {
			return &min, &max
		}
		return nil, nil
	}
	if m := monthlyFloor.FindStringSubmatch(value); len(m) == 3 {
		if min, ok := amount(m[1]); ok {
			return &min, nil
		}
	}
	return nil, nil
}

func amount(value string) (int, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" {
		return 0, false
	}
	if dot := strings.Index(value, "."); dot >= 0 {
		value = value[:dot]
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

// jobIdentity derives the external id from the /job/{id} path segment and drops
// the tracking query, so the same job stays one row whatever link led to it.
func jobIdentity(href string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", "", fmt.Errorf("invalid job link: %w", err)
	}
	match := jobIDPattern.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return "", "", fmt.Errorf("job link %q has no /job/{id} segment", href)
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	if parsed.Host == "" {
		parsed.Scheme, parsed.Host = "https", "www.104.com.tw"
	}
	return match[1], parsed.String(), nil
}

// sponsored reports the ad slot 104 renders above the search results.
func sponsored(href string) bool {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return false
	}
	return strings.HasPrefix(parsed.Query().Get("jobsource"), "hotjob")
}

func text(value string) string { return strings.Join(strings.Fields(html.UnescapeString(value)), " ") }
