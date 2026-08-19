// Package store owns SQLite persistence and job state transitions.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 8

//go:embed schema.sql
var schemaSQL string

// upgrades[v] brings a database at user_version v to v+1; a fresh database is
// created from schemaSQL, which already reflects the newest version.
var upgrades = map[int]string{
	1: `ALTER TABLE jobs ADD COLUMN discovered_by_run_id INTEGER REFERENCES runs(id);
	CREATE INDEX IF NOT EXISTS jobs_discovered_by_run_idx ON jobs(discovered_by_run_id);
	CREATE INDEX IF NOT EXISTS agent_calls_role_created_idx ON agent_calls(role, created_at);`,
	2: `ALTER TABLE jobs ADD COLUMN profile_revision TEXT;
	ALTER TABLE scores ADD COLUMN profile_revision TEXT;
	ALTER TABLE letters ADD COLUMN profile_revision TEXT;
	ALTER TABLE agent_calls ADD COLUMN profile_revision TEXT;`,
	// Every existing job becomes its own single-member group, which is the state a
	// job is in until another source turns out to carry the same listing. The
	// grouping key is normalized in Go, so it is left NULL here and filled in the
	// first time each job is seen again.
	3: `CREATE TABLE job_groups (
		id INTEGER PRIMARY KEY,
		canonical_job_id INTEGER NOT NULL REFERENCES jobs(id),
		dedupe_key TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS job_groups_dedupe_key_idx ON job_groups(dedupe_key);
	CREATE TABLE job_dupe_candidates (
		id INTEGER PRIMARY KEY,
		group_a_id INTEGER NOT NULL REFERENCES job_groups(id),
		group_b_id INTEGER NOT NULL REFERENCES job_groups(id),
		similarity REAL NOT NULL,
		reason TEXT NOT NULL,
		state TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(group_a_id, group_b_id)
	);
	CREATE INDEX IF NOT EXISTS job_dupe_candidates_state_idx ON job_dupe_candidates(state);
	ALTER TABLE jobs ADD COLUMN group_id INTEGER REFERENCES job_groups(id);
	CREATE INDEX IF NOT EXISTS jobs_group_idx ON jobs(group_id);
	INSERT INTO job_groups (canonical_job_id, dedupe_key, created_at, updated_at)
		SELECT id, NULL, first_seen_at, updated_at FROM jobs;
	UPDATE jobs SET group_id = (SELECT id FROM job_groups WHERE canonical_job_id = jobs.id);`,
	// `jobs.profile_revision` names the Profile revision the job's current
	// processing belongs to, so a job that kept its assessment through a content
	// change must still carry the revision that produced it. Rows whose recorded
	// revision has no score are re-pointed at the revision of the score they do
	// carry, which is what the assessment on screen was computed from.
	4: `UPDATE jobs SET profile_revision = (
		SELECT profile_revision FROM scores WHERE job_id = jobs.id ORDER BY created_at DESC, id DESC LIMIT 1
	)
	WHERE EXISTS (SELECT 1 FROM scores WHERE job_id = jobs.id)
	  AND NOT EXISTS (SELECT 1 FROM scores WHERE job_id = jobs.id AND profile_revision IS jobs.profile_revision);`,
	// The hard/soft split replaces one revision with two and the five scoring
	// dimensions with four. Old scores are dropped rather than converted: the
	// dimensions mean something else now, and there is no rate to convert at.
	// Every job that had been screened or scored returns to `new`, because both
	// verdicts were produced by rules that no longer exist. Letter stages, letters
	// and application history are left untouched — they are the user's own output.
	5: `ALTER TABLE jobs ADD COLUMN filter_revision TEXT;
	ALTER TABLE jobs ADD COLUMN score_revision TEXT;
	ALTER TABLE jobs DROP COLUMN profile_revision;
	ALTER TABLE letters ADD COLUMN filter_revision TEXT;
	ALTER TABLE letters ADD COLUMN score_revision TEXT;
	UPDATE letters SET score_revision = profile_revision;
	ALTER TABLE letters DROP COLUMN profile_revision;
	ALTER TABLE agent_calls ADD COLUMN filter_revision TEXT;
	ALTER TABLE agent_calls ADD COLUMN score_revision TEXT;
	UPDATE agent_calls SET score_revision = profile_revision;
	ALTER TABLE agent_calls DROP COLUMN profile_revision;
	DROP TABLE scores;
	CREATE TABLE scores (
		id INTEGER PRIMARY KEY,
		job_id INTEGER NOT NULL REFERENCES jobs(id),
		dim_content INTEGER NOT NULL,
		dim_benefit INTEGER NOT NULL,
		dim_bonus INTEGER NOT NULL,
		dim_industry INTEGER NOT NULL,
		total REAL NOT NULL,
		reason TEXT NOT NULL,
		runner TEXT NOT NULL,
		score_revision TEXT,
		created_at TEXT NOT NULL
	);
	CREATE TABLE filter_results (
		id INTEGER PRIMARY KEY,
		job_id INTEGER NOT NULL REFERENCES jobs(id),
		outcome TEXT NOT NULL,
		conditions TEXT NOT NULL,
		stage TEXT NOT NULL,
		runner TEXT,
		filter_revision TEXT,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS filter_results_job_idx ON filter_results(job_id);
	INSERT INTO status_events (job_id, axis, from_state, to_state, note, created_at)
		SELECT id, 'process',
			process_state,
			CASE WHEN description IS NULL THEN 'discovered' ELSE 'new' END,
			'hard/soft split reset', updated_at
		FROM jobs WHERE process_state IN ('filtered_out', 'queued', 'scored', 'shortlisted');
	-- A job rejected off a list page has no JD text. Resetting it to new would
	-- offer the screen an excerpt to judge and could end with an empty JD being
	-- scored, so it goes back to the discovered list to be opened instead.
	UPDATE jobs SET
			process_state = CASE WHEN description IS NULL THEN 'discovered' ELSE 'new' END,
			filter_hits = NULL, updated_at = updated_at
		WHERE process_state IN ('filtered_out', 'queued', 'scored', 'shortlisted');`,
	// Token accounting comes straight from the CLI's own structured output
	// (claude's `usage`/`total_cost_usd`, codex's `turn.completed` usage event),
	// so existing rows predate the columns and carry NULL/0.
	6: `ALTER TABLE agent_calls ADD COLUMN model TEXT;
	ALTER TABLE agent_calls ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_calls ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_calls ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_calls ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_calls ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_calls ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0;
	CREATE INDEX IF NOT EXISTS agent_calls_runner_model_created_idx ON agent_calls(runner, model, created_at);`,
	// Settings the user changes from the Side Panel live in the database rather
	// than in config.yaml: the API service owns them at runtime, and a restart
	// must not silently undo a switch the user turned off.
	7: `CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
}

var piiPattern = regexp.MustCompile(`(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`)

var phonePattern = regexp.MustCompile(`(?:\+886|0)9\d{8}`)

// maskPII replaces mail addresses and mobile numbers with fixed placeholders so
// a stored copy of a prompt or a response keeps its shape without keeping the
// contact details themselves.
func maskPII(text string) string {
	return phonePattern.ReplaceAllString(piiPattern.ReplaceAllString(text, "[EMAIL]"), "[PHONE]")
}

func hasPII(text string) bool {
	return piiPattern.MatchString(text) || phonePattern.MatchString(text)
}

// Store provides the only supported path for job persistence and transitions.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

type JobInput struct {
	Source      string
	ExternalID  string
	URL         string
	Title       string
	CompanyName string
	CompanyInfo string
	Description *string
	SalaryMin   *int
	SalaryMax   *int
	Location    string
	RemoteType  string
	// FilterRevision is the Profile screening revision a newly stored or reset
	// job will be screened under.
	FilterRevision string
}

type Job struct {
	ID             int64
	Source         string
	ExternalID     string
	URL            string
	Title          string
	CompanyName    string
	CompanyInfo    string
	Description    *string
	SalaryMin      *int
	SalaryMax      *int
	Location       string
	RemoteType     string
	ProcessState   string
	ApplyState     *string
	ContentHash    *string
	FilterHits     []string
	ScoreTotal     *float64
	FilterRevision *string
	ScoreRevision  *string
}

type UpsertResult struct {
	Job     Job
	Created bool
	Changed bool
}

type ScoreInput struct {
	JobID                             int64
	Content, Benefit, Bonus, Industry int
	Total                             float64
	Reason, Runner                    string
	ScoreRevision                     string
}

type LetterInput struct {
	JobID                      int64
	Content, Status, ReviewLog string
	Rounds                     int
	RunnerDraft, RunnerReview  string
	FilterRevision             string
	ScoreRevision              string
}

type AgentCallInput struct {
	JobID                              *int64
	Role, Runner, Model, Input, Output string
	OK                                 bool
	DurationMS                         int64
	Usage                              AgentCallUsage
	FilterRevision                     string
	ScoreRevision                      string
}

// AgentCallUsage is the token accounting one Agent call reports, read straight
// out of the CLI's own structured output. CostUSD is 0 when the runner does
// not price its own calls.
type AgentCallUsage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
	CostUSD          float64
}

// RunStats records fetch facts only: filter, score, and letter are consumed by
// the resident worker outside any run.
type RunStats map[string]int

// NewRunStats returns the zeroed fetch facts every run records.
func NewRunStats() RunStats {
	return RunStats{"fetched": 0, "new": 0, "queries": 0, "errors": 0}
}

const (
	RunTriggerTimer           = "timer"
	RunTriggerManualCLI       = "manual-cli"
	RunTriggerManualExtension = "manual-extension"
)

// LocationUnknown is the locality of a job whose source stated none. A job's
// location is a required field, so absence needs a value of its own: without it
// a missing locality would read as a locality that matches nothing, and the
// screening rules would reject the job for a fact nobody ever stated.
const LocationUnknown = "unknown"

// Open opens and migrates a SQLite database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	store, err := New(db)
	if err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("initialize store: %w; close sqlite: %v", err, closeErr)
		}
		return nil, err
	}
	restrict(path)
	return store, nil
}

// restrict tightens the database and its write-ahead sidecars to the owner on
// platforms that have file modes. The driver creates them at the process umask,
// which it has no reason to know is wrong here; the containing directory is
// already owner-only, so this only closes the gap between that and the files
// themselves. It is best-effort by design: a database that opens but whose mode
// cannot be changed is still a working database, and failing the open would be
// the larger harm.
func restrict(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err != nil {
			continue
		}
		_ = os.Chmod(path+suffix, 0o600)
	}
}

// New configures and migrates an existing database handle.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("store: database is nil")
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return nil, fmt.Errorf("enable sqlite WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	store := &Store{db: db, now: time.Now}
	if err := store.migrate(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if version == 0 {
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	} else {
		for from := version; from < schemaVersion; from++ {
			upgrade, ok := upgrades[from]
			if !ok {
				return fmt.Errorf("store: no upgrade from schema version %d", from)
			}
			if _, err := tx.ExecContext(ctx, upgrade); err != nil {
				return fmt.Errorf("upgrade schema from version %d: %w", from, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

// UpsertJob deduplicates on (source, external_id) and initializes or resets the
// process state. runID records the fetch run that first stored the job; capture
// ingest passes nil because user-navigated jobs belong to no run.
func (s *Store) UpsertJob(ctx context.Context, input JobInput, runID *int64) (UpsertResult, error) {
	if err := validateJobInput(input); err != nil {
		return UpsertResult{}, err
	}
	// A JD is public text that can still carry the recruiter's own mail address
	// or phone number. Masking at ingest is what keeps that contact information
	// out of both the database and every prompt built from it — a job board
	// holds those details under its own promise not to leak them, and reading a
	// public page is no reason to hand them to a third-party model.
	if input.Description != nil {
		masked := maskPII(*input.Description)
		input.Description = &masked
	}
	now := s.timestamp()
	partial := input.Description == nil
	hash := ""
	if !partial {
		hash = contentHash(input)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("begin upsert job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := findJobTx(ctx, tx, input.Source, input.ExternalID)
	if err != nil {
		return UpsertResult{}, err
	}
	if !found {
		state := "new"
		if partial {
			state = "discovered"
		}
		var description any
		var contentHashValue any
		if partial {
			description, contentHashValue = nil, nil
		} else {
			description, contentHashValue = *input.Description, hash
		}
		result, execErr := tx.ExecContext(ctx, `INSERT INTO jobs
			(source, external_id, url, title, company_name, company_info, description, salary_min, salary_max, location, remote_type, content_hash, process_state, filter_revision, discovered_by_run_id, first_seen_at, last_seen_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.Source, input.ExternalID, input.URL, input.Title, input.CompanyName, input.CompanyInfo, description, input.SalaryMin, input.SalaryMax, input.Location, input.RemoteType, contentHashValue, state, nullableString(input.FilterRevision), runID, now, now, now)
		if execErr != nil {
			return UpsertResult{}, fmt.Errorf("insert job: %w", execErr)
		}
		id, idErr := result.LastInsertId()
		if idErr != nil {
			return UpsertResult{}, fmt.Errorf("get inserted job id: %w", idErr)
		}
		if err := insertEvent(ctx, tx, id, "process", "", state, "", now); err != nil {
			return UpsertResult{}, err
		}
		// Every job starts in a group of its own; cross-source grouping only ever
		// moves members between groups, so a job is never without one.
		if err := s.createGroupTx(ctx, tx, id, NewDedupeKey(input.CompanyName, input.Title, input.Location, input.RemoteType), now); err != nil {
			return UpsertResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return UpsertResult{}, fmt.Errorf("commit inserted job: %w", err)
		}
		job := Job{ID: id, Source: input.Source, ExternalID: input.ExternalID, URL: input.URL, Title: input.Title, CompanyName: input.CompanyName, CompanyInfo: input.CompanyInfo, Description: input.Description, SalaryMin: input.SalaryMin, SalaryMax: input.SalaryMax, Location: input.Location, RemoteType: input.RemoteType, ProcessState: state, ContentHash: stringPtr(hash, !partial), FilterRevision: stringPtr(input.FilterRevision, input.FilterRevision != "")}
		return UpsertResult{Job: job, Created: true}, nil
	}

	if partial {
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET last_seen_at = ? WHERE id = ?", now, existing.ID); err != nil {
			return UpsertResult{}, fmt.Errorf("touch partial job: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return UpsertResult{}, fmt.Errorf("commit partial job touch: %w", err)
		}
		return UpsertResult{Job: existing}, nil
	}

	changed := existing.ContentHash == nil || *existing.ContentHash != hash
	if !changed {
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET last_seen_at = ? WHERE id = ?", now, existing.ID); err != nil {
			return UpsertResult{}, fmt.Errorf("touch unchanged job: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return UpsertResult{}, fmt.Errorf("commit unchanged job touch: %w", err)
		}
		return UpsertResult{Job: existing}, nil
	}

	newState := existing.ProcessState
	if canReset(existing.ProcessState) {
		newState = "new"
	}
	// Both revisions follow the processing, not the content: only a job returning
	// to `new` will be assessed under the ingesting revision. A job that keeps its
	// state keeps the revisions its screening result and score were produced
	// under, because those are what every reader pairs it with them by. A reset
	// job loses its score revision outright: it must pass screening again before
	// any score of it means anything, and its previous hits with it: they named
	// the conditions of an assessment that no longer stands.
	filterRevision, scoreRevision, filterHits := nullableString(input.FilterRevision), any(nil), any(nil)
	if newState == existing.ProcessState {
		filterRevision, scoreRevision = nullableRevision(existing.FilterRevision), nullableRevision(existing.ScoreRevision)
		filterHits = nullableHitsValue(existing.FilterHits)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET url=?, title=?, company_name=?, company_info=?, description=?, salary_min=?, salary_max=?, location=?, remote_type=?, content_hash=?, process_state=?, filter_hits=?, filter_revision=?, score_revision=?, updated_at=?, last_seen_at=? WHERE id=?`, input.URL, input.Title, input.CompanyName, input.CompanyInfo, *input.Description, input.SalaryMin, input.SalaryMax, input.Location, input.RemoteType, hash, newState, filterHits, filterRevision, scoreRevision, now, now, existing.ID); err != nil {
		return UpsertResult{}, fmt.Errorf("update changed job: %w", err)
	}
	if newState != existing.ProcessState {
		if err := dropFilterResults(ctx, tx, existing.ID); err != nil {
			return UpsertResult{}, err
		}
		if err := insertEvent(ctx, tx, existing.ID, "process", existing.ProcessState, newState, "", now); err != nil {
			return UpsertResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return UpsertResult{}, fmt.Errorf("commit changed job: %w", err)
	}
	existing.URL, existing.Title, existing.CompanyName, existing.CompanyInfo = input.URL, input.Title, input.CompanyName, input.CompanyInfo
	existing.Description, existing.SalaryMin, existing.SalaryMax = input.Description, input.SalaryMin, input.SalaryMax
	existing.Location, existing.RemoteType = input.Location, input.RemoteType
	if newState != existing.ProcessState {
		existing.FilterRevision, existing.ScoreRevision, existing.FilterHits = stringPtr(input.FilterRevision, input.FilterRevision != ""), nil, nil
	}
	existing.ContentHash, existing.ProcessState = stringPtr(hash, true), newState
	return UpsertResult{Job: existing, Changed: true}, nil
}

func (s *Store) TransitionProcess(ctx context.Context, jobID int64, to string) error {
	return s.transition(ctx, jobID, "process", to, "")
}

// SetFilterHits stores the ordered L0 rule names that rejected a job.
func (s *Store) SetFilterHits(ctx context.Context, jobID int64, hits []string) error {
	encoded, err := json.Marshal(hits)
	if err != nil {
		return fmt.Errorf("encode filter hits: %w", err)
	}
	result, err := s.db.ExecContext(ctx, "UPDATE jobs SET filter_hits=?, updated_at=? WHERE id=?", string(encoded), s.timestamp(), jobID)
	if err != nil {
		return fmt.Errorf("save filter hits: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("store: job %d not found", jobID)
	}
	return nil
}

func (s *Store) TransitionApply(ctx context.Context, jobID int64, to, note string) error {
	return s.transition(ctx, jobID, "apply", to, note)
}

func (s *Store) transition(ctx context.Context, jobID int64, axis, to, note string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin state transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	job, found, err := findJobIDTx(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("store: not found: job %d", jobID)
	}
	now := s.timestamp()
	switch axis {
	case "process":
		if !validProcessTransition(job.ProcessState, to) {
			return fmt.Errorf("store: illegal process transition: %s -> %s", job.ProcessState, to)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET process_state=?, updated_at=? WHERE id=?", to, now, jobID); err != nil {
			return fmt.Errorf("update process state: %w", err)
		}
		if err := insertEvent(ctx, tx, jobID, axis, job.ProcessState, to, "", now); err != nil {
			return err
		}
		if to == "letter_ready" {
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET apply_state=? WHERE id=?", "pending", jobID); err != nil {
				return fmt.Errorf("initialize apply state: %w", err)
			}
			if err := insertEvent(ctx, tx, jobID, "apply", "", "pending", "", now); err != nil {
				return err
			}
		}
	case "apply":
		from := ""
		if job.ApplyState != nil {
			from = *job.ApplyState
		}
		if job.ProcessState != "letter_ready" || !validApplyTransition(from, to) {
			return fmt.Errorf("store: illegal apply transition: %s -> %s", from, to)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET apply_state=?, updated_at=? WHERE id=?", to, now, jobID); err != nil {
			return fmt.Errorf("update apply state: %w", err)
		}
		if err := insertEvent(ctx, tx, jobID, axis, from, to, note, now); err != nil {
			return err
		}
	default:
		return fmt.Errorf("store: invalid state axis %q", axis)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state transition: %w", err)
	}
	return nil
}

func (s *Store) SaveScore(ctx context.Context, input ScoreInput) error {
	if err := validateScore(input); err != nil {
		return err
	}
	if input.ScoreRevision == "" {
		input.ScoreRevision = s.jobRevision(ctx, "score_revision", input.JobID)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO scores (job_id, dim_content, dim_benefit, dim_bonus, dim_industry, total, reason, runner, score_revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.JobID, input.Content, input.Benefit, input.Bonus, input.Industry, input.Total, input.Reason, input.Runner, nullableString(input.ScoreRevision), s.timestamp())
	if err != nil {
		return fmt.Errorf("save score: %w", err)
	}
	return nil
}

func (s *Store) SaveLetter(ctx context.Context, input LetterInput) error {
	// Only a run that produced a letter writes one. A finalized letter carries no
	// reviewer when the round limit leaves no round to review it in.
	if input.JobID <= 0 || !validLetterStatus(input.Status) || input.Rounds < 1 || input.Content == "" || input.RunnerDraft == "" {
		return errors.New("store: invalid letter")
	}
	if input.Status == "approved" && input.RunnerReview == "" {
		return errors.New("store: invalid letter")
	}
	if input.FilterRevision == "" {
		input.FilterRevision = s.jobRevision(ctx, "filter_revision", input.JobID)
	}
	if input.ScoreRevision == "" {
		input.ScoreRevision = s.jobRevision(ctx, "score_revision", input.JobID)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO letters (job_id, content, status, rounds, review_log, runner_draft, runner_review, filter_revision, score_revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.JobID, input.Content, input.Status, input.Rounds, input.ReviewLog, input.RunnerDraft, input.RunnerReview, nullableString(input.FilterRevision), nullableString(input.ScoreRevision), s.timestamp())
	if err != nil {
		return fmt.Errorf("save letter: %w", err)
	}
	return nil
}

// validLetterStatus lists the two outcomes that produce a letter: approved by the
// reviewer, or finalized as the last round's version without a final review. A run
// that produced nothing usable leaves the job at `letter_failed` and writes no row;
// `failed` survives only in rows written before that was the rule.
func validLetterStatus(status string) bool {
	return status == "approved" || status == "finalized"
}

func (s *Store) SaveAgentCall(ctx context.Context, input AgentCallInput) error {
	if !validAgentRole(input.Role) {
		return fmt.Errorf("store: invalid agent role %q", input.Role)
	}
	if !validRunner(input.Runner) || input.DurationMS < 0 {
		return errors.New("store: invalid agent call")
	}
	// The audit copy is masked rather than rejected: the JD text a prompt embeds
	// can carry a recruiter's mail address or phone number, and dropping the row
	// would discard the usage and verdict of a call that was already paid for.
	// What the Agent received is untouched, so no judgement depends on this.
	input.Input = maskPII(input.Input)
	input.Output = maskPII(input.Output)
	if hasPII(input.Input) || hasPII(input.Output) {
		return errors.New("store: invalid agent call")
	}
	ok := 0
	if input.OK {
		ok = 1
	}
	if input.JobID != nil {
		if input.FilterRevision == "" {
			input.FilterRevision = s.jobRevision(ctx, "filter_revision", *input.JobID)
		}
		if input.ScoreRevision == "" {
			input.ScoreRevision = s.jobRevision(ctx, "score_revision", *input.JobID)
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_calls (job_id, role, runner, model, input, output, ok, duration_ms, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_usd, filter_revision, score_revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.JobID, input.Role, input.Runner, nullableString(input.Model), input.Input, input.Output, ok, input.DurationMS,
		input.Usage.InputTokens, input.Usage.OutputTokens, input.Usage.CacheReadTokens, input.Usage.CacheWriteTokens, input.Usage.ReasoningTokens, input.Usage.CostUSD,
		nullableString(input.FilterRevision), nullableString(input.ScoreRevision), s.timestamp())
	if err != nil {
		return fmt.Errorf("save agent call: %w", err)
	}
	return nil
}

// jobRevision reads one of a job's two revision columns; column is a fixed
// literal chosen by the caller, never user input.
func (s *Store) jobRevision(ctx context.Context, column string, jobID int64) string {
	if column != "filter_revision" && column != "score_revision" {
		return ""
	}
	var revision sql.NullString
	// #nosec G202 -- column is one of the two literals validated immediately above.
	if err := s.db.QueryRowContext(ctx, "SELECT "+column+" FROM jobs WHERE id=?", jobID).Scan(&revision); err == nil && revision.Valid {
		return revision.String
	}
	return ""
}

func validAgentRole(role string) bool {
	return role == "filter" || role == "scorer" || role == "drafter" || role == "reviewer" || role == "calibrator"
}

func (s *Store) StartRun(ctx context.Context, trigger string) (int64, error) {
	if !validTrigger(trigger) {
		return 0, fmt.Errorf("store: invalid run trigger %q", trigger)
	}
	encoded, _ := json.Marshal(NewRunStats())
	result, err := s.db.ExecContext(ctx, "INSERT INTO runs (started_at, trigger, stats) VALUES (?, ?, ?)", s.timestamp(), trigger, string(encoded))
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get run id: %w", err)
	}
	return id, nil
}

func (s *Store) FinishRun(ctx context.Context, id int64, stats RunStats, runErr string) error {
	if id <= 0 {
		return errors.New("store: invalid run id")
	}
	encoded, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("encode run stats: %w", err)
	}
	result, err := s.db.ExecContext(ctx, "UPDATE runs SET finished_at=?, stats=?, error=? WHERE id=? AND finished_at IS NULL", s.timestamp(), string(encoded), nullableString(runErr), id)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count finished run: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("store: run %d not found or already finished", id)
	}
	return nil
}

func (s *Store) timestamp() string { return s.now().In(time.Local).Format(time.RFC3339) }

func validateJobInput(in JobInput) error {
	if in.Source != "yourator" && in.Source != "cake" && in.Source != "104" {
		return fmt.Errorf("store: invalid source %q", in.Source)
	}
	if in.ExternalID == "" || in.URL == "" || in.Title == "" || in.CompanyName == "" || in.CompanyInfo == "" || in.Location == "" {
		return errors.New("store: required job field is empty")
	}
	if in.RemoteType != "onsite" && in.RemoteType != "hybrid" && in.RemoteType != "remote" && in.RemoteType != "unknown" {
		return fmt.Errorf("store: invalid remote type %q", in.RemoteType)
	}
	if (in.SalaryMin != nil && *in.SalaryMin < 0) || (in.SalaryMax != nil && *in.SalaryMax < 0) || (in.SalaryMin != nil && in.SalaryMax != nil && *in.SalaryMin > *in.SalaryMax) {
		return errors.New("store: invalid salary range")
	}
	return nil
}

func contentHash(in JobInput) string {
	min, max := "", ""
	if in.SalaryMin != nil {
		min = strconv.Itoa(*in.SalaryMin)
	}
	if in.SalaryMax != nil {
		max = strconv.Itoa(*in.SalaryMax)
	}
	payload := strings.Join([]string{in.Title, *in.Description, min, max, in.Location, in.RemoteType}, "\n")
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func validProcessTransition(from, to string) bool {
	return map[string]map[string]bool{
		"discovered": {"filtered_out": true, "new": true},
		// `new` reaches `discovered` when the screen finds it has no JD text after
		// all: an excerpt belongs on the 待看 list, not in front of the scorer.
		"new":              {"filtered_out": true, "queued": true, "discovered": true},
		"queued":           {"scored": true, "shortlisted": true},
		"scored":           {"queued": true},
		"shortlisted":      {"letter_requested": true, "queued": true},
		"letter_requested": {"letter_ready": true, "letter_failed": true},
		"letter_failed":    {"letter_requested": true},
	}[from][to]
}

func validApplyTransition(from, to string) bool {
	return map[string]map[string]bool{"pending": {"applied": true, "dropped": true}, "applied": {"interview": true, "ghosted": true, "dropped": true}, "interview": {"offer": true, "ghosted": true, "dropped": true}}[from][to]
}

// canReset reports whether changed source content may send a job back through
// the pipeline. An alias is excluded: a merged copy must stay out of every stage
// however often its own platform reprints it. `filtered_out` is excluded too,
// and that holds for a job screened off a list page just as much as for one
// screened on its full JD: the hard rules rejected a stated fact, and the JD
// text arriving later does not unsay it. Reversing such a rejection is the
// user's own call, through a manual reprocess.
func canReset(state string) bool {
	return state != "filtered_out" && state != "scored" && state != "letter_ready" && state != StateMerged
}

func validRunner(runner string) bool { return runner == "claude" || runner == "codex" }
func validTrigger(trigger string) bool {
	return trigger == RunTriggerTimer || trigger == RunTriggerManualCLI || trigger == RunTriggerManualExtension
}

func stringPtr(value string, ok bool) *string {
	if !ok {
		return nil
	}
	return &value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullableRevision binds an optional revision back unchanged, so a job that had
// none keeps none.
func nullableRevision(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func insertEvent(ctx context.Context, tx *sql.Tx, jobID int64, axis, from, to, note, createdAt string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO status_events (job_id, axis, from_state, to_state, note, created_at) VALUES (?, ?, ?, ?, ?, ?)", jobID, axis, from, to, nullableString(note), createdAt)
	if err != nil {
		return fmt.Errorf("insert status event: %w", err)
	}
	return nil
}

// jobColumns is the single job projection every reader scans with scanJobRow.
const jobColumns = "id, source, external_id, url, title, company_name, company_info, description, salary_min, salary_max, location, remote_type, process_state, apply_state, content_hash, filter_hits, filter_revision, score_revision"

func findJobTx(ctx context.Context, tx *sql.Tx, source, externalID string) (Job, bool, error) {
	row := tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE source=? AND external_id=?", source, externalID)
	return scanJob(row)
}

func findJobIDTx(ctx context.Context, tx *sql.Tx, id int64) (Job, bool, error) {
	row := tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=?", id)
	return scanJob(row)
}

type rowScanner interface{ Scan(dest ...any) error }

// trailingScanner lets a reader select jobColumns plus extra columns of its own
// while still scanning the job itself through scanJobRow.
type trailingScanner struct {
	row      rowScanner
	trailing []any
}

func (s trailingScanner) Scan(dest ...any) error { return s.row.Scan(append(dest, s.trailing...)...) }

func scanJob(row rowScanner) (Job, bool, error) {
	job, err := scanJobRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("read job: %w", err)
	}
	return job, true, nil
}

func scanJobRow(row rowScanner) (Job, error) {
	var job Job
	var filterHits sql.NullString
	if err := row.Scan(&job.ID, &job.Source, &job.ExternalID, &job.URL, &job.Title, &job.CompanyName, &job.CompanyInfo, &job.Description, &job.SalaryMin, &job.SalaryMax, &job.Location, &job.RemoteType, &job.ProcessState, &job.ApplyState, &job.ContentHash, &filterHits, &job.FilterRevision, &job.ScoreRevision); err != nil {
		return Job{}, err
	}
	if filterHits.Valid && filterHits.String != "" {
		if err := json.Unmarshal([]byte(filterHits.String), &job.FilterHits); err != nil {
			return Job{}, fmt.Errorf("decode filter hits: %w", err)
		}
	}
	return job, nil
}

// maxScoreReason bounds one stored reason. It is a storage sanity bound, not
// the Scorer's length contract: that contract is counted in its own units and
// enforced in internal/agents before an answer is accepted, so this sits far
// above anything it lets through. A reason that got past the contract has
// already been paid for and must never be lost at write time.
const maxScoreReason = 500

func validateScore(input ScoreInput) error {
	if input.JobID <= 0 || !validRunner(input.Runner) || len([]rune(input.Reason)) > maxScoreReason {
		return errors.New("store: invalid score")
	}
	for _, value := range []int{input.Content, input.Benefit, input.Bonus, input.Industry} {
		if value < 0 || value > 100 {
			return errors.New("store: invalid score dimension")
		}
	}
	return nil
}
