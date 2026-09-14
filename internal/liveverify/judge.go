package liveverify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/dccoding1118/job-finder/internal/store"
)

// The judges read a verification snapshot of a long-lived test database, so
// none of them asserts a count over the whole database: each is about one job,
// or about what a single fetch changed (docs/verify.md §6).

const expectedSchemaVersion = 10

var (
	expectedTables = []string{"agent_calls", "filter_results", "job_dupe_candidates", "job_groups", "jobs", "letter_attempts", "letters", "runs", "scores", "settings", "status_events"}
	hex64          = regexp.MustCompile(`^[a-f0-9]{64}$`)
	verdictValues  = map[string]bool{"pass": true, "fail": true, "unknown": true}
	remoteValues   = map[string]bool{"remote": true, "hybrid": true, "onsite": true, "unknown": true}
	// scoredStates are the states a job can only be in once it holds a score.
	scoredStates = map[string]bool{"scored": true, "shortlisted": true, "letter_requested": true, "letter_ready": true, "letter_failed": true}
)

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func judgeSchema(snapshot store.VerificationSnapshot) error {
	if snapshot.SchemaVersion != expectedSchemaVersion {
		return fmt.Errorf("schema version is %d, want %d", snapshot.SchemaVersion, expectedSchemaVersion)
	}
	if !strings.EqualFold(snapshot.JournalMode, "wal") {
		return fmt.Errorf("journal mode is %s, want wal", snapshot.JournalMode)
	}
	if !snapshot.ForeignKeys {
		return fmt.Errorf("foreign keys are off")
	}
	if !reflect.DeepEqual(snapshot.Tables, expectedTables) {
		return fmt.Errorf("tables are %v, want %v", snapshot.Tables, expectedTables)
	}
	return nil
}

// judgeSource checks every stored Yourator job against the normalization
// contract and returns a content-free summary.
func judgeSource(snapshot store.VerificationSnapshot) (string, error) {
	ids := map[string]bool{}
	for _, job := range snapshot.Jobs {
		if job.Source != "yourator" {
			continue
		}
		label := fmt.Sprintf("yourator job %d", job.ID)
		if strings.TrimSpace(job.ExternalID) == "" {
			return "", fmt.Errorf("%s has no external ID", label)
		}
		if ids[job.ExternalID] {
			return "", fmt.Errorf("duplicate external_id %s", job.ExternalID)
		}
		ids[job.ExternalID] = true
		parsed, err := url.Parse(job.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "www.yourator.co" {
			return "", fmt.Errorf("%s URL is not an HTTPS URL on www.yourator.co", label)
		}
		if strings.TrimSpace(job.Title) == "" || strings.TrimSpace(job.CompanyName) == "" || strings.TrimSpace(job.Location) == "" {
			return "", fmt.Errorf("%s lacks a title, company or location", label)
		}
		if job.DescriptionLength <= 0 {
			return "", fmt.Errorf("%s has no description", label)
		}
		if !hex64.MatchString(job.DescriptionSHA256) {
			return "", fmt.Errorf("%s description hash is malformed", label)
		}
		if job.ContentHash == nil || !hex64.MatchString(*job.ContentHash) {
			return "", fmt.Errorf("%s content hash is malformed", label)
		}
		if !remoteValues[job.RemoteType] {
			return "", fmt.Errorf("%s remote_type %s is not in the enum", label, job.RemoteType)
		}
		if (job.SalaryMin == nil) != (job.SalaryMax == nil) {
			return "", fmt.Errorf("%s has only one salary bound", label)
		}
		if job.SalaryMin != nil && (*job.SalaryMin < 0 || *job.SalaryMin > *job.SalaryMax) {
			return "", fmt.Errorf("%s salary range is inverted", label)
		}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("the database holds no Yourator job")
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	return fmt.Sprintf("yourator_jobs=%d ids_sha256=%s", len(sorted), digest(strings.Join(sorted, "\n"))), nil
}

// fingerprint reduces the stored jobs to what a repeated fetch of an unchanged
// source must reproduce exactly: the identity, the content hash the change
// detection keys on, and the processing state that hash decides. A source page
// carrying per-request state would otherwise reset every job to `new` on each
// run and pay for screening and scoring again.
func fingerprint(snapshot store.VerificationSnapshot) (count int, sum string) {
	rows := make([]string, 0, len(snapshot.Jobs))
	for _, job := range snapshot.Jobs {
		hash := ""
		if job.ContentHash != nil {
			hash = *job.ContentHash
		}
		rows = append(rows, strings.Join([]string{job.Source, job.ExternalID, hash, job.ProcessState}, "|"))
	}
	sort.Strings(rows)
	return len(rows), digest(strings.Join(rows, "\n"))
}

func findJob(snapshot store.VerificationSnapshot, id int64) (store.VerificationJob, error) {
	for _, job := range snapshot.Jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return store.VerificationJob{}, fmt.Errorf("job %d is not in the snapshot", id)
}

// judgeScreened checks one job's screening result and, once it holds a score,
// the score's shape.
func judgeScreened(snapshot store.VerificationSnapshot, id int64) (string, error) {
	job, err := findJob(snapshot, id)
	if err != nil {
		return "", err
	}
	if job.FilterOutcome == nil || !verdictValues[*job.FilterOutcome] {
		return "", fmt.Errorf("job %d has no screening outcome in the enum", id)
	}
	for _, condition := range job.FilterConditions {
		if !verdictValues[condition.Verdict] {
			return "", fmt.Errorf("job %d carries a condition verdict outside the enum", id)
		}
	}
	scored := scoredStates[job.ProcessState]
	if scored {
		score := job.Score
		if score == nil {
			return "", fmt.Errorf("job %d is in %s without a score", id, job.ProcessState)
		}
		for name, value := range map[string]int{"content_fit": score.Content, "benefit_fit": score.Benefit, "bonus_fit": score.Bonus, "industry_fit": score.Industry} {
			if value < 0 || value > 100 {
				return "", fmt.Errorf("job %d %s is out of range", id, name)
			}
		}
		if score.Total < 0 || score.Total > 100 {
			return "", fmt.Errorf("job %d total score is out of range", id)
		}
		if !hex64.MatchString(score.ReasonSHA256) || score.ReasonSHA256 == digest("") {
			return "", fmt.Errorf("job %d has no reason", id)
		}
	}
	return fmt.Sprintf("job=%d process_state=%s filter_outcome=%s conditions=%d scored=%t", id, job.ProcessState, *job.FilterOutcome, len(job.FilterConditions), scored), nil
}

// judgeLettered checks the letter a generation that ended in status left on one
// job. A failed generation leaves no letter of its own to check.
func judgeLettered(snapshot store.VerificationSnapshot, id int64, status string) (string, error) {
	if status != "approved" && status != "finalized" && status != "failed" {
		return "", fmt.Errorf("letter generation status %s is not a terminal status", status)
	}
	job, err := findJob(snapshot, id)
	if err != nil {
		return "", err
	}
	if status == "failed" {
		return fmt.Sprintf("job=%d status=failed", id), nil
	}
	letter := job.Letter
	if letter == nil {
		return "", fmt.Errorf("job %d has no letter after a %s generation", id, status)
	}
	if letter.Status != status {
		return "", fmt.Errorf("job %d letter status %s differs from its generation (%s)", id, letter.Status, status)
	}
	if !letter.HasNamePlaceholder || !letter.HasContactPlaceholder {
		return "", fmt.Errorf("job %d letter lacks a signature placeholder", id)
	}
	return fmt.Sprintf("job=%d status=%s rounds=%d", id, status, letter.Rounds), nil
}
