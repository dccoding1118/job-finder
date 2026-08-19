package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/store"
)

func fetchRawJob(externalID, title string) crawler.RawJob {
	return crawler.RawJob{
		Source: "yourator", ExternalID: externalID, URL: "https://example.test/" + externalID,
		Title: title, CompanyName: "Example", CompanyInfo: "平台團隊", Description: "Synthetic platform work",
		Location: "台北市", RemoteType: "onsite",
	}
}

// A fetch runs for many minutes. Storing everything at the end would leave the
// database untouched for all of it, which is exactly what a dead process looks
// like — so each batch has to land, and be reported, as it arrives.
func TestFetchStoresAndReportsEachBatchAsItArrives(t *testing.T) {
	ctx := context.Background()
	data, err := store.Open(filepath.Join(t.TempDir(), "fetch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()

	stored := []int{}
	p := Pipeline{Store: data, Source: mockSource{jobs: []crawler.RawJob{fetchRawJob("a1", "Backend Engineer"), fetchRawJob("a2", "Platform Engineer")}}}
	p.Progress = func(ctx context.Context, _ crawler.Batch, progress FetchStats) error {
		count, countErr := data.CountJobsByState(ctx)
		if countErr != nil {
			return countErr
		}
		total := 0
		for _, value := range count {
			total += value
		}
		stored = append(stored, total)
		if total != progress.Fetched {
			t.Fatalf("progress reported %d fetched while %d jobs were stored", progress.Fetched, total)
		}
		return nil
	}
	stats, err := p.Fetch(ctx, crawler.SearchSpec{Queries: []crawler.SearchQuery{{Direction: "P1", Keywords: []string{"platform"}}}, MaxPages: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Fetched != 2 || stats.New != 2 {
		t.Fatalf("expected two fetched and two new jobs, got %+v", stats)
	}
	if len(stored) != 2 || stored[0] != 1 || stored[1] != 2 {
		t.Fatalf("each batch must be stored before the next is fetched, saw %v", stored)
	}
}

// An emit error is the caller saying it cannot store what it was handed. The
// fetch stops there rather than walking the rest of the spec for results
// nobody can keep.
func TestFetchStopsWhenProgressReportingFails(t *testing.T) {
	ctx := context.Background()
	data, err := store.Open(filepath.Join(t.TempDir(), "fetch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()

	failure := errors.New("run row is gone")
	calls := 0
	p := Pipeline{Store: data, Source: mockSource{jobs: []crawler.RawJob{fetchRawJob("a1", "Backend Engineer"), fetchRawJob("a2", "Platform Engineer")}}}
	p.Progress = func(context.Context, crawler.Batch, FetchStats) error {
		calls++
		return failure
	}
	if _, err := p.Fetch(ctx, crawler.SearchSpec{Queries: []crawler.SearchQuery{{Direction: "P1", Keywords: []string{"platform"}}}, MaxPages: 1}, nil); !errors.Is(err, failure) {
		t.Fatalf("expected the emit failure to end the fetch, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected the fetch to stop at the first failure, got %d reports", calls)
	}
}

// What a process has in flight is the only signal that exists while a single
// Agent call spends minutes: every other record is written once it is over.
func TestActivityReportsWorkInFlightOldestFirst(t *testing.T) {
	activity := &Activity{}
	if units := activity.InFlight(); len(units) != 0 {
		t.Fatalf("an idle process reports nothing, got %v", units)
	}
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	endScore := activity.Begin("score", 7, base.Add(time.Minute))
	endLetter := activity.Begin("letter", 9, base)

	units := activity.InFlight()
	if len(units) != 2 || units[0].Stage != "letter" || units[0].JobID != 9 || units[1].Stage != "score" {
		t.Fatalf("the longest-running unit must come first, got %+v", units)
	}
	endLetter()
	endLetter()
	if units := activity.InFlight(); len(units) != 1 || units[0].Stage != "score" {
		t.Fatalf("an ended unit must leave, and ending it twice must be harmless, got %+v", units)
	}
	endScore()
	if units := activity.InFlight(); len(units) != 0 {
		t.Fatalf("expected nothing left in flight, got %+v", units)
	}
}

// A nil Activity is what a one-shot CLI stage carries, so recording into one
// must be a no-op rather than a panic.
func TestNilActivityRecordsNothing(t *testing.T) {
	var activity *Activity
	activity.Begin("filter", 1, time.Now())()
	if units := activity.InFlight(); units != nil {
		t.Fatalf("a nil activity reports nothing, got %v", units)
	}
}
