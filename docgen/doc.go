// Package docgen runs the youtrack-docgen command line with additional
// collectors. A private overlay builds its own binary: it creates an App,
// registers its collectors under the names used in docgen.yaml and runs it.
//
//	func main() {
//		app := docgen.New()
//		if err := app.Register("inventory", inventory.New); err != nil {
//			log.Fatal(err)
//		}
//		app.Main()
//	}
//
// Registered collectors go through the same filter, redaction, linking and
// rendering as the built-in ones.
//
// This package is part of the public API of youtrack-docgen. The API is v0.x: breaking changes are listed in CHANGELOG.md.
package docgen
