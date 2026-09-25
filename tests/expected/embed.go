// Package expected carries the committed snapshots the runner needs at run
// time, where the checkout is not mounted.
package expected

import _ "embed"

// CursorTools is cursor-tools.json: the tools cursor's real backend gave the
// model per mode, and the exec type that runs each on the client.
//
//go:embed cursor-tools.json
var CursorTools []byte
