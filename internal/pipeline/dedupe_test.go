package pipeline

import (
	"context"
	"testing"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/store"
)

func dedupePipeline(t *testing.T, enabled bool) (Pipeline, *store.Store) {
	t.Helper()
	p, db := openPipeline(t, Filter{Locations: []string{"Taipei", "台北"}})
	p.DedupeEnabled = enabled
	return p, db
}

func cakeRow(externalID, title, company, location, description string) crawler.RawJob {
	return crawler.RawJob{
		Source: "cake", ExternalID: externalID, URL: "https://www.cake.me/companies/example/jobs/" + externalID,
		Title: title, CompanyName: company, CompanyInfo: "public listing",
		Description: description, Location: location, RemoteType: "unknown",
	}
}

func job104Row(externalID, title, company, location, description string) crawler.RawJob {
	return crawler.RawJob{
		Source: "104", ExternalID: externalID, URL: "https://www.104.com.tw/job/" + externalID,
		Title: title, CompanyName: company, CompanyInfo: "software",
		Description: description, Location: location, RemoteType: "onsite",
	}
}

// PT-70 / PT-71: a captured job that another source already carries answers with
// the canonical copy's id and verdict, and costs no new work.
func TestIngestReportsCanonicalOfMergedGroup(t *testing.T) {
	p, db := dedupePipeline(t, true)
	ctx := context.Background()
	full, err := p.IngestJob(ctx, job104Row("a1", "Senior Backend Engineer", "Example Co., Ltd.", "台北市", "合成職缺說明"))
	if err != nil {
		t.Fatal(err)
	}
	if full.ProcessState != "queued" {
		t.Fatalf("104 capture = %+v", full)
	}
	results, err := p.IngestList(ctx, []crawler.RawJob{cakeRow("c1", "資深後端工程師", "Example 股份有限公司", "台北市", "")})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].JobID != full.JobID || results[0].ProcessState != "queued" {
		t.Fatalf("cake list capture = %+v, want the canonical job %d", results[0], full.JobID)
	}
	// Capturing the alias's own detail page still answers with the canonical copy.
	again, err := p.IngestJob(ctx, cakeRow("c1", "資深後端工程師", "Example 股份有限公司", "台北市", "合成職缺說明（Cake 版）"))
	if err != nil {
		t.Fatal(err)
	}
	if again.JobID != full.JobID {
		t.Fatalf("cake detail capture = %+v, want the canonical job %d", again, full.JobID)
	}
	jobs, err := db.ListJobs(ctx, store.JobFilter{}, store.JobSortNewest)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("listed %d jobs, want only the canonical one", len(jobs))
	}
}

// PT-72: no stage ever picks up an alias.
func TestMergedAliasIsNotPickedByAnyStage(t *testing.T) {
	p, db := dedupePipeline(t, true)
	ctx := context.Background()
	if _, err := p.IngestJob(ctx, job104Row("a1", "Backend Engineer", "Example", "台北市", "合成職缺說明")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.IngestJob(ctx, cakeRow("c1", "Backend Engineer", "Example", "台北市", "合成職缺說明")); err != nil {
		t.Fatal(err)
	}
	counts, err := db.CountJobsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts["merged"] != 1 {
		t.Fatalf("merged aliases = %d, want 1", counts["merged"])
	}
	for _, stage := range []string{"filter", "score", "letter"} {
		picked, err := db.PickForStage(ctx, stage, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range picked {
			if job.ProcessState == "merged" {
				t.Fatalf("stage %q picked an alias", stage)
			}
		}
	}
}

// PT-73: with grouping off, each source keeps its own job and its own verdict.
func TestDedupeDisabledKeepsSourcesIndependent(t *testing.T) {
	p, db := dedupePipeline(t, false)
	ctx := context.Background()
	first, err := p.IngestJob(ctx, job104Row("a1", "Backend Engineer", "Example", "台北市", "合成職缺說明"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.IngestJob(ctx, cakeRow("c1", "Backend Engineer", "Example", "台北市", "合成職缺說明"))
	if err != nil {
		t.Fatal(err)
	}
	if first.JobID == second.JobID {
		t.Fatal("the two captures must stay separate jobs")
	}
	jobs, err := db.ListJobs(ctx, store.JobFilter{}, store.JobSortNewest)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("listed %d jobs, want both sources", len(jobs))
	}
	candidates, err := db.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("grouping is off but registered %+v", candidates)
	}
}
