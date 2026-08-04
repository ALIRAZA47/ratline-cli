// Command ratline provisions and manages isolated users, sites and
// certificates on a single server.
package main

import (
	"os"

	"github.com/ALIRAZA47/ratline-cli/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
