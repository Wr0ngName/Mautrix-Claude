package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"

	"go.mau.fi/mautrix-claude/pkg/claudeapi"
)

// GetUserInfo returns information about a ghost user (Claude model).
func (c *ClaudeClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	meta, ok := ghost.Metadata.(*GhostMetadata)
	if !ok || meta == nil {
		return nil, fmt.Errorf("invalid ghost metadata")
	}

	modelName := meta.Model
	if modelName == "" {
		modelName = c.Connector.Config.GetDefaultModel()
	}
	displayName := fmt.Sprintf("Claude (%s)", modelName)

	// Get model info for better display name
	if info := claudeapi.GetModelInfo(modelName); info != nil {
		displayName = info.Name
	}

	isBot := true

	return &bridgev2.UserInfo{
		Name:        &displayName,
		IsBot:       &isBot,
		Identifiers: []string{fmt.Sprintf("claude:%s", modelName)},
	}, nil
}
