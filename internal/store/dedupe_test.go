package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// dedupeJob is one synthetic listing of the same role on a given source.
func dedupeJob(source, externalID, title, company, location, remote string, description *string) JobInput {
	return JobInput{
		Source: source, ExternalID: externalID, URL: "https://example.test/" + source + "/" + externalID,
		Title: title, CompanyName: company, CompanyInfo: "public listing",
		Description: description, Location: location, RemoteType: remote,
	}
}

func openDedupeStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	store := openTestStore(t, filepath.Join(t.TempDir(), "dedupe.db"))
	t.Cleanup(func() { closeTestStore(t, store) })
	return store, context.Background()
}

func insertDedupeJob(t *testing.T, store *Store, ctx context.Context, input JobInput) int64 {
	t.Helper()
	result, err := store.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.Job.ID
}

func link(t *testing.T, store *Store, ctx context.Context, jobID int64) DedupeOutcome {
	t.Helper()
	outcome, err := store.LinkOrSuggestDuplicate(ctx, jobID, DedupeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func processState(t *testing.T, store *Store, ctx context.Context, jobID int64) string {
	t.Helper()
	var state string
	if err := store.db.QueryRowContext(ctx, "SELECT process_state FROM jobs WHERE id=?", jobID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// ST-70: three spellings of one company are one grouping key.
func TestNormalizeCompanyFoldsLegalSuffixes(t *testing.T) {
	t.Parallel()
	first := normalizeCompany("範例科技股份有限公司")
	for _, value := range []string{"範例科技有限公司", "範例科技", "範例科技台灣分公司"} {
		if normalizeCompany(value) != first {
			t.Fatalf("normalizeCompany(%q) = %q, want %q", value, normalizeCompany(value), first)
		}
	}
	if normalizeCompany("Example Cloud Co., Ltd.") != normalizeCompany("ＥＸＡＭＰＬＥ　Cloud") {
		t.Fatalf("Co., Ltd. and the fullwidth spelling disagree: %q vs %q", normalizeCompany("Example Cloud Co., Ltd."), normalizeCompany("ＥＸＡＭＰＬＥ　Cloud"))
	}
}

// ST-71: seniority modifiers and the Chinese wording fold together; an
// internship stays a different job.
func TestNormalizeTitleFoldsSeniorityAndSynonyms(t *testing.T) {
	t.Parallel()
	senior := NewDedupeKey("Example", "資深後端工程師", "台北市", "onsite")
	english := NewDedupeKey("Example", "Senior Backend Engineer", "Taipei City", "onsite")
	intern := NewDedupeKey("Example", "後端實習生", "台北市", "onsite")
	if senior.Title != english.Title {
		t.Fatalf("titles differ: %q vs %q", senior.Title, english.Title)
	}
	if senior.City != english.City || senior.City == "" {
		t.Fatalf("cities differ: %q vs %q", senior.City, english.City)
	}
	if intern.Title == senior.Title {
		t.Fatalf("an internship must not normalize onto %q", senior.Title)
	}
	if senior.Hash() != english.Hash() {
		t.Fatal("the two spellings of one job produced different keys")
	}
}

// ST-72: same city and remote merge; different cities become a candidate.
func TestLinkMergesCompatibleLocationsAndFlagsMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	description := "Synthetic platform work"
	first := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Senior Backend Engineer", "Example Co., Ltd.", "台北市", "onsite", &description))
	link(t, store, ctx, first)

	same := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "資深後端工程師", "Example 股份有限公司", "Taipei City, Taiwan", "onsite", nil))
	outcome := link(t, store, ctx, same)
	if !outcome.Merged || outcome.CanonicalJobID != first {
		t.Fatalf("same-city merge = %+v, want canonical %d", outcome, first)
	}
	if processState(t, store, ctx, same) != StateMerged {
		t.Fatalf("alias state = %q", processState(t, store, ctx, same))
	}

	remote := insertDedupeJob(t, store, ctx, dedupeJob("yourator", "y1", "Backend Engineer", "Example", "Remote", "remote", nil))
	if remoteOutcome := link(t, store, ctx, remote); !remoteOutcome.Merged || remoteOutcome.CanonicalJobID != first {
		t.Fatalf("remote merge = %+v", remoteOutcome)
	}
}

// ST-72: the same title in a different city becomes a candidate instead of
// merging.
func TestLinkFlagsLocationMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	description := "Synthetic platform work"
	first := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Senior Backend Engineer", "Example Co., Ltd.", "台北市", "onsite", &description))
	link(t, store, ctx, first)

	elsewhere := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Engineer", "Example", "高雄市", "onsite", nil))
	outcome := link(t, store, ctx, elsewhere)
	if outcome.Merged || len(outcome.CandidateIDs) != 1 {
		t.Fatalf("location mismatch = %+v, want a candidate and no merge", outcome)
	}
	candidates, err := store.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Reason != ReasonLocationMismatch {
		t.Fatalf("candidates = %+v", candidates)
	}
	if processState(t, store, ctx, elsewhere) == StateMerged {
		t.Fatal("a location mismatch must not merge")
	}
}

// ST-81: two listings on the same platform are two openings. Neither an exact
// title match nor a similar one may merge them or ask the user about them, and a
// group already covering a source is closed to further jobs from it.
func TestLinkNeverPairsJobsFromOneSource(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	description := "Synthetic platform work"

	first := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Engineer", "Example Co., Ltd.", "台北市", "onsite", &description))
	link(t, store, ctx, first)

	// An identical title on the same source: a merge here would swallow a real
	// second opening.
	twin := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c2", "Backend Engineer", "Example Co., Ltd.", "台北市", "onsite", nil))
	if outcome := link(t, store, ctx, twin); outcome.Merged || len(outcome.CandidateIDs) != 0 {
		t.Fatalf("same-source twin = %+v, want neither a merge nor a candidate", outcome)
	}
	// A similar title on the same source: the pair the user kept being asked about.
	similar := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c3", "Engineering Lead - Backend", "Example Co., Ltd.", "台北市", "onsite", nil))
	if outcome := link(t, store, ctx, similar); outcome.Merged || len(outcome.CandidateIDs) != 0 {
		t.Fatalf("same-source similar title = %+v, want neither a merge nor a candidate", outcome)
	}
	candidates, err := store.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none from one source", candidates)
	}

	// The other platform still merges, which is what dedupe is for.
	crossed := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "資深後端工程師", "Example 股份有限公司", "台北市", "onsite", nil))
	if outcome := link(t, store, ctx, crossed); !outcome.Merged || outcome.CanonicalJobID != first {
		t.Fatalf("cross-source merge = %+v, want canonical %d", outcome, first)
	}
	// That group now covers 104 as well, so a second 104 listing is not compared
	// against it either.
	secondFrom104 := insertDedupeJob(t, store, ctx, dedupeJob("104", "a2", "Backend Engineer", "Example Co., Ltd.", "台北市", "onsite", nil))
	if outcome := link(t, store, ctx, secondFrom104); outcome.Merged || len(outcome.CandidateIDs) != 0 {
		t.Fatalf("second 104 listing = %+v, want neither a merge nor a candidate", outcome)
	}
}

// ST-73: the merge is one transaction that picks the canonical copy by JD, then
// length, then source, and records what each alias was before it.
func TestMergeChoosesCanonicalAndRecordsAliasState(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	short, long := "short JD", "a considerably longer synthetic JD body"

	yourator := insertDedupeJob(t, store, ctx, dedupeJob("yourator", "y1", "Backend Engineer", "Example", "台北市", "onsite", &short))
	link(t, store, ctx, yourator)
	if err := store.TransitionProcess(ctx, yourator, "queued"); err != nil {
		t.Fatal(err)
	}
	cake := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Engineer", "Example", "台北市", "onsite", &long))
	outcome := link(t, store, ctx, cake)
	if !outcome.Merged || outcome.CanonicalJobID != cake {
		t.Fatalf("the longer JD must be canonical: %+v", outcome)
	}
	group, err := store.GroupOf(ctx, yourator)
	if err != nil {
		t.Fatal(err)
	}
	if group.CanonicalJobID != cake || len(group.Members) != 2 {
		t.Fatalf("group = %+v", group)
	}
	events, _, err := store.GetJobDetail(ctx, yourator)
	if err != nil {
		t.Fatal(err)
	}
	last := events.Events[len(events.Events)-1]
	if last.ToState != StateMerged || last.FromState != "queued" || last.Note == nil || *last.Note != mergeNote(cake, "queued") {
		t.Fatalf("merge event = %+v", last)
	}
}

// ST-74: a copy that already carries work is never merged automatically.
func TestLinkRefusesToMergeJobsWithOutput(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	description := "Synthetic platform work"
	scored := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Engineer", "Example", "台北市", "onsite", &description))
	link(t, store, ctx, scored)
	if err := store.SaveScore(ctx, ScoreInput{JobID: scored, HardSkill: 80, Domain: 80, Seniority: 80, Condition: 80, Direction: 80, Total: 80, Reason: "synthetic", Runner: "claude"}); err != nil {
		t.Fatal(err)
	}
	other := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Engineer", "Example", "台北市", "onsite", nil))
	outcome := link(t, store, ctx, other)
	if outcome.Merged || len(outcome.CandidateIDs) != 1 {
		t.Fatalf("outcome = %+v, want a candidate", outcome)
	}
	candidates, err := store.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Reason != ReasonHasOutput {
		t.Fatalf("candidates = %+v", candidates)
	}
	if processState(t, store, ctx, other) == StateMerged || processState(t, store, ctx, scored) == StateMerged {
		t.Fatal("neither side may change state")
	}
}

// ST-75: a similar title becomes a candidate; a distant one no relation at all.
func TestLinkRegistersGreyZoneTitlesOnly(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	first := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Platform Engineer", "Example", "台北市", "onsite", nil))
	link(t, store, ctx, first)

	similar := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Platform Engineer II", "Example", "台北市", "onsite", nil))
	if outcome := link(t, store, ctx, similar); outcome.Merged || len(outcome.CandidateIDs) != 1 {
		t.Fatalf("similar title = %+v, want one candidate", outcome)
	}
	distant := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c2", "Marketing Manager", "Example", "台北市", "onsite", nil))
	outcome, err := store.LinkOrSuggestDuplicate(ctx, distant, DedupeOptions{TitleSimilarityThreshold: 0.6})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Merged || len(outcome.CandidateIDs) != 0 {
		t.Fatalf("distant title = %+v, want no relation", outcome)
	}
	candidates, err := store.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Reason != ReasonTitleSimilar {
		t.Fatalf("candidates = %+v", candidates)
	}
}

// ST-76: repeating a merge or a candidate registration changes nothing.
func TestLinkIsIdempotent(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	first := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Engineer", "Example", "台北市", "onsite", nil))
	link(t, store, ctx, first)
	second := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Engineer", "Example", "台北市", "onsite", nil))
	link(t, store, ctx, second)
	events := func(jobID int64) int {
		detail, _, err := store.GetJobDetail(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		return len(detail.Events)
	}
	before := events(second)
	for i := 0; i < 2; i++ {
		link(t, store, ctx, second)
		link(t, store, ctx, first)
	}
	if after := events(second); after != before {
		t.Fatalf("status events = %d, want %d", after, before)
	}
	similar := insertDedupeJob(t, store, ctx, dedupeJob("yourator", "y1", "Backend Engineer Intern", "Example", "台北市", "onsite", nil))
	link(t, store, ctx, similar)
	link(t, store, ctx, similar)
	candidates, err := store.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want one row per pair", len(candidates))
	}
}

// ST-77: an alias is picked up by no stage and listed nowhere.
func TestMergedJobsAreNeitherListedNorPicked(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	description := "Synthetic platform work"
	canonical := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Engineer", "Example", "台北市", "onsite", &description))
	link(t, store, ctx, canonical)
	alias := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Engineer", "Example", "台北市", "onsite", &description))
	link(t, store, ctx, alias)
	if processState(t, store, ctx, alias) != StateMerged {
		t.Fatalf("alias state = %q", processState(t, store, ctx, alias))
	}
	jobs, err := store.ListJobs(ctx, JobFilter{}, JobSortNewest)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ID == alias {
			t.Fatal("a merged job must not be listed")
		}
	}
	for _, stage := range []string{"filter", "score", "letter"} {
		picked, err := store.PickForStage(ctx, stage, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range picked {
			if job.ID == alias {
				t.Fatalf("stage %q picked a merged job", stage)
			}
		}
	}
}

// ST-78: unmerging restores the alias without touching its history.
func TestUnmergeRestoresPreviousStateAndKeepsOutput(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	description := "Synthetic platform work"
	canonical := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Engineer", "Example", "台北市", "onsite", &description))
	link(t, store, ctx, canonical)
	alias := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Engineer", "Example", "台北市", "onsite", nil))
	if err := store.TransitionProcess(ctx, alias, "filtered_out"); err != nil {
		t.Fatal(err)
	}
	link(t, store, ctx, alias)
	if processState(t, store, ctx, alias) != StateMerged {
		t.Fatal("alias was not merged")
	}
	if err := store.UnmergeJob(ctx, alias); err != nil {
		t.Fatal(err)
	}
	if state := processState(t, store, ctx, alias); state != "filtered_out" {
		t.Fatalf("restored state = %q, want filtered_out", state)
	}
	group, err := store.GroupOf(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}
	if group.CanonicalJobID != alias || len(group.Members) != 1 {
		t.Fatalf("restored group = %+v", group)
	}
	if canonicalGroup, err := store.GroupOf(ctx, canonical); err != nil || len(canonicalGroup.Members) != 1 {
		t.Fatalf("canonical group = %+v, err=%v", canonicalGroup, err)
	}
	if err := store.UnmergeJob(ctx, canonical); !errors.Is(err, ErrNotMerged) {
		t.Fatalf("unmerge of a canonical job = %v, want ErrNotMerged", err)
	}
}

// A user's decision merges the pair and closes the candidate; deciding twice is
// refused rather than applied again.
func TestMergeAndIgnoreCandidateDecisions(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	first := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Platform Engineer", "Example", "台北市", "onsite", nil))
	link(t, store, ctx, first)
	similar := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Platform Engineer II", "Example", "台北市", "onsite", nil))
	outcome := link(t, store, ctx, similar)
	candidateID := outcome.CandidateIDs[0]

	canonicalID, err := store.MergeCandidate(ctx, candidateID, DedupeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if canonicalID != first {
		t.Fatalf("canonical = %d, want %d", canonicalID, first)
	}
	if processState(t, store, ctx, similar) != StateMerged {
		t.Fatal("the decided pair was not merged")
	}
	candidates, err := store.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("decided candidates still pending: %+v", candidates)
	}
	if _, err := store.MergeCandidate(ctx, candidateID, DedupeOptions{}); err == nil {
		t.Fatal("a decided candidate must not be merged twice")
	}
	if err := store.IgnoreCandidate(ctx, candidateID); err == nil {
		t.Fatal("a decided candidate must not be ignored afterwards")
	}
}

func TestIgnoreCandidateRemovesItFromTheList(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	first := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Platform Engineer", "Example", "台北市", "onsite", nil))
	link(t, store, ctx, first)
	similar := insertDedupeJob(t, store, ctx, dedupeJob("cake", "c1", "Backend Platform Engineer II", "Example", "台北市", "onsite", nil))
	candidateID := link(t, store, ctx, similar).CandidateIDs[0]
	if err := store.IgnoreCandidate(ctx, candidateID); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.ListDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("ignored candidate still listed: %+v", candidates)
	}
	if processState(t, store, ctx, similar) == StateMerged {
		t.Fatal("ignoring must not merge")
	}
}

// Changed source content must not pull an alias back through the pipeline.
func TestChangedContentDoesNotResetAnAlias(t *testing.T) {
	t.Parallel()
	store, ctx := openDedupeStore(t)
	description := "Synthetic platform work"
	canonical := insertDedupeJob(t, store, ctx, dedupeJob("104", "a1", "Backend Engineer", "Example", "台北市", "onsite", &description))
	link(t, store, ctx, canonical)
	aliasInput := dedupeJob("cake", "c1", "Backend Engineer", "Example", "台北市", "onsite", &description)
	alias := insertDedupeJob(t, store, ctx, aliasInput)
	link(t, store, ctx, alias)

	changed := "Synthetic platform work, rewritten"
	aliasInput.Description = &changed
	if _, err := store.UpsertJob(ctx, aliasInput, nil); err != nil {
		t.Fatal(err)
	}
	if state := processState(t, store, ctx, alias); state != StateMerged {
		t.Fatalf("alias state after a content change = %q", state)
	}
}

// A database written before cross-source grouping existed keeps every job and
// gains the single-member group each of them now belongs to.
func TestMigrationFromVersionThreeGroupsExistingJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	created := openTestStore(t, path)
	ctx := context.Background()
	description := "Synthetic platform work"
	jobID := insertDedupeJob(t, created, ctx, dedupeJob("104", "a1", "Backend Engineer", "Example", "台北市", "onsite", &description))
	for _, statement := range []string{
		"DROP TABLE job_dupe_candidates",
		"DROP INDEX jobs_group_idx",
		"ALTER TABLE jobs DROP COLUMN group_id",
		"DROP TABLE job_groups",
		"PRAGMA user_version = 3",
	} {
		if _, err := created.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare v3 database: %v", err)
		}
	}
	closeTestStore(t, created)

	migrated := openTestStore(t, path)
	defer closeTestStore(t, migrated)
	group, err := migrated.GroupOf(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if group.GroupID == 0 || group.CanonicalJobID != jobID || len(group.Members) != 1 {
		t.Fatalf("migrated group = %+v", group)
	}
	// The key is normalized in Go, so it is filled the first time the job is
	// grouped again rather than during the migration.
	if outcome := link(t, migrated, ctx, jobID); outcome.Merged || outcome.CanonicalJobID != jobID {
		t.Fatalf("post-migration link = %+v", outcome)
	}
}
