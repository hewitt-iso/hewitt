// Package images holds the parts of the workspace image that Hewitt also
// uses at run time.
package images

import _ "embed"

// Agent is hewitt-agent. Hewitt mounts it into every container it starts
// agents in, so the script always matches the binary, whatever the image's
// own copy is.
//
//go:embed hewitt-agent
var Agent []byte
