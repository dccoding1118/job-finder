package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dccoding1118/job-finder/cmd/jobfinder/cli"
	"github.com/dccoding1118/job-finder/internal/logging"
)

func main() {
	location, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		panic(err)
	}
	time.Local = location

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Execute(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		// stderr alone is not a record on every platform this ships to; see
		// logging.ReportFatal.
		logging.ReportFatal(err)
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
}
