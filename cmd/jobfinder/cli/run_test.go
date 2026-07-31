package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/profile"
)

func TestRoutedAgentsUsesEachRoleConfiguration(t *testing.T) {
	screener, scorer, drafter, reviewer, err := routedAgents(roleRoutes{
		Filter: roleRoute{
			Primary:  roleEndpoint{Agent: "claude", Model: "filter-model"},
			Fallback: roleEndpoint{Agent: "codex", Model: "gpt-5.6-terra"},
		},
		Scorer: roleRoute{
			Primary:  roleEndpoint{Agent: "codex", Model: "gpt-5.6-terra"},
			Fallback: roleEndpoint{Agent: "claude", Model: "claude-sonnet-5"},
		},
		Drafter: roleRoute{
			Primary:  roleEndpoint{Agent: "claude", Model: "claude-sonnet-5"},
			Fallback: roleEndpoint{Agent: "codex", Model: "gpt-5.6-terra"},
		},
		Reviewer: roleRoute{
			Primary:  roleEndpoint{Agent: "codex", Model: "review-model"},
			Fallback: roleEndpoint{Agent: "claude", Model: "review-fallback-model"},
		},
	}, time.Minute)
	if err != nil || screener.Primary.Name() != "claude" || scorer.Primary.Name() != "codex" || drafter.Primary.Name() != "claude" || reviewer.Primary.Name() != "codex" {
		t.Fatalf("unexpected routes: %v", err)
	}
	// Screening routes on its own: it is condition-by-condition fact checking and
	// may sit on a cheaper model than scoring.
	if !containsPair(screener.Primary.(agents.CommandRunner).Args, "--model", "filter-model") {
		t.Fatalf("filter does not use its role-specific model")
	}
	command := scorer.Primary.(agents.CommandRunner)
	if !containsPair(command.Args, "--model", "gpt-5.6-terra") {
		t.Fatalf("codex args do not contain configured model: %v", command.Args)
	}
	reviewerCommand := reviewer.Primary.(agents.CommandRunner)
	if !containsPair(reviewerCommand.Args, "--model", "review-model") {
		t.Fatalf("reviewer does not use its role-specific model: %v", reviewerCommand.Args)
	}
	if _, _, _, _, err := routedAgents(roleRoutes{Scorer: roleRoute{Primary: roleEndpoint{Agent: "invalid", Model: "model"}}}, time.Minute); err == nil {
		t.Fatal("accepted incomplete or invalid routes")
	}
	if _, _, _, _, err := routedAgents(roleRoutes{Filter: roleRoute{Primary: roleEndpoint{Agent: "claude"}}}, time.Minute); err == nil || !strings.Contains(err.Error(), "endpoint model is required") {
		t.Fatalf("empty role model error = %v", err)
	}
}

func TestParseFileConfigRejectsUnknownFields(t *testing.T) {
	if _, err := parseFileConfig([]byte("unknown: true\n")); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("unknown config field error = %v", err)
	}
	legacy := "llm:\n  runners:\n    claude:\n      model: claude-sonnet-5\n"
	if _, err := parseFileConfig([]byte(legacy)); err == nil || !strings.Contains(err.Error(), "field runners not found") {
		t.Fatalf("legacy global runner config error = %v", err)
	}
}

// The pause switch is the only way to collect without judging, so a config that
// asks for it must not be silently read as the default running worker.
func TestParseFileConfigReadsThePausedWorker(t *testing.T) {
	cfg, err := parseFileConfig([]byte("worker:\n  paused: true\n"))
	if err != nil || !cfg.Worker.Paused {
		t.Fatalf("paused worker config = %v, err = %v", cfg.Worker.Paused, err)
	}
	cfg, err = parseFileConfig([]byte("worker:\n  scan_interval: 5s\n"))
	if err != nil || cfg.Worker.Paused {
		t.Fatalf("default worker config = %v, err = %v", cfg.Worker.Paused, err)
	}
}

func TestDirectionQueriesKeepsAtMostThreeDirectionGroups(t *testing.T) {
	p := profile.Profile{Search: profile.Search{Directions: []profile.Direction{
		{Key: "P1", Keywords: []string{"cloud", "platform"}},
		{Key: "P2", Keywords: []string{"backend", "Go"}},
		{Key: "P3", Keywords: []string{"Kubernetes", "reliability"}},
		{Key: "P4", Keywords: []string{"ignored"}},
	}}}
	queries := directionQueries(p)
	if len(queries) != 3 || queries[0].Direction != "P1" || strings.Join(queries[1].Keywords, ",") != "backend,Go" || queries[2].Direction != "P3" {
		t.Fatalf("unexpected queries: %+v", queries)
	}
}

func containsPair(values []string, key, value string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == key && values[index+1] == value {
			return true
		}
	}
	return false
}
