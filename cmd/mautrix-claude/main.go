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

func main() {
	m := mxmain.BridgeMain{
		Name:        "mautrix-claude",
		URL:         "https://github.com/mautrix/claude",
		Description: "A Matrix-Claude API bridge",
		Version:     "0.1.0",
		Connector:   connector.NewConnector(),
	}

	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
