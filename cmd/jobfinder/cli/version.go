package cli

import (
	"fmt"
	goruntime "runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is injected at build time via -ldflags "-X .../cli.version=<tag>".
// Builds without the flag report the module version recorded by the toolchain,
// falling back to "dev" for plain `go build` from a checkout.
var version = ""

// Version reports the running binary's version string.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the jobfinder version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "jobfinder %s %s/%s\n", Version(), goruntime.GOOS, goruntime.GOARCH)
			return err
		},
	}
}
