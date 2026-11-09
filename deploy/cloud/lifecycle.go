// Package cloud carries the remote lifecycle script shipped with the desktop app.
package cloud

import _ "embed"

//go:embed lifecycle.sh
var Lifecycle string
