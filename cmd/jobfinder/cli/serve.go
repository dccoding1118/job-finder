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
		server, err := api.New(api.Config{Addr: rt.cfg.API.Addr, Token: rt.cfg.API.Token, ExtensionOrigin: rt.cfg.API.ExtensionOrigin}, rt.store, trigger, rt.pipeline)
		if err != nil {
			return err
		}
		if err := lockWorker(rt.cfg.DB.Path); err != nil {
			return err
		}
		defer unlockWorker(rt.cfg.DB.Path)
		worker := &pipeline.Worker{Pipeline: rt.pipeline, ScanInterval: rt.scanInterval}
		workerCtx, stopWorker := context.WithCancel(cmd.Context())
		defer stopWorker()
		go func() { _ = worker.Run(workerCtx) }()
		go func() {
			<-cmd.Context().Done()
			_ = server.Shutdown(context.Background())
		}()
		return server.ListenAndServe()
	}}
	cmd.Flags().StringVar(&path, "config", "config.yaml", "path to config.yaml")
	return cmd
}
