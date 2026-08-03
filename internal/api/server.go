// Package api exposes the authenticated localhost JSON API used by the extension.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/pipeline"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

type Config struct {
	Addr, Token, ExtensionOrigin string
	// Dedupe carries the cross-source grouping thresholds the user's merge
	// decisions are applied with, so a manual merge picks the canonical copy by
	// the same rules an automatic one does.
	Dedupe store.DedupeOptions
	// ResidentWorker reports that this process carries the worker and holds its
	// lock. Without it the stages belong to a hand-driven `run --stage`, so the
	// automatic-processing switch has nothing to govern and a single-job request
	// would run beside a batch this process cannot see.
	ResidentWorker bool
}

// Triggerer starts one background fetch. Filter, score, and letter need no
// trigger: the resident worker consumes them continuously.
type Triggerer interface{ Start(context.Context) bool }

// Processor is the pipeline surface the API drives. Orchestration stays in
// pipeline; the API only forwards captures and the user's letter request.
type Processor interface {
	IngestList(context.Context, []crawler.RawJob) ([]pipeline.IngestResult, error)
	IngestJob(context.Context, crawler.RawJob) (pipeline.IngestResult, error)
	RequestLetter(context.Context, int64) error
	RequestReprocess(context.Context, int64) error
	ProcessJobNow(context.Context, int64) error
	FilterBudgetRemaining(context.Context) (int, bool, error)
	ScoreBudgetRemaining(context.Context) (int, bool, error)
}

type Server struct {
	store    *store.Store
	cfg      Config
	trigger  Triggerer
	pipeline Processor
	profiles *profile.Provider
	activate profile.ActivationFunc
	http     *http.Server
}

type ProfileConfig struct {
	Provider *profile.Provider
	Activate profile.ActivationFunc
}

func New(cfg Config, data *store.Store, trigger Triggerer, processor Processor, profileConfigs ...ProfileConfig) (*Server, error) {
	if data == nil || strings.TrimSpace(cfg.Token) == "" || strings.TrimSpace(cfg.ExtensionOrigin) == "" {
		return nil, errors.New("api: store, token, and extension origin are required")
	}
	if err := validateLoopbackAddr(cfg.Addr); err != nil {
		return nil, err
	}
	s := &Server{store: data, cfg: cfg, trigger: trigger, pipeline: processor}
	if len(profileConfigs) > 0 {
		s.profiles = profileConfigs[0].Provider
		s.activate = profileConfigs[0].Activate
	}
	s.http = &http.Server{Addr: cfg.Addr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	return s, nil
}

func (s *Server) ListenAndServe() error {
	err := s.http.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }
func (s *Server) Handler() http.Handler              { return s.routes() }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/jobs", s.jobs)
	mux.HandleFunc("/api/v1/jobs/", s.job)
	mux.HandleFunc("/api/v1/queue", s.queue)
	mux.HandleFunc("/api/v1/runs", s.runs)
	mux.HandleFunc("/api/v1/status", s.status)
	mux.HandleFunc("/api/v1/capture/list", s.captureList)
	mux.HandleFunc("/api/v1/capture/job", s.captureJob)
	mux.HandleFunc("/api/v1/duplicates", s.duplicates)
	mux.HandleFunc("/api/v1/duplicates/", s.duplicate)
	mux.HandleFunc("/api/v1/settings", s.settings)
	mux.HandleFunc("/api/v1/profile", s.profile)
	mux.HandleFunc("/api/v1/profile/reprocess", s.reprocessProfile)
	return s.authorize(mux)
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != s.cfg.ExtensionOrigin {
			writeError(w, http.StatusForbidden, "forbidden", "origin is not allowed")
			return
		}
		if r.Method == http.MethodOptions {
			if origin == "" {
				writeError(w, http.StatusForbidden, "forbidden", "preflight origin is required")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", s.cfg.ExtensionOrigin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		const prefix = "Bearer "
		value := strings.TrimPrefix(r.Header.Get("Authorization"), prefix)
		if !strings.HasPrefix(r.Header.Get("Authorization"), prefix) || subtle.ConstantTimeCompare([]byte(value), []byte(s.cfg.Token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token is required")
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.cfg.ExtensionOrigin)
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func validateLoopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("api: invalid address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("api: address must be loopback")
	}
	return nil
}

// InMemoryTrigger serializes calls and is suitable for one API process.
type InMemoryTrigger struct {
	mu      sync.Mutex
	running bool
	Run     func(context.Context)
}

func (t *InMemoryTrigger) Start(ctx context.Context) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running || t.Run == nil {
		return false
	}
	t.running = true
	go func() { defer func() { t.mu.Lock(); t.running = false; t.mu.Unlock() }(); t.Run(ctx) }()
	return true
}
