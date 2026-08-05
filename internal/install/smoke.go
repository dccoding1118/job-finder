package install

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
	"gopkg.in/yaml.v3"
)

// apiConfig is the slice of config.yaml the installer needs. It is parsed
// permissively: the installer must not fail on configuration keys it does not
// know about, which are the concern of the runtime, not of the install.
type apiConfig struct {
	API struct {
		Addr  string `yaml:"addr"`
		Token string `yaml:"token"`
	} `yaml:"api"`
}

func readAPIConfig(path string) (apiConfig, error) {
	contents, err := os.ReadFile(path) // #nosec G304 -- path comes from the resolved layout.
	if err != nil {
		return apiConfig{}, fmt.Errorf("install: read config: %w", err)
	}
	var cfg apiConfig
	if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return apiConfig{}, fmt.Errorf("install: parse config: %w", err)
	}
	if cfg.API.Addr == "" || cfg.API.Token == "" {
		return apiConfig{}, fmt.Errorf("install: config is missing api.addr or api.token")
	}
	return cfg, nil
}

// smokeAPI is the platform-independent half of the effect-surface proof: the
// API is listening on loopback, answers the authenticated Job API, and rejects
// the unauthenticated one. A service that is "active" but answers 401 to its
// own token is not a working install.
func smokeAPI(ctx context.Context, layout paths.Layout, out io.Writer) error {
	cfg, err := readAPIConfig(layout.Config)
	if err != nil {
		return err
	}
	if loopbackErr := assertLoopback(cfg.API.Addr); loopbackErr != nil {
		return loopbackErr
	}
	client := &http.Client{Timeout: 10 * time.Second}
	url := "http://" + cfg.API.Addr + "/api/v1/jobs"

	code, err := statusOf(ctx, client, url, cfg.API.Token)
	if err != nil {
		return fmt.Errorf("install: authenticated GET %s: %w", url, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("install: authenticated GET /api/v1/jobs returned %d, want 200", code)
	}
	code, err = statusOf(ctx, client, url, "")
	if err != nil {
		return fmt.Errorf("install: unauthenticated GET %s: %w", url, err)
	}
	if code != http.StatusUnauthorized {
		return fmt.Errorf("install: unauthenticated GET /api/v1/jobs returned %d, want 401", code)
	}
	report(out, "API smoke passed on %s (authenticated 200, unauthenticated 401)", cfg.API.Addr)
	return nil
}

func statusOf(ctx context.Context, client *http.Client, url, token string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, nil
}

// assertLoopback refuses an install whose API would be reachable from the
// network. The server binds exactly api.addr, so the address is the binding:
// there is no configuration under which a non-loopback addr yields a
// loopback-only listener.
func assertLoopback(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("install: api.addr %q is not host:port: %w", addr, err)
	}
	if port == "" {
		return fmt.Errorf("install: api.addr %q has no port", addr)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("install: api.addr %q is not a loopback address; the API must never be bound to a routable interface", addr)
	}
	return nil
}

// apiResponding reports whether anything accepts a connection on the configured
// address, which is the settling condition after a start or restart request.
func apiResponding(layout paths.Layout) bool {
	cfg, err := readAPIConfig(layout.Config)
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("tcp", cfg.API.Addr, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
