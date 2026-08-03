package cli

import (
	"context"
	"os"
	"os/exec"

	"github.com/dccoding1118/job-finder/internal/api"
	"github.com/dccoding1118/job-finder/internal/pipeline"
	"github.com/dccoding1118/job-finder/internal/store"
	"github.com/spf13/cobra"
)

// newServeCmd serves the API and carries the resident worker in the same
// process, so the filter, score, and letter stages are consumed continuously
// whichever entry wrote the job.
func newServeCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "serve", Short: "Serve the localhost JSON API and consume the pipeline stages", RunE: func(cmd *cobra.Command, _ []string) error {
		rt, err := loadRuntime(path)
		if err != nil {
			return err
		}
		defer rt.close()
		trigger := &api.InMemoryTrigger{Run: func(ctx context.Context) {
			binary, binaryErr := os.Executable()
			runErr := binaryErr
			if runErr == nil {
				runErr = exec.CommandContext(ctx, binary, "run", "--config", path, "--trigger", store.RunTriggerManualExtension).Run() // #nosec G204 -- invokes this executable with a local config path.
			}
			_ = runErr
		}}
		// The API and the worker share one gate, which is what lets the user's own
		// single-job request cut ahead of the worker's batch instead of waiting it
		// out. It belongs to this process, so it is created where both are wired.
		rt.pipeline.Gate = &pipeline.Gate{}
		server, err := api.New(api.Config{Addr: rt.cfg.API.Addr, Token: rt.cfg.API.Token, ExtensionOrigin: rt.cfg.API.ExtensionOrigin, Dedupe: rt.pipeline.Dedupe, ResidentWorker: !rt.workerPaused}, rt.store, trigger, rt.pipeline, api.ProfileConfig{Provider: rt.provider, Activate: rt.pipeline.ActivateProfile})
		if err != nil {
			return err
		}
		// A paused worker still serves the API and still collects: jobs accumulate
		// at `new` and no stage consumes them, which is what a run needs while the
		// Profile is not yet settled. The worker lock is left unheld so the stages
		// can then be driven in batches by hand with `run --stage` — which is also
		// why the API refuses single-job processing in this mode: this process
		// cannot see what a hand-driven batch is spending.
		//
		// The Side Panel's automatic-processing switch is the other axis: the worker
		// stays resident and holds the lock, and only stops picking screening and
		// scoring work up by itself, so the user's own single-job requests still run
		// here, serialized against nothing else.
		if !rt.workerPaused {
			if err := lockWorker(rt.cfg.DB.Path); err != nil {
				return err
			}
			defer unlockWorker(rt.cfg.DB.Path)
			// The switch is carried by the worker's own copy of the pipeline, not by
			// the shared value: `run --stage` is a batch the user is driving by hand
			// and must do what they asked whatever the automatic brake says.
			resident := rt.pipeline
			resident.AutoProcessing = rt.store.AutoProcessing
			worker := &pipeline.Worker{Pipeline: resident, ScanInterval: rt.scanInterval}
			workerCtx, stopWorker := context.WithCancel(cmd.Context())
			defer stopWorker()
			go func() { _ = worker.Run(workerCtx) }()
		}
		go func() {
			<-cmd.Context().Done()
			_ = server.Shutdown(context.Background())
		}()
		return server.ListenAndServe()
	}}
	cmd.Flags().StringVar(&path, "config", "config.yaml", "path to config.yaml")
	return cmd
}
