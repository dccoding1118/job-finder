package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	DB      struct{ Path string }
	Profile struct {
		Path     string
		Denylist string
	}
	Sources struct {
		Yourator struct {
			Enabled         bool   `yaml:"enabled"`
			BaseURL         string `yaml:"base_url"`
			MaxPages        int    `yaml:"max_pages"`
			RequestDelayMin string `yaml:"request_delay_min"`
			RequestDelayMax string `yaml:"request_delay_max"`
			RetryMax        int    `yaml:"retry_max"`
			RetryBackoff    string `yaml:"retry_backoff"`
			RequestTimeout  string `yaml:"request_timeout"`
			CheckRobots     bool   `yaml:"check_robots"`
		} `yaml:"yourator"`
	} `yaml:"sources"`
	Scoring struct {
		HardSkillWeight float64 `yaml:"hard_skill_weight"`
		DomainWeight    float64 `yaml:"domain_weight"`
		SeniorityWeight float64 `yaml:"seniority_weight"`
		ConditionWeight float64 `yaml:"condition_weight"`
		DirectionWeight float64 `yaml:"direction_weight"`
		Threshold       *float64
	} `yaml:"scoring"`
	LLM struct {
		MaxScorePerDay  int        `yaml:"max_score_per_day"`
		MaxLetterPerDay int        `yaml:"max_letter_per_day"`
		MaxLetterLength int        `yaml:"max_letter_length"`
		MinInterval     string     `yaml:"min_interval"`
		Timeout         string     `yaml:"timeout"`
		Roles           roleRoutes `yaml:"roles"`
	} `yaml:"llm"`
	Worker struct {
		ScanInterval string `yaml:"scan_interval"`
	} `yaml:"worker"`
	API struct {
		Addr            string `yaml:"addr"`
		Token           string `yaml:"token"`
		ExtensionOrigin string `yaml:"extension_origin"`
	} `yaml:"api"`
}

type (
	roleEndpoint struct {
		Agent string `yaml:"agent"`
		Model string `yaml:"model"`
	}
	roleRoute struct {
		Primary  roleEndpoint `yaml:"primary"`
		Fallback roleEndpoint `yaml:"fallback"`
	}
	roleRoutes struct {
		Scorer   roleRoute `yaml:"scorer"`
		Drafter  roleRoute `yaml:"drafter"`
		Reviewer roleRoute `yaml:"reviewer"`
	}
)

func defaultScoringWeights() [5]float64 {
	return [5]float64{.30, .15, .15, .20, .20}
}

// newRunCmd fetches. Filter, score, and letter belong to the resident worker in
// the API server; `--stage` only exists to drive one of them by hand while that
// worker is stopped.
func newRunCmd() *cobra.Command {
	var path, stage, trigger string
	var limit int
	c := &cobra.Command{Use: "run", Short: "Fetch jobs from the automated sources", RunE: func(cmd *cobra.Command, _ []string) error {
		if stage != "" && stage != "fetch" && stage != "filter" && stage != "score" && stage != "letter" {
			return fmt.Errorf("config: invalid stage %q", stage)
		}
		rt, err := loadRuntime(path)
		if err != nil {
			return err
		}
		defer rt.close()
		if stage != "" && stage != "fetch" {
			return runStage(cmd, rt, stage, limit)
		}
		return runFetch(cmd, rt, trigger)
	}}
	c.Flags().StringVar(&path, "config", "config.yaml", "path to config.yaml")
	c.Flags().StringVar(&stage, "stage", "", "fetch, or drive one worker stage by hand: filter, score, or letter")
	c.Flags().IntVar(&limit, "limit", 30, "maximum jobs for a hand-driven filter, score, or letter stage")
	c.Flags().StringVar(&trigger, "trigger", store.RunTriggerManualCLI, "run trigger: timer, manual-cli, or manual-extension")
	return c
}

// runFetch records the fetch facts of one round and exits without waiting for
// any LLM stage; the worker consumes what it stored.
func runFetch(cmd *cobra.Command, rt *runtime, trigger string) error {
	source, spec, err := rt.fetchSource()
	if err != nil {
		return err
	}
	rt.pipeline.Source = source
	runID, err := rt.store.StartRun(cmd.Context(), trigger)
	if err != nil {
		return err
	}
	stats := store.NewRunStats()
	stats["queries"] = len(spec.Queries)
	var runErr error
	defer func() { _ = rt.store.FinishRun(cmd.Context(), runID, stats, errorSummary(runErr)) }()
	fetched, err := rt.pipeline.Fetch(cmd.Context(), spec, &runID)
	stats["fetched"], stats["new"] = fetched.Fetched, fetched.New
	if err != nil {
		runErr = err
		stats["errors"]++
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "fetched: %d\nnew: %d\n", fetched.Fetched, fetched.New)
	return nil
}

// runStage is the debugging entry for a second process. It holds the worker
// lock, so it exits at once while the API server's worker is resident.
func runStage(cmd *cobra.Command, rt *runtime, stage string, limit int) error {
	if err := lockWorker(rt.cfg.DB.Path); err != nil {
		return err
	}
	defer unlockWorker(rt.cfg.DB.Path)
	switch stage {
	case "filter":
		stats, err := rt.pipeline.FilterJobsWithStats(cmd.Context(), limit)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "filtered: %d\n", stats.FilteredOut)
		return err
	case "score":
		stats, err := rt.pipeline.ScoreWithStats(cmd.Context(), limit)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "scored: %d\n", stats.Processed)
		return err
	default:
		stats, err := rt.pipeline.LetterWithStats(cmd.Context(), limit)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "lettered: %d\n", stats.Processed)
		return err
	}
}

func routedAgents(routes roleRoutes, timeout time.Duration) (agents.Scorer, agents.Drafter, agents.Reviewer, error) {
	runner := func(endpoint roleEndpoint) (agents.Runner, error) {
		if strings.TrimSpace(endpoint.Model) == "" {
			return nil, fmt.Errorf("config: llm.roles endpoint model is required")
		}
		switch endpoint.Agent {
		case "claude":
			return agents.ClaudeRunner(endpoint.Model, timeout), nil
		case "codex":
			return agents.CodexRunner(endpoint.Model, timeout), nil
		default:
			return nil, fmt.Errorf("config: llm.roles endpoint agent must be claude or codex")
		}
	}
	resolve := func(route roleRoute) (agents.Runner, agents.Runner, error) {
		primary, err := runner(route.Primary)
		if err != nil {
			return nil, nil, err
		}
		fallback, err := runner(route.Fallback)
		if err != nil {
			return nil, nil, err
		}
		return primary, fallback, nil
	}
	sp, sf, err := resolve(routes.Scorer)
	if err != nil {
		return agents.Scorer{}, agents.Drafter{}, agents.Reviewer{}, err
	}
	dp, df, err := resolve(routes.Drafter)
	if err != nil {
		return agents.Scorer{}, agents.Drafter{}, agents.Reviewer{}, err
	}
	rp, rf, err := resolve(routes.Reviewer)
	if err != nil {
		return agents.Scorer{}, agents.Drafter{}, agents.Reviewer{}, err
	}
	return agents.Scorer{Primary: sp, Fallback: sf}, agents.Drafter{Primary: dp, Fallback: df}, agents.Reviewer{Primary: rp, Fallback: rf}, nil
}

// lockWorker keeps stage consumption to one process. The resident worker holds
// it for as long as the API server runs, which is what makes a hand-driven
// stage exit instead of competing with it.
func lockWorker(dbPath string) error {
	// #nosec G304 -- the lock is derived solely from the explicit local SQLite path.
	f, err := os.OpenFile(workerLockPath(dbPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("another process is consuming the pipeline stages")
		}
		return fmt.Errorf("create worker lock: %w", err)
	}
	return f.Close()
}

func unlockWorker(dbPath string) { _ = os.Remove(workerLockPath(dbPath)) }

func workerLockPath(dbPath string) string { return filepath.Clean(dbPath) + ".worker.lock" }
func errorSummary(err error) string {
	if err == nil {
		return ""
	}
	return "pipeline failed"
}

func directionQueries(p profile.Profile) []crawler.SearchQuery {
	limit := len(p.Preferences.Directions)
	if limit > 3 {
		limit = 3
	}
	out := make([]crawler.SearchQuery, 0, limit)
	for _, direction := range p.Preferences.Directions[:limit] {
		out = append(out, crawler.SearchQuery{Direction: direction.Key, Keywords: append([]string(nil), direction.Keywords...)})
	}
	return out
}

func parseFileConfig(data []byte) (fileConfig, error) {
	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}
