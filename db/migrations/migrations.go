// Package migrations embeds relational schemas in release binaries.
package migrations

import "embed"

//go:embed postgresql/*.sql jed/global/*.sql jed/family/*.sql
var FS embed.FS
