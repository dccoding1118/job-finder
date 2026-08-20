package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// VerificationSnapshot is a content-minimized view used by the checked-in E2E verifier.
// It retains hashes and contract fields, never full descriptions, letters, or Agent payloads.
type VerificationSnapshot struct {
	SchemaVersion int                      `json:"schema_version"`
	JournalMode   string                   `json:"journal_mode"`
	ForeignKeys   bool                     `json:"foreign_keys"`
	Tables        []string                 `json:"tables"`
	Jobs          []VerificationJob        `json:"jobs"`
	Runs          []Run                    `json:"runs"`
	AgentCalls    []VerificationAgentCalls `json:"agent_calls"`
	AgentPayloads VerificationAgentPayload `json:"agent_payloads"`
	Descriptions  VerificationDescriptions `json:"job_descriptions"`
}

// VerificationDescriptions is the same whole-table verdict for the stored JDs,
// which are masked at ingest and therefore never the source of PII downstream.
type VerificationDescriptions struct {
	Rows       int `json:"rows"`
	PIIMatches int `json:"pii_matches"`
	Masked     int `json:"masked"`
}

// VerificationAgentPayload is the whole-table verdict on the stored prompts and
// responses, reduced to counts so the evidence itself carries no payload text.
type VerificationAgentPayload struct {
	Rows int `json:"rows"`
	// PIIMatches must stay 0: a stored payload never keeps a mail address or a
	// mobile number.
	PIIMatches int `json:"pii_matches"`
	// Masked counts the rows a placeholder was substituted into, and
	// MaskedWithUsage how many of those still carry their token accounting —
	// masking replaces rejecting, so the two are equal.
	Masked          int `json:"masked"`
	MaskedWithUsage int `json:"masked_with_usage"`
}

type VerificationJob struct {
	ID                int64                         `json:"id"`
	Source            string                        `json:"source"`
	ExternalID        string                        `json:"external_id"`
	URL               string                        `json:"url"`
	Title             string                        `json:"title"`
	CompanyName       string                        `json:"company_name"`
	CompanyInfo       string                        `json:"company_info"`
	DescriptionSHA256 string                        `json:"description_sha256"`
	DescriptionLength int                           `json:"description_length"`
	SalaryMin         *int                          `json:"salary_min"`
	SalaryMax         *int                          `json:"salary_max"`
	Location          string                        `json:"location"`
	RemoteType        string                        `json:"remote_type"`
	ProcessState      string                        `json:"process_state"`
	ApplyState        *string                       `json:"apply_state"`
	ContentHash       *string                       `json:"content_hash"`
	FilterHits        []string                      `json:"filter_hits"`
	FilterRevision    *string                       `json:"filter_revision"`
	ScoreRevision     *string                       `json:"score_revision"`
	FilterOutcome     *string                       `json:"filter_outcome"`
	FilterConditions  []VerificationFilterCondition `json:"filter_conditions"`
	Score             *VerificationScore            `json:"score"`
	Letter            *VerificationLetter           `json:"letter"`
	Events            []VerificationEvent           `json:"events"`
}

// VerificationFilterCondition is one screening condition reduced to its contract
// fields. Rule is only set for the structural rules, whose names are a fixed
// vocabulary; a semantically derived condition's text comes from the JD and is
// therefore never carried into the snapshot.
type VerificationFilterCondition struct {
	Rule     string `json:"rule"`
	Kind     string `json:"kind"`
	Category string `json:"category"`
	Verdict  string `json:"verdict"`
}

type VerificationScore struct {
	Content       int     `json:"content_fit"`
	Benefit       int     `json:"benefit_fit"`
	Bonus         int     `json:"bonus_fit"`
	Industry      int     `json:"industry_fit"`
	Total         float64 `json:"total"`
	ReasonSHA256  string  `json:"reason_sha256"`
	Runner        string  `json:"runner"`
	ScoreRevision *string `json:"score_revision"`
}

type VerificationLetter struct {
	Status                string  `json:"status"`
	Rounds                int     `json:"rounds"`
	ContentSHA256         string  `json:"content_sha256"`
	ReviewLogSHA256       string  `json:"review_log_sha256"`
	ReviewEntries         int     `json:"review_entries"`
	RunnerDraft           string  `json:"runner_draft"`
	RunnerReview          string  `json:"runner_review"`
	HasNamePlaceholder    bool    `json:"has_name_placeholder"`
	HasContactPlaceholder bool    `json:"has_contact_placeholder"`
	FilterRevision        *string `json:"filter_revision"`
	ScoreRevision         *string `json:"score_revision"`
}

type VerificationEvent struct {
	Axis      string `json:"axis"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
}

type VerificationAgentCalls struct {
	Role           string  `json:"role"`
	Runner         string  `json:"runner"`
	OK             bool    `json:"ok"`
	Count          int     `json:"count"`
	FilterRevision *string `json:"filter_revision"`
	ScoreRevision  *string `json:"score_revision"`
}

// SnapshotForVerification reads only safe summaries needed to reconstruct E2E assertions.
func (s *Store) SnapshotForVerification(ctx context.Context) (VerificationSnapshot, error) {
	var out VerificationSnapshot
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&out.SchemaVersion); err != nil {
		return out, fmt.Errorf("verification: read schema version: %w", err)
	}
	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&out.JournalMode); err != nil {
		return out, fmt.Errorf("verification: read journal mode: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return out, fmt.Errorf("verification: read foreign keys: %w", err)
	}
	out.ForeignKeys = foreignKeys == 1

	rows, err := s.db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return out, fmt.Errorf("verification: list tables: %w", err)
	}
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			_ = rows.Close()
			return out, fmt.Errorf("verification: scan table: %w", scanErr)
		}
		out.Tables = append(out.Tables, name)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return out, fmt.Errorf("verification: close tables: %w", closeErr)
	}

	jobs, err := s.ListJobs(ctx, JobFilter{}, JobSortNewest)
	if err != nil {
		return out, err
	}
	out.Jobs = make([]VerificationJob, 0, len(jobs))
	for _, job := range jobs {
		detail, found, detailErr := s.GetJobDetail(ctx, job.ID)
		if detailErr != nil || !found {
			return out, fmt.Errorf("verification: read job %d: %w", job.ID, detailErr)
		}
		item := VerificationJob{
			ID: job.ID, Source: job.Source, ExternalID: job.ExternalID, URL: job.URL,
			Title: job.Title, CompanyName: job.CompanyName, CompanyInfo: job.CompanyInfo,
			SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax, Location: job.Location,
			RemoteType: job.RemoteType, ProcessState: job.ProcessState, ApplyState: job.ApplyState,
			ContentHash: job.ContentHash, FilterHits: []string{}, FilterRevision: job.FilterRevision,
			ScoreRevision: job.ScoreRevision, Events: []VerificationEvent{},
			FilterConditions: []VerificationFilterCondition{},
		}
		if detail.Filter != nil {
			outcome := detail.Filter.Outcome
			item.FilterOutcome = &outcome
			for _, condition := range detail.Filter.Conditions {
				entry := VerificationFilterCondition{Kind: condition.Kind, Category: condition.Category, Verdict: condition.Verdict}
				if condition.Category == "other" {
					entry.Rule = condition.Text
				}
				item.FilterConditions = append(item.FilterConditions, entry)
			}
		}
		if job.Description != nil {
			item.DescriptionSHA256 = digest(*job.Description)
			item.DescriptionLength = len(*job.Description)
		}
		var filterJSON sql.NullString
		if queryErr := s.db.QueryRowContext(ctx, "SELECT filter_hits FROM jobs WHERE id=?", job.ID).Scan(&filterJSON); queryErr != nil {
			return out, fmt.Errorf("verification: read filter hits: %w", queryErr)
		}
		if filterJSON.Valid && filterJSON.String != "" {
			if decodeErr := json.Unmarshal([]byte(filterJSON.String), &item.FilterHits); decodeErr != nil {
				return out, fmt.Errorf("verification: decode filter hits: %w", decodeErr)
			}
		}
		if detail.Score != nil {
			item.Score = &VerificationScore{
				Content: detail.Score.Content, Benefit: detail.Score.Benefit,
				Bonus: detail.Score.Bonus, Industry: detail.Score.Industry, Total: detail.Score.Total,
				ReasonSHA256: digest(detail.Score.Reason), Runner: detail.Score.Runner, ScoreRevision: detail.Score.ScoreRevision,
			}
		}
		if detail.Letter != nil {
			var draft, review string
			if queryErr := s.db.QueryRowContext(ctx, `SELECT COALESCE(a.runner_draft, ''), COALESCE(a.runner_review, '')
				FROM letters l LEFT JOIN letter_attempts a ON a.id = l.attempt_id
				WHERE l.job_id=? ORDER BY l.created_at DESC, l.id DESC LIMIT 1`, job.ID).Scan(&draft, &review); queryErr != nil {
				return out, fmt.Errorf("verification: read letter runners: %w", queryErr)
			}
			entries := 0
			if strings.TrimSpace(detail.Letter.ReviewLog) != "" {
				entries = len(strings.Split(detail.Letter.ReviewLog, "\n"))
			}
			item.Letter = &VerificationLetter{
				Status: detail.Letter.Status, Rounds: detail.Letter.Rounds,
				ContentSHA256: digest(detail.Letter.Content), ReviewLogSHA256: digest(detail.Letter.ReviewLog),
				ReviewEntries: entries, RunnerDraft: draft, RunnerReview: review,
				HasNamePlaceholder:    strings.Contains(detail.Letter.Content, "[你的姓名]"),
				HasContactPlaceholder: strings.Contains(detail.Letter.Content, "[你的聯絡方式]"),
				FilterRevision:        detail.Letter.FilterRevision,
				ScoreRevision:         detail.Letter.ScoreRevision,
			}
		}
		for _, event := range detail.Events {
			item.Events = append(item.Events, VerificationEvent{Axis: event.Axis, FromState: event.FromState, ToState: event.ToState})
		}
		out.Jobs = append(out.Jobs, item)
	}

	out.Runs, err = s.ListRuns(ctx)
	if err != nil {
		return out, err
	}
	callRows, err := s.db.QueryContext(ctx, `SELECT role, runner, ok, filter_revision, score_revision, COUNT(*) FROM agent_calls GROUP BY role, runner, ok, filter_revision, score_revision ORDER BY role, runner, ok, filter_revision, score_revision`)
	if err != nil {
		return out, fmt.Errorf("verification: summarize agent calls: %w", err)
	}
	defer func() { _ = callRows.Close() }()
	out.AgentCalls = []VerificationAgentCalls{}
	for callRows.Next() {
		var call VerificationAgentCalls
		var ok int
		if scanErr := callRows.Scan(&call.Role, &call.Runner, &ok, &call.FilterRevision, &call.ScoreRevision, &call.Count); scanErr != nil {
			return out, fmt.Errorf("verification: scan agent calls: %w", scanErr)
		}
		call.OK = ok == 1
		out.AgentCalls = append(out.AgentCalls, call)
	}
	if callErr := callRows.Err(); callErr != nil {
		return out, callErr
	}
	out.AgentPayloads, err = s.agentPayloadSummary(ctx)
	if err != nil {
		return out, err
	}
	out.Descriptions, err = s.descriptionSummary(ctx)
	if err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) descriptionSummary(ctx context.Context) (VerificationDescriptions, error) {
	var summary VerificationDescriptions
	rows, err := s.db.QueryContext(ctx, "SELECT description FROM jobs WHERE description IS NOT NULL")
	if err != nil {
		return summary, fmt.Errorf("verification: read job descriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var description string
		if scanErr := rows.Scan(&description); scanErr != nil {
			return summary, fmt.Errorf("verification: scan job description: %w", scanErr)
		}
		summary.Rows++
		if hasPII(description) {
			summary.PIIMatches++
		}
		if containsPlaceholder(description) {
			summary.Masked++
		}
	}
	return summary, rows.Err()
}

func (s *Store) agentPayloadSummary(ctx context.Context) (VerificationAgentPayload, error) {
	var summary VerificationAgentPayload
	rows, err := s.db.QueryContext(ctx, "SELECT input, output, input_tokens, output_tokens FROM agent_calls")
	if err != nil {
		return summary, fmt.Errorf("verification: read agent payloads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var input, output string
		var inputTokens, outputTokens int
		if scanErr := rows.Scan(&input, &output, &inputTokens, &outputTokens); scanErr != nil {
			return summary, fmt.Errorf("verification: scan agent payload: %w", scanErr)
		}
		summary.Rows++
		if hasPII(input) || hasPII(output) {
			summary.PIIMatches++
		}
		if containsPlaceholder(input) || containsPlaceholder(output) {
			summary.Masked++
			if inputTokens > 0 || outputTokens > 0 {
				summary.MaskedWithUsage++
			}
		}
	}
	return summary, rows.Err()
}

func containsPlaceholder(text string) bool {
	return strings.Contains(text, "[EMAIL]") || strings.Contains(text, "[PHONE]")
}

func digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
