package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/logging"
	"github.com/dccoding1118/job-finder/internal/pipeline"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// runtime is the configured store, pipeline, and fetch source every command
// works from, so `run` and `serve` cannot drift in how they read config.yaml.
type runtime struct {
	cfg          fileConfig
	store        *store.Store
	pipeline     pipeline.Pipeline
	provider     *profile.Provider
	scanInterval time.Duration
	workerPaused bool
	logSink      io.Closer
}

func loadRuntime(path string) (*runtime, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- explicit local configuration path from CLI.
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := parseFileConfig(data)
	if err != nil {
		return nil, err
	}
	// The log sink is installed before anything else can produce a record, so a
	// failure in the rest of the setup is reported wherever this deployment
	// reads its logs rather than only on a stderr nobody collects.
	logSink, err := logging.Setup(cfg.Log.File, cfg.Log.MaxSizeMB, cfg.Log.Keep)
	if err != nil {
		return nil, err
	}
	denylist, err := profile.LoadDenylist(cfg.Profile.Denylist)
	if err != nil {
		return nil, err
	}
	interval, err := time.ParseDuration(cfg.LLM.MinInterval)
	if err != nil {
		return nil, fmt.Errorf("config: llm.min_interval: %w", err)
	}
	timeout, err := time.ParseDuration(cfg.LLM.Timeout)
	if err != nil || timeout <= 0 {
		return nil, fmt.Errorf("config: llm.timeout must be a positive duration")
	}
	scanInterval := pipeline.DefaultScanInterval
	if cfg.Worker.ScanInterval != "" {
		if scanInterval, err = time.ParseDuration(cfg.Worker.ScanInterval); err != nil || scanInterval <= 0 {
			return nil, fmt.Errorf("config: worker.scan_interval must be a positive duration")
		}
	}
	db, err := store.Open(cfg.DB.Path)
	if err != nil {
		return nil, err
	}
	provider, err := profile.NewProvider(cfg.Profile.Path, denylist)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	p := pipeline.Pipeline{
		Store: db, Provider: provider,
		Denylist: denylist, Weights: [4]float64{cfg.Scoring.ContentWeight, cfg.Scoring.BenefitWeight, cfg.Scoring.BonusWeight, cfg.Scoring.IndustryWeight},
		DedupeEnabled:   cfg.Dedupe.Enabled == nil || *cfg.Dedupe.Enabled,
		Dedupe:          store.DedupeOptions{TitleSimilarityThreshold: cfg.Dedupe.TitleSimilarityThreshold, SourcePriority: cfg.Dedupe.SourcePriority},
		MaxFilterPerDay: cfg.LLM.MaxFilterPerDay, MaxScorePerDay: cfg.LLM.MaxScorePerDay,
		MaxLetterPerDay: cfg.LLM.MaxLetterPerDay, MaxLetterLength: cfg.LLM.MaxLetterLength,
		MaxLetterRounds: cfg.LLM.MaxLetterRounds, MinInterval: interval,
		// Every copy of this value made later — the API's and the resident worker's
		// — reports into this one record, so what the process has in flight is
		// visible whichever of them is running it.
		Activity: &pipeline.Activity{},
	}
	if cfg.Scoring.Threshold == nil {
		p.Threshold = 75
	} else {
		p.Threshold = *cfg.Scoring.Threshold
	}
	if p.Weights == [4]float64{} {
		p.Weights = defaultScoringWeights()
	}
	if cfg.Scoring.Baseline != nil {
		p.Baseline = *cfg.Scoring.Baseline
	}
	if p.Screener, p.Scorer, p.Drafter, p.Reviewer, err = routedAgents(cfg.LLM.Roles, timeout); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &runtime{cfg: cfg, store: db, pipeline: p, provider: provider, scanInterval: scanInterval, workerPaused: cfg.Worker.Paused, logSink: logSink}, nil
}

func (r *runtime) close() {
	_ = r.store.Close()
	if r.logSink != nil {
		_ = r.logSink.Close()
	}
}

// fetchSource builds the Yourator adapter and the search spec derived from the
// Profile directions.
func (r *runtime) fetchSource() (crawler.Source, crawler.SearchSpec, error) {
	snapshot, err := r.provider.Ready()
	if err != nil {
		return nil, crawler.SearchSpec{}, err
	}
	source := r.cfg.Sources.Yourator
	if !source.Enabled {
		return nil, crawler.SearchSpec{}, fmt.Errorf("config: sources.yourator is disabled")
	}
	requestDelayMin, err := time.ParseDuration(source.RequestDelayMin)
	if err != nil || requestDelayMin < 0 {
		return nil, crawler.SearchSpec{}, fmt.Errorf("config: sources.yourator.request_delay_min must be a non-negative duration")
	}
	requestDelayMax, err := time.ParseDuration(source.RequestDelayMax)
	if err != nil || requestDelayMax < requestDelayMin {
		return nil, crawler.SearchSpec{}, fmt.Errorf("config: sources.yourator.request_delay_max must be at least request_delay_min")
	}
	retryBackoff, err := time.ParseDuration(source.RetryBackoff)
	if err != nil || retryBackoff < 0 || source.RetryMax < 0 {
		return nil, crawler.SearchSpec{}, fmt.Errorf("config: invalid Yourator retry settings")
	}
	requestTimeout := 30 * time.Second
	if source.RequestTimeout != "" {
		if requestTimeout, err = time.ParseDuration(source.RequestTimeout); err != nil || requestTimeout <= 0 {
			return nil, crawler.SearchSpec{}, fmt.Errorf("config: sources.yourator.request_timeout must be a positive duration")
		}
	}
	adapter := crawler.Yourator{BaseURL: source.BaseURL, Client: &http.Client{Timeout: requestTimeout}, RequestDelayMin: requestDelayMin, RequestDelayMax: requestDelayMax, RetryMax: source.RetryMax, RetryBackoff: retryBackoff, CheckRobots: source.CheckRobots}
	spec := crawler.SearchSpec{Queries: directionQueries(*snapshot.Profile), MaxPages: source.MaxPages}
	return adapter, spec, nil
}
