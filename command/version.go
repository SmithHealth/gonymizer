package main

import (
	"fmt"
	"github.com/smithoss/gonymizer"
	"github.com/spf13/cobra"
	"os"
	"runtime"
)

// VersionCmd is the cobra.Command struct we use for "VersionCmd" command.
var (
	VersionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print program version and exit",
		Run:   version,
	}
)

func version(cmd *cobra.Command, args []string) {
	fmt.Printf("%v (v%v, build %v, build date:%v)\n",
		os.Args[0],
		gonymizer.Version(),
		gonymizer.BuildNumber(),
		gonymizer.BuildDate())
	fmt.Printf("Go (runtime:%v) (GOMAXPROCS:%d) (NumCPUs:%d)\n", runtime.Version(), runtime.GOMAXPROCS(-1),
		runtime.NumCPU())
	os.Exit(0)
}
