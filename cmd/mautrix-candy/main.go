// mautrix-candy is a Matrix-Candy.ai puppeting bridge.
package main

import (
	"go.mau.fi/mautrix-candy/pkg/connector"
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
		Name:        "mautrix-candy",
		URL:         "https://github.com/mautrix/candy",
		Description: "A Matrix-Candy.ai puppeting bridge",
		Version:     "0.1.0",
		Connector:   connector.NewConnector(),
	}

	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
