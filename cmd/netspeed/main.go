package main

import (
	"os"
	"runtime/debug"

	"github.com/0xshubhs/internetspeedtestcli/internal/app"
)

var version = "dev"

func main() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	os.Exit(app.Main(os.Args[1:], version))
}
