package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestMigrationFromVersionTwoAddsProfileRevisionColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	created := openTestStore(t, path)
	closeTestStore(t, created)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ALTER TABLE jobs DROP COLUMN profile_revision",
		"ALTER TABLE scores DROP COLUMN profile_revision",
		"ALTER TABLE letters DROP COLUMN profile_revision",
		"ALTER TABLE agent_calls DROP COLUMN profile_revision",
		"PRAGMA user_version = 2",
	} {
		if _, err := raw.Exec(statement); err != nil {
			_ = raw.Close()
			t.Fatalf("prepare v2 database: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	migrated := openTestStore(t, path)
	defer closeTestStore(t, migrated)
	for _, table := range []string{"jobs", "scores", "letters", "agent_calls"} {
		rows, err := migrated.db.Query("PRAGMA table_info(" + table + ")") // #nosec G202 -- table names are fixed test constants.
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			found = found || name == "profile_revision"
		}
		_ = rows.Close()
		if !found {
			t.Fatalf("%s.profile_revision is missing after migration", table)
		}
	}
}

func TestActivateProfileReprocessesEligibleAndProtectsLetterHistory(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()

	partial := fullJob("")
	partial.ExternalID, partial.Description, partial.ProfileRevision = "partial", nil, "sha256:old"
	partialJob, err := data.UpsertJob(ctx, partial, nil)
	if err != nil {
		t.Fatal(err)
	}
	full := fullJob("complete description")
	full.ExternalID, full.ProfileRevision = "full", "sha256:old"
	fullJobResult, err := data.UpsertJob(ctx, full, nil)
	if err != nil {
		t.Fatal(err)
	}
	if transitionErr := data.TransitionProcess(ctx, fullJobResult.Job.ID, "queued"); transitionErr != nil {
		t.Fatal(transitionErr)
	}
	protected := fullJob("protected description")
	protected.ExternalID, protected.ProfileRevision = "protected", "sha256:old"
	protectedJob, err := data.UpsertJob(ctx, protected, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "shortlisted", "letter_requested"} {
		if transitionErr := data.TransitionProcess(ctx, protectedJob.Job.ID, state); transitionErr != nil {
			t.Fatal(transitionErr)
		}
	}

	stats, err := data.ActivateProfile(ctx, "sha256:new", func(job Job) []string {
		if job.ID == partialJob.Job.ID {
			return []string{"locations"}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.PartialScreened != 1 || stats.Requeued != 1 || stats.Protected != 1 {
		t.Fatalf("activation stats = %+v", stats)
	}
	partialDetail, _, _ := data.GetJobDetail(ctx, partialJob.Job.ID)
	fullDetail, _, _ := data.GetJobDetail(ctx, fullJobResult.Job.ID)
	protectedDetail, _, _ := data.GetJobDetail(ctx, protectedJob.Job.ID)
	if partialDetail.Job.ProcessState != "filtered_out" || fullDetail.Job.ProcessState != "new" || protectedDetail.Job.ProcessState != "letter_requested" {
		t.Fatalf("states = %s/%s/%s", partialDetail.Job.ProcessState, fullDetail.Job.ProcessState, protectedDetail.Job.ProcessState)
	}
	if partialDetail.Job.ProfileRevision == nil || *partialDetail.Job.ProfileRevision != "sha256:new" || protectedDetail.Job.ProfileRevision == nil || *protectedDetail.Job.ProfileRevision != "sha256:old" {
		t.Fatal("activation changed the wrong revisions")
	}
	second, err := data.ActivateProfile(ctx, "sha256:new", func(Job) []string { return nil })
	if err != nil || second.Unchanged != 2 || second.Protected != 1 {
		t.Fatalf("idempotent activation = %+v err=%v", second, err)
	}
}

func TestRevisionCASRejectsOldFilterAndScore(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	input := fullJob("description")
	input.ProfileRevision = "sha256:new"
	created, err := data.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if commitErr := data.CommitFilter(ctx, created.Job.ID, "sha256:old", nil); !errors.Is(commitErr, ErrStaleRevision) {
		t.Fatalf("old filter CAS error = %v", commitErr)
	}
	if commitErr := data.CommitFilter(ctx, created.Job.ID, "sha256:new", nil); commitErr != nil {
		t.Fatal(commitErr)
	}
	score := ScoreInput{JobID: created.Job.ID, HardSkill: 80, Domain: 80, Seniority: 80, Condition: 80, Direction: 80, Total: 80, Reason: "fit", Runner: "claude", ProfileRevision: "sha256:old"}
	if commitErr := data.CommitScore(ctx, score, "shortlisted"); !errors.Is(commitErr, ErrStaleRevision) {
		t.Fatalf("old score CAS error = %v", commitErr)
	}
	score.ProfileRevision = "sha256:new"
	if commitErr := data.CommitScore(ctx, score, "shortlisted"); commitErr != nil {
		t.Fatal(commitErr)
	}
	current, err := data.CurrentScore(ctx, created.Job.ID)
	if err != nil || current == nil || current.ProfileRevision == nil || *current.ProfileRevision != "sha256:new" {
		t.Fatalf("current score = %+v err=%v", current, err)
	}
}
