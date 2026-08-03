package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dccoding1118/job-finder/internal/store"
	"github.com/spf13/cobra"
)

// newVerifyCmd provides deterministic local fixtures used only by the checked-in verifier.
func newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "verify", Hidden: true}
	cmd.AddCommand(newMockSourceCmd())
	cmd.AddCommand(newVerificationSnapshotCmd())
	return cmd
}

func newMockSourceCmd() *cobra.Command {
	var addr, requestLog string
	cmd := &cobra.Command{
		Use:    "mock-source",
		Hidden: true,
		Short:  "Serve deterministic Yourator-compatible verification fixtures",
		RunE: func(_ *cobra.Command, _ []string) error {
			var logFile *os.File
			var logMu sync.Mutex
			if requestLog != "" {
				var err error
				logFile, err = os.OpenFile(requestLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- explicit verifier-only path.
				if err != nil {
					return fmt.Errorf("open fixture request log: %w", err)
				}
				defer func() { _ = logFile.Close() }()
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
			mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("User-agent: *\nDisallow: /r/\n"))
			})
			mux.HandleFunc("/api/v4/jobs", func(w http.ResponseWriter, r *http.Request) {
				terms := append([]string(nil), r.URL.Query()["term[]"]...)
				sort.Strings(terms)
				ids := map[string][]int{
					"cloud,platform":         {1000, 1001},
					"Go,backend":             {1001, 1002},
					"Kubernetes,reliability": {1002, 1003, 1004},
				}[strings.Join(terms, ",")]
				fixtures := map[int]map[string]any{
					1000: {"id": 1000, "name": "Verification intern platform engineer", "path": "/jobs/1000", "salary": "NT$ 60,000 - 70,000", "location": "Taipei", "company": map[string]string{"brand": "Example Learning"}},
					1001: {"id": 1001, "name": "Verification failure remote platform engineer", "path": "/jobs/1001", "salary": "NT$ 100,000 - 120,000", "location": "Taipei", "company": map[string]string{"brand": "Example Platform"}},
					1002: {"id": 1002, "name": "Verification ready hybrid backend engineer", "path": "/jobs/1002", "salary": "NT$ 110,000 - 130,000", "location": "Taipei", "company": map[string]string{"brand": "Example Services"}},
					1003: {"id": 1003, "name": "Verification low score cloud engineer", "path": "/jobs/1003", "salary": "NT$ 90,000 - 100,000", "location": "Taipei", "company": map[string]string{"brand": "Example Operations"}},
					// 1004 withholds its salary so screening has to answer "unknown"
					// rather than reject; it is the fixture behind the 待看 outcome.
					1004: {"id": 1004, "name": "Verification unknown salary platform engineer", "path": "/jobs/1004", "salary": "面議", "location": "Taipei", "company": map[string]string{"brand": "Example Ventures"}},
				}
				jobs := make([]map[string]any, 0, len(ids))
				for _, id := range ids {
					jobs = append(jobs, fixtures[id])
				}
				writeMockJSON(w, map[string]any{"payload": map[string]any{"hasMore": false, "jobs": jobs}})
			})
			mux.HandleFunc("/jobs/1000", mockJobPage("Verification intern platform engineer", "Training <strong>cloud</strong> platform work"))
			mux.HandleFunc("/jobs/1001", mockJobPage("Verification failure remote platform engineer", "Build Go &amp; cloud platform services"))
			mux.HandleFunc("/jobs/1002", mockJobPage("Verification ready hybrid backend engineer", "Build <strong>Go</strong> backend services"))
			mux.HandleFunc("/jobs/1003", mockJobPage("Verification low score cloud engineer", "Maintain cloud operations services"))
			// 1004 also carries a recruiter contact line, the shape of JD text that
			// puts a mail address and a phone number into an Agent prompt.
			mux.HandleFunc("/jobs/1004", mockJobPage("Verification unknown salary platform engineer", "Operate cloud platform services. Contact hr@verification.invalid or 0912345678"))
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if logFile != nil && r.URL.Path != "/healthz" {
					logMu.Lock()
					_ = json.NewEncoder(logFile).Encode(map[string]any{
						"method": r.Method, "path": r.URL.Path, "query": r.URL.Query(),
						"user_agent": r.Header.Get("User-Agent"), "referer": r.Header.Get("Referer"),
					})
					logMu.Unlock()
				}
				mux.ServeHTTP(w, r)
			})
			server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
			return server.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:18787", "loopback address for the fixture server")
	cmd.Flags().StringVar(&requestLog, "request-log", "", "safe JSONL request journal path")
	return cmd
}

func newVerificationSnapshotCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:    "snapshot",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = data.Close() }()
			snapshot, err := data.SnapshotForVerification(cmd.Context())
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(snapshot)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path")
	_ = cmd.MarkFlagRequired("db")
	return cmd
}

func mockJobPage(title, description string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, "<html><body><h1>%s</h1><section class=\"job-description\">%s</section></body></html>", title, description)
	}
}

func writeMockJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
