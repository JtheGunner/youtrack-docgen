// Command youtrack-docgen generates infrastructure documentation and
// publishes it to the YouTrack Knowledge Base.
package main

import (
	"os"

	"github.com/JtheGunner/youtrackt-docgen/internal/cli"
)

func main() {
	if err := cli.Execute(os.Stdout, os.Stderr); err != nil {
		os.Exit(1)
	}
}
