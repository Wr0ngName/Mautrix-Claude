package connector

import (
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/id"
)

// GhostQueryHandler implements appservice.QueryHandler to tell the homeserver
// which ghost users exist. Without this, the homeserver won't allow inviting
// ghost users to rooms because it thinks they don't exist.
type GhostQueryHandler struct {
	Matrix *matrix.Connector
}

// QueryAlias handles alias existence queries. We don't use room aliases.
func (q *GhostQueryHandler) QueryAlias(alias id.RoomAlias) bool {
	return false
}

// QueryUser handles user existence queries from the homeserver.
// Returns true if the user ID belongs to a valid Claude ghost user.
func (q *GhostQueryHandler) QueryUser(userID id.UserID) bool {
	// Check if this user ID is in our ghost namespace
	ghostID, isGhost := q.Matrix.ParseGhostMXID(userID)
	if !isGhost {
		return false
	}

	// Valid ghost IDs are model family names: sonnet, opus, haiku, or "error"
	switch string(ghostID) {
	case "sonnet", "opus", "haiku", "error":
		return true
	}

	return false
}
