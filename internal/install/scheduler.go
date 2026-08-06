package install

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
)

// scheduler is the only part of an install that differs by platform: what keeps
// the API resident and what triggers the daily fetch. Both implementations
// compile on both platforms — they are process invocations, not platform APIs —
// so a mistake in the Windows path is a compile or vet failure everywhere
// rather than something only a release build discovers.
type scheduler interface {
	// name identifies the mechanism in the install report.
	name() string
	// preflight fails when the mechanism cannot be driven at all, which is an
	// environment problem rather than a defect in what is being installed.
	preflight(ctx context.Context) error
	// mount writes the definitions from the artifact's templates, stashing the
	// outgoing ones for rollback, and registers them.
	mount(ctx context.Context, layout paths.Layout, assetDir string, out io.Writer) error
	// restoreDefinitions puts the stashed definitions back.
	restoreDefinitions(ctx context.Context, layout paths.Layout, out io.Writer) error
	// start brings the API up and arms the fetch schedule on a fresh install.
	start(ctx context.Context, layout paths.Layout, out io.Writer) error
	// restart replaces a running API process; an already-running service is not
	// restarted by "enable", which is exactly how a stale process survives an
	// update unnoticed.
	restart(ctx context.Context, layout paths.Layout, out io.Writer) error
	// assertEffective proves on the running process — not on the installed
	// files — that the API is the binary just placed and that it started after
	// the given marker, and that the scheduled fetch is armed.
	assertEffective(ctx context.Context, layout paths.Layout, marker time.Time, out io.Writer) error
	// hints are the platform's day-to-day commands, printed after an install.
	hints(layout paths.Layout) []string
}

func newScheduler(goos string) scheduler {
	if goos == "windows" {
		return taskScheduler{}
	}
	return systemdScheduler{}
}

// verifyEffect runs the platform's own proof and then the platform-independent
// one: the API answers on loopback, with the token and only with the token.
func verifyEffect(ctx context.Context, sched scheduler, layout paths.Layout, marker time.Time, opts Options) error {
	if opts.SkipVerify {
		report(opts.Out, "--skip-verify: the running service was not checked")
		return nil
	}
	if err := sched.assertEffective(ctx, layout, marker, opts.Out); err != nil {
		return err
	}
	return smokeAPI(ctx, layout, opts.Out)
}

// run executes a command and returns its trimmed stdout, folding stderr into
// the error so a failure explains itself.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 G702 -- fixed scheduling commands with layout-derived arguments.
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String() + " " + stdout.String())
		if detail == "" {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return "", fmt.Errorf("%s: %w: %s", name, err, detail)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// waitFor polls a condition for up to the given budget. Service managers return
// from a start request before the process is listening, so the checks that
// follow need a settling window rather than a fixed sleep.
func waitFor(ctx context.Context, budget time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(budget)
	for {
		if condition() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
}
