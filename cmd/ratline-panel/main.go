// Command ratline-panel is a web interface for ratline.
//
// It is a separate binary and a separate service on purpose. ratline is a tool an
// operator runs; the panel is a daemon that listens on a port, and a provisioning
// tool that is also a network service is a larger thing to trust than one that is
// not. A server that never wants a web interface never installs this, and the
// ratline binary is unchanged by its existence.
//
// What the panel does not have is a second implementation of anything. Every action
// it offers runs the ratline binary with --json and reads the envelope, so a
// mutation made in a browser is staged, verified, committed and rolled back by
// exactly the code that would have run had somebody typed it over SSH.
package main

import (
	"os"

	panelcli "github.com/ALIRAZA47/ratline-cli/internal/panel/cli"
)

func main() { os.Exit(panelcli.Main(os.Args[1:])) }
