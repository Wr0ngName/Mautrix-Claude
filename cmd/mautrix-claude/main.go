// mautrix-claude is a Matrix-Claude API puppeting bridge.
package main

import (
	"go.mau.fi/mautrix-claude/pkg/connector"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
)

// Information to find out exactly which commit the bridge was built from.
// These are filled at build time with the -X linker flag.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

var m mxmain.BridgeMain

func main() {
	c := connector.NewConnector()
	m = mxmain.BridgeMain{
		Name:        "mautrix-claude",
		URL:         "https://github.com/mautrix/claude",
		Description: "A Matrix-Claude API bridge",
		Version:     "0.1.0",
		Connector:   c,
		PostInit:    postInit,
	}

	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}

// postInit is called after bridge initialization but before start.
// We use this to set up the custom QueryHandler for ghost user existence queries.
func postInit() {
	// Set the QueryHandler on the appservice to handle ghost user queries
	m.Matrix.AS.QueryHandler = &connector.GhostQueryHandler{
		Matrix: m.Matrix,
	}
}
