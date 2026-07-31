package crawler

import (
	"strings"
	"testing"

	"github.com/dccoding1118/job-finder/internal/store"
)

// cakeListData is a structure-faithful, content-synthetic __NEXT_DATA__ of a Cake
// list page: two listings of one company, one of them paying in USD per year.
const cakeListData = `{"props":{"pageProps":{"ssr":{"search":{"query":"backend","page":1,"filters":{}},"isInfiniteScroll":false},"initialState":{"jobSearch":{"entityByPathId":{
	"example-cloud:senior-backend-engineer":{"path":"senior-backend-engineer","title":"Senior Backend Engineer","highlightedTitle":"Senior <em>Backend</em> Engineer","description":"synthetic summary","highlightedDescription":"synthetic <em>summary</em>","locations":["台北市"],"salary":{"min":80000,"max":120000,"currency":"TWD","type":"per_month"},"page":{"path":"example-cloud","name":"Example Cloud","highlightedName":"Example <em>Cloud</em>","geo":"Taipei","country":"Taiwan"}},
	"example-cloud:platform-intern":{"path":"platform-intern","title":"Platform Intern","locations":["台北市"],"salary":{"min":60000,"max":null,"currency":"USD","type":"per_year"},"page":{"path":"example-cloud","name":"Example Cloud","geo":"Taipei","country":"Taiwan"}}
}}}}}}`

// cakeJobData is a structure-faithful, content-synthetic detail page. The company
// block deliberately carries contact fields, which must not reach any job field.
const cakeJobData = `{"props":{"pageProps":{
	"job":{"path":"senior-backend-engineer","title":"Senior Backend Engineer","description":"<p>設計並維護合成服務</p><ul><li>Go 平台開發</li></ul>","requirements":"<p>三年以上合成經驗</p>","interview_process":"","locations":[{"full_name":"台北市, 台灣","full_name_en":"Taipei City, Taiwan"},{"full_name":"新竹市, 台灣","full_name_en":"Hsinchu City, Taiwan"}],
		"salary_min":80000,"salary_max":120000,"salary_type":"per_month","salary_currency":"TWD","hide_salary_completely":false,"hide_salary_max":false,"remote":"partial_remote_work","aasm_state":"published"},
	"company":{"path":"example-cloud","name":"Example Cloud","products_or_services":"合成雲端平台","founded_year":2015,"geo_formatted_address":"台北市合成路 1 號","geo_state_name_l":"臺北市","geo_city_l":"內湖區","geo_state_name":"Taipei City","geo_city":"Neihu District","contact_name":"合成聯絡人","email":"contact@example.test","phone":"0912345678"}
}}}`

// CT-50 / CT-51 / CT-52: the embedded list state maps onto partial jobs, the
// highlighted fields are ignored, and a non-TWD, non-monthly salary is dropped.
func TestParseCakeListMapsEmbeddedState(t *testing.T) {
	jobs, err := ParseCakeList(CakeCapture{URL: "https://www.cake.me/jobs?query=backend&page=1", NextData: cakeListData})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("parsed %d jobs, want 2", len(jobs))
	}
	senior := jobs[1]
	if senior.ExternalID != "example-cloud/senior-backend-engineer" {
		t.Fatalf("external id = %q", senior.ExternalID)
	}
	if senior.URL != "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer" {
		t.Fatalf("url = %q", senior.URL)
	}
	if senior.Source != "cake" || senior.Title != "Senior Backend Engineer" || senior.CompanyName != "Example Cloud" {
		t.Fatalf("unexpected fields: %+v", senior)
	}
	if !senior.Partial() {
		t.Fatal("a list item must never carry a JD")
	}
	if senior.Location != "台北市" || senior.RemoteType != "unknown" {
		t.Fatalf("location/remote = %q/%q", senior.Location, senior.RemoteType)
	}
	if senior.SalaryMin == nil || *senior.SalaryMin != 80000 || senior.SalaryMax == nil || *senior.SalaryMax != 120000 {
		t.Fatalf("salary = %+v", senior)
	}
	intern := jobs[0]
	if intern.SalaryMin != nil || intern.SalaryMax != nil {
		t.Fatalf("a USD yearly salary must not be parsed: %+v", intern)
	}
}

// ET-41's server side: a stale snapshot is recognized, and the DOM harvest the
// content script falls back to maps onto the same shape.
func TestCakeSnapshotFreshnessAndDOMHarvest(t *testing.T) {
	if !CakeSnapshotFresh(cakeListData, "?page=1&query=backend") {
		t.Fatal("the same conditions in another order must read as fresh")
	}
	if CakeSnapshotFresh(cakeListData, "?query=backend&page=2") {
		t.Fatal("another page must read as stale")
	}
	// A condition the snapshot cannot be compared against reads as stale: only
	// `query` and `page` are recognized, so a filtered URL falls back to the DOM.
	if CakeSnapshotFresh(cakeListData, "?query=backend&page=1&profession%5B%5D=it_back-end-engineer") {
		t.Fatal("a filtered URL must read as stale")
	}
	jobs, err := ParseCakeList(CakeCapture{URL: "https://www.cake.me/jobs?query=backend&page=2", Items: []CakeListItem{
		{Href: "/companies/example-cloud/jobs/senior-backend-engineer", Title: "Senior Backend Engineer", CompanyName: "Example Cloud", Location: "台北市", SalaryText: "月薪 80,000 ~ 120,000 元"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ExternalID != "example-cloud/senior-backend-engineer" || !jobs[0].Partial() {
		t.Fatalf("DOM harvest = %+v", jobs)
	}
	if jobs[0].SalaryMin == nil || *jobs[0].SalaryMin != 80000 {
		t.Fatalf("DOM salary = %+v", jobs[0])
	}
}

// CT-53 / CT-55 / CT-56: the three HTML sections become one plain-text JD, the
// remote field maps, and no contact field reaches the job.
func TestParseCakeJobJoinsSectionsAndDropsContactFields(t *testing.T) {
	job, err := ParseCakeJob(CakeCapture{URL: "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer", NextData: cakeJobData})
	if err != nil {
		t.Fatal(err)
	}
	if job.ExternalID != "example-cloud/senior-backend-engineer" {
		t.Fatalf("external id = %q, want the list item's key", job.ExternalID)
	}
	if job.Partial() {
		t.Fatal("a detail capture must carry the JD")
	}
	for _, want := range []string{"工作內容", "設計並維護合成服務", "Go 平台開發", "條件要求", "三年以上合成經驗"} {
		if !strings.Contains(job.Description, want) {
			t.Fatalf("description is missing %q: %q", want, job.Description)
		}
	}
	if strings.Contains(job.Description, "<") || strings.Contains(job.Description, "面試流程") {
		t.Fatalf("description kept markup or an empty section: %q", job.Description)
	}
	if job.RemoteType != "hybrid" {
		t.Fatalf("remote type = %q, want hybrid", job.RemoteType)
	}
	if job.Location != "台北市, 台灣、新竹市, 台灣" {
		t.Fatalf("location = %q", job.Location)
	}
	for _, forbidden := range []string{"合成聯絡人", "contact@example.test", "0912345678"} {
		for _, field := range []string{job.CompanyInfo, job.Description, job.Title, job.CompanyName, job.Location} {
			if strings.Contains(field, forbidden) {
				t.Fatalf("%q leaked into %q", forbidden, field)
			}
		}
	}
	if !strings.Contains(job.CompanyInfo, "合成雲端平台") || !strings.Contains(job.CompanyInfo, "2015") {
		t.Fatalf("company info = %q", job.CompanyInfo)
	}
}

// A Cake listing often carries no location of its own; the company's city is
// what keeps it assessable instead of being screened out as unknown.
func TestParseCakeJobFallsBackToTheCompanyCity(t *testing.T) {
	data := strings.ReplaceAll(cakeJobData, `"locations":[{"full_name":"台北市, 台灣","full_name_en":"Taipei City, Taiwan"},{"full_name":"新竹市, 台灣","full_name_en":"Hsinchu City, Taiwan"}]`, `"locations":[]`)
	job, err := ParseCakeJob(CakeCapture{URL: "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer", NextData: data})
	if err != nil {
		t.Fatal(err)
	}
	if job.Location != "臺北市內湖區" {
		t.Fatalf("location = %q, want the company city", job.Location)
	}
	if strings.Contains(job.Location, "合成路") {
		t.Fatalf("a street address must not become the location: %q", job.Location)
	}
}

func TestParseCakeJobRemoteMapping(t *testing.T) {
	for value, want := range map[string]string{"no_remote_work": "onsite", "partial_remote_work": "hybrid", "full_remote_work": "remote", "surprise_value": "unknown"} {
		data := strings.ReplaceAll(cakeJobData, `"remote":"partial_remote_work"`, `"remote":"`+value+`"`)
		job, err := ParseCakeJob(CakeCapture{URL: "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer", NextData: data})
		if err != nil {
			t.Fatal(err)
		}
		if job.RemoteType != want {
			t.Fatalf("remote %q mapped to %q, want %q", value, job.RemoteType, want)
		}
	}
}

// CT-54: a hidden salary is unknown, never read off the underlying figure.
func TestParseCakeJobHonorsHiddenSalary(t *testing.T) {
	hidden := strings.ReplaceAll(cakeJobData, `"hide_salary_completely":false`, `"hide_salary_completely":true`)
	job, err := ParseCakeJob(CakeCapture{URL: "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer", NextData: hidden})
	if err != nil {
		t.Fatal(err)
	}
	if job.SalaryMin != nil || job.SalaryMax != nil {
		t.Fatalf("a completely hidden salary parsed to %+v", job)
	}
	maxHidden := strings.ReplaceAll(cakeJobData, `"hide_salary_max":false`, `"hide_salary_max":true`)
	job, err = ParseCakeJob(CakeCapture{URL: "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer", NextData: maxHidden})
	if err != nil {
		t.Fatal(err)
	}
	if job.SalaryMin == nil || *job.SalaryMin != 80000 || job.SalaryMax != nil {
		t.Fatalf("a hidden maximum parsed to %+v", job)
	}
}

func ptr[T any](value T) *T { return &value }

// cakeJobDOM is one detail page as the content script reads it off the rendered
// page, which is the only source of a Cake JD.
func cakeJobDOM() CakeJobDOM {
	return CakeJobDOM{
		Title:       "Software Engineer (AI Solution / MCP Focus)",
		CompanyName: "InAddition Consultants Ltd.",
		Sections: []CakeJobSection{
			{Title: "職缺描述", Body: "核心任務是打通 LLM 與應用程式之間的溝通橋樑。"},
			{Title: "職務需求", Body: "熟悉 Java 後端生態系。"},
			{Title: "面試流程", Body: "電話訪談、實體面試。"},
		},
		Meta: []string{"職缺 6 天前更新", "台北市大安區", "月薪 TWD 70,000 ~ 100,000 元", "部分遠端工作"},
	}
}

// CT-62: the rendered detail page becomes a full job, and the metadata lines are
// recognized without depending on their order or position.
func TestParseCakeJobReadsTheRenderedPage(t *testing.T) {
	job, err := ParseCakeJob(CakeCapture{
		URL:    "https://www.cake.me/companies/inaddition-consultants/jobs/software-engineer-ai-solution-mcp-focus",
		JobDOM: ptr(cakeJobDOM()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.ExternalID != "inaddition-consultants/software-engineer-ai-solution-mcp-focus" {
		t.Fatalf("external id = %q, want the URL's key", job.ExternalID)
	}
	if job.Partial() {
		t.Fatal("a detail capture must carry the JD")
	}
	for _, want := range []string{"職缺描述", "溝通橋樑", "職務需求", "Java 後端生態系", "面試流程"} {
		if !strings.Contains(job.Description, want) {
			t.Fatalf("description is missing %q: %q", want, job.Description)
		}
	}
	if job.Title != "Software Engineer (AI Solution / MCP Focus)" || job.CompanyName != "InAddition Consultants Ltd." {
		t.Fatalf("title = %q, company = %q", job.Title, job.CompanyName)
	}
	if job.Location != "台北市大安區" {
		t.Fatalf("location = %q, want the metadata line that names a place", job.Location)
	}
	if job.RemoteType != "hybrid" {
		t.Fatalf("remote type = %q, want hybrid", job.RemoteType)
	}
	if job.SalaryMin == nil || *job.SalaryMin != 70000 || job.SalaryMax == nil || *job.SalaryMax != 100000 {
		t.Fatalf("salary = %+v / %+v", job.SalaryMin, job.SalaryMax)
	}
}

// CT-63: metadata the page does not carry leaves the job assessable rather than
// screened out on a guess, and a page without a JD is refused.
func TestParseCakeJobDOMTreatsMissingMetadataAsUnknown(t *testing.T) {
	bare := cakeJobDOM()
	bare.Meta = []string{"職缺 6 天前更新", "雇主活躍於 2 天前"}
	job, err := ParseCakeJob(CakeCapture{URL: "https://www.cake.me/companies/example-cloud/jobs/x", JobDOM: &bare})
	if err != nil {
		t.Fatal(err)
	}
	if job.Location != store.LocationUnknown || job.RemoteType != "unknown" || job.SalaryMin != nil || job.SalaryMax != nil {
		t.Fatalf("bare metadata produced %+v", job)
	}

	for name, dom := range map[string]CakeJobDOM{
		"no sections": {Title: "T", CompanyName: "C"},
		"empty bodies": {Title: "T", CompanyName: "C", Sections: []CakeJobSection{
			{Title: "職缺描述", Body: "   "},
		}},
		"no title":   {CompanyName: "C", Sections: cakeJobDOM().Sections},
		"no company": {Title: "T", Sections: cakeJobDOM().Sections},
	} {
		if _, err := ParseCakeJob(CakeCapture{URL: "https://www.cake.me/companies/example-cloud/jobs/x", JobDOM: ptr(dom)}); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// CT-57: missing, malformed, job-less, and closed material all report an error
// rather than a guess.
func TestParseCakeJobRejectsUnusableMaterial(t *testing.T) {
	closed := strings.ReplaceAll(cakeJobData, `"aasm_state":"published"`, `"aasm_state":"closed"`)
	for name, capture := range map[string]CakeCapture{
		"missing":   {URL: "https://www.cake.me/companies/example-cloud/jobs/x"},
		"malformed": {URL: "https://www.cake.me/companies/example-cloud/jobs/x", NextData: "{not json"},
		"job-less":  {URL: "https://www.cake.me/companies/example-cloud/jobs/x", NextData: `{"props":{"pageProps":{}}}`},
		"closed":    {URL: "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer", NextData: closed},
	} {
		if _, err := ParseCakeJob(capture); err == nil {
			t.Fatalf("%s material was accepted", name)
		}
	}
	if _, err := ParseCakeList(CakeCapture{URL: "https://www.cake.me/jobs", NextData: "{not json"}); err == nil {
		t.Fatal("malformed list material was accepted")
	}
}

// One unusable entry must not cost the rest of the page its marks: the capture
// answers for the items it could read.
// A Cake entry often states no locality at all. The job still needs one, so it
// gets the value that says exactly that — and never a locality that would be
// screened against the Profile as though the source had stated it.
func TestParseCakeListMarksAMissingLocationAsUnknown(t *testing.T) {
	jobs, err := ParseCakeList(CakeCapture{URL: "https://www.cake.me/jobs?query=backend&page=2", Items: []CakeListItem{
		{Href: "/companies/example-cloud/jobs/senior-backend-engineer", Title: "Senior Backend Engineer", CompanyName: "Example Cloud"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Location != store.LocationUnknown {
		t.Fatalf("DOM harvest without a location = %+v", jobs)
	}
}

func TestParseCakeListSkipsUnusableEntries(t *testing.T) {
	data := strings.ReplaceAll(cakeListData, `"title":"Platform Intern"`, `"title":""`)
	jobs, err := ParseCakeList(CakeCapture{URL: "https://www.cake.me/jobs?query=backend&page=1", NextData: data})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ExternalID != "example-cloud/senior-backend-engineer" {
		t.Fatalf("parsed %+v, want only the readable entry", jobs)
	}
	harvest, err := ParseCakeList(CakeCapture{URL: "https://www.cake.me/jobs?query=backend&page=2", Items: []CakeListItem{
		{Href: "/companies/example-cloud/jobs/senior-backend-engineer", Title: "Senior Backend Engineer", CompanyName: "Example Cloud", Location: "台北市"},
		{Href: "/companies/example-cloud/jobs/platform-intern", Title: "Platform Intern", CompanyName: ""},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(harvest) != 1 {
		t.Fatalf("DOM harvest = %+v, want only the readable item", harvest)
	}
}
