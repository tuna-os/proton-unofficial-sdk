// Command proton-helper is the stdin/stdout front end of protonsdk: one
// UTF-8 JSON request on stdin, one versioned reply line on stdout, exit 0
// always. Installed or linked as hylki-proton it is the Proton helper the
// Hylki mail client runs, and behaves identically under either name.
package main

import (
	"os"

	"github.com/tuna-os/proton-unofficial-sdk/protonsdk"
)

func main() {
	protonsdk.Serve(os.Stdin, os.Stdout)
}
