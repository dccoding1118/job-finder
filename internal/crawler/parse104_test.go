package crawler

import "testing"

func TestParseListItemsExcludesSponsoredAndReadsFields(t *testing.T) {
	items := []ListItem{
		{Href: "https://www.104.com.tw/job/ad11?jobsource=hotjob_chr_exp", Title: "Sponsored", CompanyName: "Ad Co", Location: "Taipei"},
		{Href: "https://www.104.com.tw/job/abc12?jobsource=joblist_new", Title: "Backend Engineer", CompanyName: "Example", CompanyInfo: "software", Location: "台北市", SalaryText: "月薪40,000~60,000元", Remote: true},
	}
	jobs, err := ParseListItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("parsed %d jobs, want the sponsored one dropped", len(jobs))
	}
	job := jobs[0]
	if job.ExternalID != "abc12" || job.URL != "https://www.104.com.tw/job/abc12" {
		t.Fatalf("identity = %q %q, want the tracking query stripped", job.ExternalID, job.URL)
	}
	if !job.Partial() {
		t.Fatal("list jobs must be partial")
	}
	if job.RemoteType != "remote" || job.SalaryMin == nil || *job.SalaryMin != 40000 || job.SalaryMax == nil || *job.SalaryMax != 60000 {
		t.Fatalf("unexpected job = %+v", job)
	}
}

func TestParseListItemsNegotiableSalaryIsNil(t *testing.T) {
	jobs, err := ParseListItems([]ListItem{{Href: "https://www.104.com.tw/job/z9", Title: "Engineer", CompanyName: "Example", Location: "台北市", SalaryText: "待遇面議"}})
	if err != nil {
		t.Fatal(err)
	}
	if jobs[0].SalaryMin != nil || jobs[0].SalaryMax != nil {
		t.Fatalf("negotiable salary parsed to %+v", jobs[0])
	}
}

func TestParseJobCaptureDecodesDescriptionAndSalary(t *testing.T) {
	posting := `[{"@type":"WebPage"},{"@type":"JobPosting","identifier":{"value":123},"mainEntityOfPage":{"@id":"https://www.104.com.tw/job/xyz88"},"title":"Platform Engineer","hiringOrganization":{"name":"Example"},"industry":"software","description":"&lt;p&gt;【工作內容】&lt;br&gt;Go 平台開發&lt;/p&gt;","jobLocation":{"address":{"addressLocality":"台北市"}},"baseSalary":{"value":{"minValue":"60000","maxValue":"90000","unitText":"MONTH"}},"jobLocationType":"TELECOMMUTE"}]`
	job, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/xyz88", JSONLD: []string{posting}})
	if err != nil {
		t.Fatal(err)
	}
	if job.ExternalID != "123" || job.URL != "https://www.104.com.tw/job/xyz88" {
		t.Fatalf("identity = %q %q", job.ExternalID, job.URL)
	}
	if job.Description == "" || job.Partial() {
		t.Fatalf("description not decoded: %q", job.Description)
	}
	if job.SalaryMin == nil || *job.SalaryMin != 60000 || job.SalaryMax == nil || *job.SalaryMax != 90000 {
		t.Fatalf("salary = %+v", job)
	}
	// TELECOMMUTE with no partial-remote wording is fully remote.
	if job.RemoteType != "remote" {
		t.Fatalf("remote type = %q, want remote", job.RemoteType)
	}
}

func TestParseJobCaptureNegotiableSalaryIsNil(t *testing.T) {
	posting := `{"@type":"JobPosting","title":"Engineer","hiringOrganization":{"name":"Example"},"industry":"software","description":"待遇面議，年薪 81~108 萬","jobLocation":{"address":{"addressLocality":"台北市"}},"mainEntityOfPage":{"@id":"https://www.104.com.tw/job/neg1"},"baseSalary":{"value":{"minValue":"40000","unitText":"MONTH"}}}`
	job, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/neg1", JSONLD: []string{posting}})
	if err != nil {
		t.Fatal(err)
	}
	if job.SalaryMin != nil || job.SalaryMax != nil {
		t.Fatalf("placeholder salary parsed to %+v", job)
	}
}

func TestParseJobCaptureReadsSingleValueRange(t *testing.T) {
	// Live 104 sometimes carries an explicit range in a single `value` string
	// instead of minValue/maxValue; a clear low~high pair there is trusted.
	posting := `{"@type":"JobPosting","title":"Engineer","hiringOrganization":{"name":"Example"},"industry":"software","description":"Go 平台開發","jobLocation":{"address":{"addressLocality":"台北市"}},"mainEntityOfPage":{"@id":"https://www.104.com.tw/job/val1"},"baseSalary":{"value":{"value":"50000~70000","unitText":"MONTH"}}}`
	job, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/val1", JSONLD: []string{posting}})
	if err != nil {
		t.Fatal(err)
	}
	if job.SalaryMin == nil || *job.SalaryMin != 50000 || job.SalaryMax == nil || *job.SalaryMax != 70000 {
		t.Fatalf("salary = %+v, want 50000~70000 from value.value", job)
	}
}

func TestParseJobCaptureSingleValueFloorIsNil(t *testing.T) {
	// A lone floor figure in `value` (negotiable placeholder) is left unknown,
	// like the list-tag rule; only an explicit range is trusted.
	posting := `{"@type":"JobPosting","title":"Engineer","hiringOrganization":{"name":"Example"},"industry":"software","description":"待遇面議","jobLocation":{"address":{"addressLocality":"台北市"}},"mainEntityOfPage":{"@id":"https://www.104.com.tw/job/val2"},"baseSalary":{"value":{"value":"40000元以上","unitText":"MONTH"}}}`
	job, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/val2", JSONLD: []string{posting}})
	if err != nil {
		t.Fatal(err)
	}
	if job.SalaryMin != nil || job.SalaryMax != nil {
		t.Fatalf("single floor value parsed to %+v", job)
	}
}

func TestParseJobCapturePartialRemoteIsHybrid(t *testing.T) {
	posting := `{"@type":"JobPosting","title":"Engineer","hiringOrganization":{"name":"Example"},"industry":"software","description":"每月 10 天居家辦公","jobLocation":{"address":{"addressLocality":"台北市"}},"mainEntityOfPage":{"@id":"https://www.104.com.tw/job/hy1"},"jobLocationType":"TELECOMMUTE"}`
	job, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/hy1", JSONLD: []string{posting}})
	if err != nil {
		t.Fatal(err)
	}
	if job.RemoteType != "hybrid" {
		t.Fatalf("remote type = %q, want hybrid", job.RemoteType)
	}
}

func TestParseJobCaptureFallsBackToDOM(t *testing.T) {
	job, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/dom1", DOM: &JobDOM{Title: "Engineer", CompanyName: "Example", Location: "台北市", Description: "Go 平台開發"}})
	if err != nil {
		t.Fatal(err)
	}
	if job.ExternalID != "dom1" || job.Partial() {
		t.Fatalf("dom job = %+v", job)
	}
}

func TestParseJobCaptureMissingContentIsError(t *testing.T) {
	if _, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/none"}); err == nil {
		t.Fatal("a page with neither JSON-LD nor DOM material was accepted")
	}
	// A JobPosting whose description decodes to nothing is an error, not a
	// silent skip.
	posting := `{"@type":"JobPosting","title":"Engineer","hiringOrganization":{"name":"Example"},"description":"","jobLocation":{"address":{"addressLocality":"台北市"}},"mainEntityOfPage":{"@id":"https://www.104.com.tw/job/empty"}}`
	if _, err := ParseJobCapture(JobCapture{URL: "https://www.104.com.tw/job/empty", JSONLD: []string{posting}}); err == nil {
		t.Fatal("an empty description was accepted")
	}
}
