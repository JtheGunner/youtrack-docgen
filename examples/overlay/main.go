// Command overlay is youtrack-docgen with one extra collector, "inventory".
// It only uses the public packages of youtrack-docgen.
package main

import (
	"fmt"
	"os"

	"github.com/JtheGunner/youtrack-docgen/docgen"
)

func main() {
	app := docgen.New()
	if err := app.Register(Source, newFromSettings); err != nil {
		fmt.Fprintln(os.Stderr, "register inventory collector:", err)
		os.Exit(1)
	}
	app.Main()
}
