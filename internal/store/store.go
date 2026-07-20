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
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 2

//go:embed schema.sql
var schemaSQL string

// upgrades[v] brings a database at user_version v to v+1; a fresh database is
// created from schemaSQL, which already reflects the newest version.
var upgrades = map[int]string{
	1: `ALTER TABLE jobs ADD COLUMN discovered_by_run_id INTEGER REFERENCES runs(id);
	CREATE INDEX IF NOT EXISTS jobs_discovered_by_run_idx ON jobs(discovered_by_run_id);
	CREATE INDEX IF NOT EXISTS agent_calls_role_created_idx ON agent_calls(role, created_at);`,
}

var piiPattern = regexp.MustCompile(`(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}|(?:\+886|0)9\d{8}`)

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
}

type Job struct {
	ID           int64
	Source       string
	ExternalID   string
	URL          string
	Title        string
	CompanyName  string
	CompanyInfo  string
	Description  *string
	SalaryMin    *int
	SalaryMax    *int
	Location     string
	RemoteType   string
	ProcessState string
	ApplyState   *string
	ContentHash  *string
	FilterHits   []string
	ScoreTotal   *float64
}

type UpsertResult struct {
	Job     Job
	Created bool
	Changed bool
}

type ScoreInput struct {
	JobID                                              int64
	HardSkill, Domain, Seniority, Condition, Direction int
	Total                                              float64
	Reason, Runner                                     string
}

type LetterInput struct {
	JobID                      int64
	Content, Status, ReviewLog string
	Rounds                     int
	RunnerDraft, RunnerReview  string
}

type AgentCallInput struct {
	JobID                       *int64
	Role, Runner, Input, Output string
	OK                          bool
	DurationMS                  int64
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
	return store, nil
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
			(source, external_id, url, title, company_name, company_info, description, salary_min, salary_max, location, remote_type, content_hash, process_state, discovered_by_run_id, first_seen_at, last_seen_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.Source, input.ExternalID, input.URL, input.Title, input.CompanyName, input.CompanyInfo, description, input.SalaryMin, input.SalaryMax, input.Location, input.RemoteType, contentHashValue, state, runID, now, now, now)
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
		if err := tx.Commit(); err != nil {
			return UpsertResult{}, fmt.Errorf("commit inserted job: %w", err)
		}
		job := Job{ID: id, Source: input.Source, ExternalID: input.ExternalID, URL: input.URL, Title: input.Title, CompanyName: input.CompanyName, CompanyInfo: input.CompanyInfo, Description: input.Description, SalaryMin: input.SalaryMin, SalaryMax: input.SalaryMax, Location: input.Location, RemoteType: input.RemoteType, ProcessState: state, ContentHash: stringPtr(hash, !partial)}
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
	if existing.ContentHash == nil || canReset(existing.ProcessState) {
		newState = "new"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET url=?, title=?, company_name=?, company_info=?, description=?, salary_min=?, salary_max=?, location=?, remote_type=?, content_hash=?, process_state=?, updated_at=?, last_seen_at=? WHERE id=?`, input.URL, input.Title, input.CompanyName, input.CompanyInfo, *input.Description, input.SalaryMin, input.SalaryMax, input.Location, input.RemoteType, hash, newState, now, now, existing.ID); err != nil {
		return UpsertResult{}, fmt.Errorf("update changed job: %w", err)
	}
	if newState != existing.ProcessState {
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO scores (job_id, dim_hard_skill, dim_domain, dim_seniority, dim_condition, dim_direction, total, reason, runner, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.JobID, input.HardSkill, input.Domain, input.Seniority, input.Condition, input.Direction, input.Total, input.Reason, input.Runner, s.timestamp())
	if err != nil {
		return fmt.Errorf("save score: %w", err)
	}
	return nil
}

func (s *Store) SaveLetter(ctx context.Context, input LetterInput) error {
	if input.JobID <= 0 || (input.Status != "approved" && input.Status != "failed") || input.Rounds < 1 || input.Content == "" || input.RunnerDraft == "" || (input.Status == "approved" && input.RunnerReview == "") {
		return errors.New("store: invalid letter")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO letters (job_id, content, status, rounds, review_log, runner_draft, runner_review, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, input.JobID, input.Content, input.Status, input.Rounds, input.ReviewLog, input.RunnerDraft, input.RunnerReview, s.timestamp())
	if err != nil {
		return fmt.Errorf("save letter: %w", err)
	}
	return nil
}

func (s *Store) SaveAgentCall(ctx context.Context, input AgentCallInput) error {
	if input.Role != "scorer" && input.Role != "drafter" && input.Role != "reviewer" && input.Role != "calibrator" {
		return fmt.Errorf("store: invalid agent role %q", input.Role)
	}
	if !validRunner(input.Runner) || input.DurationMS < 0 || piiPattern.MatchString(input.Input) || piiPattern.MatchString(input.Output) {
		return errors.New("store: invalid agent call")
	}
	ok := 0
	if input.OK {
		ok = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_calls (job_id, role, runner, input, output, ok, duration_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, input.JobID, input.Role, input.Runner, input.Input, input.Output, ok, input.DurationMS, s.timestamp())
	if err != nil {
		return fmt.Errorf("save agent call: %w", err)
	}
	return nil
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
		"discovered":       {"filtered_out": true, "new": true},
		"new":              {"filtered_out": true, "queued": true},
		"queued":           {"scored": true, "shortlisted": true},
		"shortlisted":      {"letter_requested": true},
		"letter_requested": {"letter_ready": true, "letter_failed": true},
		"letter_failed":    {"letter_requested": true},
	}[from][to]
}

func validApplyTransition(from, to string) bool {
	return map[string]map[string]bool{"pending": {"applied": true, "dropped": true}, "applied": {"interview": true, "ghosted": true, "dropped": true}, "interview": {"offer": true, "ghosted": true, "dropped": true}}[from][to]
}

func canReset(state string) bool {
	return state != "filtered_out" && state != "scored" && state != "letter_ready"
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

func insertEvent(ctx context.Context, tx *sql.Tx, jobID int64, axis, from, to, note, createdAt string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO status_events (job_id, axis, from_state, to_state, note, created_at) VALUES (?, ?, ?, ?, ?, ?)", jobID, axis, from, to, nullableString(note), createdAt)
	if err != nil {
		return fmt.Errorf("insert status event: %w", err)
	}
	return nil
}

// jobColumns is the single job projection every reader scans with scanJobRow.
const jobColumns = "id, source, external_id, url, title, company_name, company_info, description, salary_min, salary_max, location, remote_type, process_state, apply_state, content_hash, filter_hits"

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
	if err := row.Scan(&job.ID, &job.Source, &job.ExternalID, &job.URL, &job.Title, &job.CompanyName, &job.CompanyInfo, &job.Description, &job.SalaryMin, &job.SalaryMax, &job.Location, &job.RemoteType, &job.ProcessState, &job.ApplyState, &job.ContentHash, &filterHits); err != nil {
		return Job{}, err
	}
	if filterHits.Valid && filterHits.String != "" {
		if err := json.Unmarshal([]byte(filterHits.String), &job.FilterHits); err != nil {
			return Job{}, fmt.Errorf("decode filter hits: %w", err)
		}
	}
	return job, nil
}

func validateScore(input ScoreInput) error {
	if input.JobID <= 0 || !validRunner(input.Runner) || len([]rune(input.Reason)) > 50 {
		return errors.New("store: invalid score")
	}
	for _, value := range []int{input.HardSkill, input.Domain, input.Seniority, input.Condition, input.Direction} {
		if value < 0 || value > 100 {
			return errors.New("store: invalid score dimension")
		}
	}
	return nil
}
