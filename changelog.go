// Package pinky holds files from the repository's root that the app
// embeds.
package pinky

import _ "embed"

// Changelog is CHANGELOG.md: newest first, a "## <version> (<YYYY-MM-DD>)"
// section of bullets per release. What's new shows it after an update.
//
//go:embed CHANGELOG.md
var Changelog string
