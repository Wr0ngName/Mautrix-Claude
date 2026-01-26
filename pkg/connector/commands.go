package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/mautrix-claude/pkg/claudeapi"
	"maunium.net/go/mautrix/bridgev2/commands"
)

// RegisterCommands registers custom commands for the Claude AI bridge.
func (c *ClaudeConnector) RegisterCommands(proc *commands.Processor) {
	proc.AddHandlers(
		&commands.FullHandler{
			Func:    c.cmdJoin,
			Name:    "join",
			Aliases: []string{"add", "invite"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Add Claude to the current room (creates a bridge portal)",
				Args:        "[model]",
			},
			RequiresLogin:  true,
			RequiresPortal: false, // Can be used in any room
		},
		&commands.FullHandler{
			Func:    c.cmdModel,
			Name:    "model",
			Aliases: []string{"set-model", "switch-model"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "View or change the Claude model for this conversation",
				Args:        "[model-name]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdModels,
			Name:    "models",
			Aliases: []string{"list-models"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "List available Claude models",
			},
			RequiresLogin: true,
		},
		&commands.FullHandler{
			Func:    c.cmdClear,
			Name:    "clear",
			Aliases: []string{"reset", "clear-context"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Clear the conversation history/context for this room",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdStats,
			Name:    "stats",
			Aliases: []string{"info", "status"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Show conversation statistics for this room",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdSystem,
			Name:    "system",
			Aliases: []string{"set-system", "system-prompt"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "View or set the system prompt for this conversation",
				Args:        "[prompt]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdTemperature,
			Name:    "temperature",
			Aliases: []string{"temp", "set-temp"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "View or set the temperature (0-1) for this conversation",
				Args:        "[value]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdMention,
			Name:    "mention",
			Aliases: []string{"mentions", "mention-only"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Toggle mention-only mode (Claude only responds when @mentioned)",
				Args:        "[on|off]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
	)
}

// getAPIKeyFromLogin extracts the API key from a user login.
func (c *ClaudeConnector) getAPIKeyFromLogin(ce *commands.Event) string {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		return ""
	}
	meta, ok := login.Metadata.(*UserLoginMetadata)
	if !ok || meta == nil {
		return ""
	}
	return meta.APIKey
}

// cmdModel views or changes the Claude model for a conversation.
func (c *ClaudeConnector) cmdModel(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a Claude conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current model
	if len(ce.Args) == 0 {
		currentModel := meta.Model
		if currentModel == "" {
			currentModel = c.Config.GetDefaultModel()
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**Current model:** `%s`\n\n", currentModel))

		// Try to get display name from cache
		displayName := claudeapi.GetModelDisplayName(currentModel)
		if displayName != currentModel {
			sb.WriteString(fmt.Sprintf("**Name:** %s\n", displayName))
		}

		inputTokens, outputTokens := claudeapi.EstimateMaxTokens(currentModel)
		sb.WriteString(fmt.Sprintf("**Max input tokens:** %d\n", inputTokens))
		sb.WriteString(fmt.Sprintf("**Max output tokens:** %d\n", outputTokens))

		sb.WriteString("\nUse `model <name>` to change. Run `models` to see available options.")
		ce.Reply(sb.String())
		return
	}

	// Get API key to validate model
	apiKey := c.getAPIKeyFromLogin(ce)
	if apiKey == "" {
		ce.Reply("Failed to get API credentials.")
		return
	}

	// Set new model - resolve alias if needed
	newModel := strings.Join(ce.Args, "-")

	ctx, cancel := context.WithTimeout(ce.Ctx, 15*time.Second)
	defer cancel()

	// Map friendly shortcuts to latest model of that family (dynamically from API)
	switch strings.ToLower(newModel) {
	case "opus", "claude-opus":
		resolved, err := claudeapi.GetLatestModelByFamilyFromAPI(ctx, apiKey, "opus")
		if err != nil {
			ce.Reply("Failed to resolve opus model: %v", err)
			return
		}
		newModel = resolved
	case "sonnet", "claude-sonnet":
		resolved, err := claudeapi.GetLatestModelByFamilyFromAPI(ctx, apiKey, "sonnet")
		if err != nil {
			ce.Reply("Failed to resolve sonnet model: %v", err)
			return
		}
		newModel = resolved
	case "haiku", "claude-haiku":
		resolved, err := claudeapi.GetLatestModelByFamilyFromAPI(ctx, apiKey, "haiku")
		if err != nil {
			ce.Reply("Failed to resolve haiku model: %v", err)
			return
		}
		newModel = resolved
	default:
		// Validate model ID format first (prevents abuse with overly long strings)
		if err := ValidateModelID(newModel); err != nil {
			ce.Reply("Invalid model ID: %v", err)
			return
		}

		// Validate the model exists via API
		if err := claudeapi.ValidateModel(ctx, apiKey, newModel); err != nil {
			ce.Reply("Invalid model: `%s`\n\nError: %v\n\nRun `models` to see available options.", newModel, err)
			return
		}
	}

	// Update portal metadata - save old value for rollback on failure
	oldModel := meta.Model
	meta.Model = newModel
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.Model = oldModel // Rollback in-memory state on save failure
		ce.Reply("Failed to save model change: %v", err)
		return
	}

	displayName := claudeapi.GetModelDisplayName(newModel)
	if displayName != newModel {
		ce.Reply("Model changed to **%s** (`%s`)", displayName, newModel)
	} else {
		ce.Reply("Model changed to `%s`", newModel)
	}
}

// cmdModels lists available Claude models by querying the API.
func (c *ClaudeConnector) cmdModels(ce *commands.Event) {
	// Get API key
	apiKey := c.getAPIKeyFromLogin(ce)
	if apiKey == "" {
		ce.Reply("Failed to get API credentials.")
		return
	}

	// Fetch models from API
	ctx, cancel := context.WithTimeout(ce.Ctx, 15*time.Second)
	defer cancel()

	models, err := claudeapi.FetchModels(ctx, apiKey)
	if err != nil {
		ce.Reply("Failed to fetch models from API: %v", err)
		return
	}

	if len(models) == 0 {
		ce.Reply("No models available.")
		return
	}

	var sb strings.Builder
	sb.WriteString("**Available Claude Models:**\n\n")

	defaultModel := c.Config.GetDefaultModel()

	// Group by family
	families := map[string][]claudeapi.ModelInfo{
		"opus":   {},
		"sonnet": {},
		"haiku":  {},
		"other":  {},
	}

	for _, model := range models {
		family := model.Family
		if family == "unknown" {
			family = "other"
		}
		families[family] = append(families[family], model)
	}

	// Display in order: opus, sonnet, haiku, other
	for _, family := range []string{"opus", "sonnet", "haiku", "other"} {
		familyModels := families[family]
		if len(familyModels) == 0 {
			continue
		}

		// Capitalize first letter (strings.Title is deprecated in Go 1.18+)
		capitalizedFamily := strings.ToUpper(family[:1]) + family[1:]
		sb.WriteString(fmt.Sprintf("**%s:**\n", capitalizedFamily))
		for _, model := range familyModels {
			isDefault := ""
			if model.ID == defaultModel {
				isDefault = " *(default)*"
			}

			sb.WriteString(fmt.Sprintf("• **%s**%s\n", model.DisplayName, isDefault))
			sb.WriteString(fmt.Sprintf("  `%s`\n", model.ID))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Use `model <model-id>` to switch models.\n")
	sb.WriteString("Shortcuts: `opus`, `sonnet`, `haiku`")

	ce.Reply(sb.String())
}

// cmdClear clears the conversation history.
func (c *ClaudeConnector) cmdClear(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a Claude conversation room.")
		return
	}

	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You are not logged in.")
		return
	}

	client, ok := login.Client.(*ClaudeClient)
	if !ok || client == nil {
		ce.Reply("Failed to get client.")
		return
	}

	// Get stats before clearing
	msgCount, tokens, _ := client.GetConversationStats(ce.Portal.PortalKey.ID)

	// Clear the conversation
	client.ClearConversation(ce.Portal.PortalKey.ID)

	ce.Reply("Conversation cleared. Removed %d messages (~%d tokens).", msgCount, tokens)
}

// cmdStats shows conversation statistics.
func (c *ClaudeConnector) cmdStats(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a Claude conversation room.")
		return
	}

	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You are not logged in.")
		return
	}

	client, ok := login.Client.(*ClaudeClient)
	if !ok || client == nil {
		ce.Reply("Failed to get client.")
		return
	}

	meta, _ := ce.Portal.Metadata.(*PortalMetadata)

	// Get conversation stats
	msgCount, estimatedTokens, lastUsed := client.GetConversationStats(ce.Portal.PortalKey.ID)

	var sb strings.Builder
	sb.WriteString("**Conversation Statistics:**\n\n")

	// Model info
	model := c.Config.GetDefaultModel()
	if meta != nil && meta.Model != "" {
		model = meta.Model
	}

	displayName := claudeapi.GetModelDisplayName(model)
	if displayName != model {
		sb.WriteString(fmt.Sprintf("**Model:** %s (`%s`)\n", displayName, model))
	} else {
		sb.WriteString(fmt.Sprintf("**Model:** `%s`\n", model))
	}

	// Conversation stats
	sb.WriteString(fmt.Sprintf("**Messages in context:** %d\n", msgCount))
	sb.WriteString(fmt.Sprintf("**Estimated tokens:** ~%d\n", estimatedTokens))

	if !lastUsed.IsZero() {
		sb.WriteString(fmt.Sprintf("**Last active:** %s ago\n", time.Since(lastUsed).Round(time.Second)))
	}

	// System prompt info
	if meta != nil && meta.SystemPrompt != "" {
		promptPreview := meta.SystemPrompt
		if len(promptPreview) > 100 {
			promptPreview = promptPreview[:97] + "..."
		}
		sb.WriteString(fmt.Sprintf("**Custom system prompt:** %s\n", promptPreview))
	}

	// Temperature info
	if meta != nil && meta.Temperature != nil {
		sb.WriteString(fmt.Sprintf("**Temperature:** %.2f\n", *meta.Temperature))
	} else {
		sb.WriteString(fmt.Sprintf("**Temperature:** %.2f (default)\n", c.Config.GetTemperature()))
	}

	// API metrics
	if metrics := client.GetMetrics(); metrics != nil {
		totalReqs := metrics.TotalRequests.Load()
		failedReqs := metrics.FailedRequests.Load()
		inputTokens := metrics.TotalInputTokens.Load()
		outputTokens := metrics.TotalOutputTokens.Load()

		sb.WriteString(fmt.Sprintf("\n**API Stats (this session):**\n"))
		sb.WriteString(fmt.Sprintf("• Requests: %d (%d failed)\n", totalReqs, failedReqs))
		sb.WriteString(fmt.Sprintf("• Total tokens: %d (in: %d, out: %d)\n",
			inputTokens+outputTokens, inputTokens, outputTokens))
		if avgDuration := metrics.GetAverageRequestDuration(); avgDuration > 0 {
			sb.WriteString(fmt.Sprintf("• Avg response time: %s\n", avgDuration.Round(time.Millisecond)))
		}
	}

	ce.Reply(sb.String())
}

// cmdSystem views or sets the system prompt.
func (c *ClaudeConnector) cmdSystem(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a Claude conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current system prompt
	if len(ce.Args) == 0 {
		currentPrompt := meta.SystemPrompt
		if currentPrompt == "" {
			currentPrompt = c.Config.GetSystemPrompt()
			if currentPrompt == "" {
				ce.Reply("No system prompt is set. Use `system <prompt>` to set one.")
			} else {
				ce.Reply("**Current system prompt (default):**\n\n%s", currentPrompt)
			}
		} else {
			ce.Reply("**Current system prompt:**\n\n%s\n\nUse `system clear` to reset to default.", currentPrompt)
		}
		return
	}

	// Check for clear command
	if strings.ToLower(ce.Args[0]) == "clear" {
		oldPrompt := meta.SystemPrompt
		meta.SystemPrompt = ""
		if err := ce.Portal.Save(ce.Ctx); err != nil {
			meta.SystemPrompt = oldPrompt // Rollback in-memory state on save failure
			ce.Reply("Failed to clear system prompt: %v", err)
			return
		}
		ce.Reply("System prompt cleared. Using default.")
		return
	}

	// Set new system prompt - save old value for rollback on failure
	newPrompt := strings.Join(ce.Args, " ")
	oldPrompt := meta.SystemPrompt
	meta.SystemPrompt = newPrompt
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.SystemPrompt = oldPrompt // Rollback in-memory state on save failure
		ce.Reply("Failed to save system prompt: %v", err)
		return
	}

	ce.Reply("System prompt updated.")
}

// cmdMention toggles mention-only mode.
func (c *ClaudeConnector) cmdMention(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a Claude conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current status
	if len(ce.Args) == 0 {
		if meta.MentionOnly {
			ce.Reply("**Mention-only mode:** ON\n\nClaude only responds when @mentioned.\n\nUse `mention off` to respond to all messages.")
		} else {
			ce.Reply("**Mention-only mode:** OFF\n\nClaude responds to all messages.\n\nUse `mention on` to only respond when @mentioned.")
		}
		return
	}

	// Parse argument
	arg := strings.ToLower(ce.Args[0])
	var newValue bool
	switch arg {
	case "on", "true", "yes", "1", "enable", "enabled":
		newValue = true
	case "off", "false", "no", "0", "disable", "disabled":
		newValue = false
	case "toggle":
		newValue = !meta.MentionOnly
	default:
		ce.Reply("Invalid argument. Use `mention on`, `mention off`, or `mention toggle`.")
		return
	}

	oldValue := meta.MentionOnly
	meta.MentionOnly = newValue
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.MentionOnly = oldValue
		ce.Reply("Failed to save setting: %v", err)
		return
	}

	if newValue {
		ce.Reply("Mention-only mode **enabled**. Claude will only respond when @mentioned.")
	} else {
		ce.Reply("Mention-only mode **disabled**. Claude will respond to all messages.")
	}
}

// cmdJoin adds Claude to the current room by creating a bridge portal.
// If Claude is already in the room, this re-configures the relay.
func (c *ClaudeConnector) cmdJoin(ce *commands.Event) {
	// If already a portal, just re-configure relay
	if ce.Portal != nil {
		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("You are not logged in.")
			return
		}

		if !c.br.Config.Relay.Enabled {
			ce.Reply("Claude is already in this room. Relay mode is disabled in bridge config.\n\nUse `model` to change the model or `stats` to see conversation info.")
			return
		}

		// Re-set relay to this user
		if err := ce.Portal.SetRelay(ce.Ctx, login); err != nil {
			ce.Reply("Failed to set relay: %v", err)
			return
		}

		ce.Reply("✓ Relay updated! Messages from all users in this room will now be relayed through your account.\n\nUse `model` to change models, `mention on` for mention-only mode, or `unset-relay` to disable relay.")
		return
	}

	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You are not logged in.")
		return
	}

	client, ok := login.Client.(*ClaudeClient)
	if !ok || client == nil {
		ce.Reply("Failed to get client.")
		return
	}

	// Determine model to use
	model := c.Config.GetDefaultModel()
	if len(ce.Args) > 0 {
		requestedModel := strings.ToLower(strings.Join(ce.Args, "-"))
		switch requestedModel {
		case "opus", "claude-opus":
			model = "opus"
		case "sonnet", "claude-sonnet":
			model = "sonnet"
		case "haiku", "claude-haiku":
			model = "haiku"
		default:
			// Assume it's a full model ID
			if strings.Contains(requestedModel, "claude") {
				model = requestedModel
			} else {
				ce.Reply("Unknown model: %s. Use `opus`, `sonnet`, `haiku`, or a full model ID.", requestedModel)
				return
			}
		}
	}

	// Get the room ID from the event
	roomID := ce.RoomID
	if roomID == "" {
		ce.Reply("Could not determine room ID.")
		return
	}

	c.Log.Info().
		Str("room_id", string(roomID)).
		Str("model", model).
		Str("user", string(ce.User.MXID)).
		Msg("Join command: adding Claude to room")

	// Create a unique conversation/portal ID based on the room
	conversationID := fmt.Sprintf("room_%s", roomID)
	portalKey := MakeClaudePortalKey(conversationID)

	// Get or create the portal
	ctx := ce.Ctx
	portal, err := c.br.GetPortalByKey(ctx, portalKey)
	if err != nil {
		ce.Reply("Failed to get portal: %v", err)
		return
	}

	// Check if this portal already has a different room associated
	if portal.MXID != "" && portal.MXID != roomID {
		ce.Reply("This portal is associated with a different room. Please use a new conversation.")
		return
	}

	// Get the ghost for this model (with proper metadata)
	ghostID := c.MakeClaudeGhostID(model)
	ghost, err := c.GetOrUpdateGhost(ctx, ghostID, model)
	if err != nil {
		ce.Reply("Failed to get Claude ghost: %v", err)
		return
	}

	// Set up portal metadata
	chatName := fmt.Sprintf("Claude (%s)", model)
	portalMeta := &PortalMetadata{
		ConversationName: chatName,
		Model:            model,
	}

	// Update the portal to use this room
	if portal.MXID == "" {
		// Link the existing Matrix room to this portal
		portal.MXID = roomID
		portal.Metadata = portalMeta

		if err := portal.Save(ctx); err != nil {
			ce.Reply("Failed to save portal: %v", err)
			return
		}
	}

	// Have the ghost join the room
	err = ghost.Intent.EnsureJoined(ctx, roomID)
	if err != nil {
		c.Log.Warn().Err(err).Msg("Failed to join room with ghost, trying invite first")

		// Try to invite and then join
		botIntent := c.br.Bot
		err = botIntent.EnsureInvited(ctx, roomID, ghost.Intent.GetMXID())
		if err != nil {
			ce.Reply("Failed to invite Claude to this room: %v\n\nMake sure the bot has permission to invite users.", err)
			return
		}

		err = ghost.Intent.EnsureJoined(ctx, roomID)
		if err != nil {
			ce.Reply("Claude was invited but failed to join: %v", err)
			return
		}
	}

	// Auto-set relay so other users in the room can also talk to Claude
	// This uses the joining user's login to relay messages from non-logged-in users
	if c.br.Config.Relay.Enabled {
		if err := portal.SetRelay(ctx, login); err != nil {
			c.Log.Warn().Err(err).Msg("Failed to set relay for portal")
			// Non-fatal - continue but warn user
		} else {
			c.Log.Debug().
				Str("relay_login", string(login.ID)).
				Msg("Auto-configured relay for portal")
		}
	}

	displayName := claudeapi.GetModelDisplayName(model)
	if c.br.Config.Relay.Enabled {
		ce.Reply("✓ **%s** has joined the room!\n\nAll users in this room can now chat with Claude (messages relayed through your account).\n\nUse `model` to change models, `system` to set a custom prompt, `mention on` for mention-only mode, or `clear` to reset conversation.", displayName)
	} else {
		ce.Reply("✓ **%s** has joined the room!\n\n⚠️ **Note:** Relay mode is disabled. Only you can talk to Claude. Enable `relay.enabled: true` in bridge config for multi-user support.\n\nUse `model` to change models, `system` to set a custom prompt, or `clear` to reset the conversation.", displayName)
	}

	c.Log.Info().
		Str("room_id", string(roomID)).
		Str("model", model).
		Str("ghost_id", string(ghostID)).
		Bool("relay_enabled", c.br.Config.Relay.Enabled).
		Msg("Successfully added Claude to room")
}

// cmdTemperature views or sets the temperature.
func (c *ClaudeConnector) cmdTemperature(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a Claude conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current temperature
	if len(ce.Args) == 0 {
		if meta.Temperature != nil {
			ce.Reply("**Current temperature:** %.2f\n\nUse `temperature <0-1>` to change, or `temperature reset` to use default.", *meta.Temperature)
		} else {
			ce.Reply("**Current temperature:** %.2f (default)\n\nUse `temperature <0-1>` to change.", c.Config.GetTemperature())
		}
		return
	}

	// Check for reset command
	if strings.ToLower(ce.Args[0]) == "reset" || strings.ToLower(ce.Args[0]) == "clear" {
		oldTemp := meta.Temperature
		meta.Temperature = nil
		if err := ce.Portal.Save(ce.Ctx); err != nil {
			meta.Temperature = oldTemp // Rollback in-memory state on save failure
			ce.Reply("Failed to reset temperature: %v", err)
			return
		}
		ce.Reply("Temperature reset to default (%.2f).", c.Config.GetTemperature())
		return
	}

	// Parse temperature value
	var temp float64
	if _, err := fmt.Sscanf(ce.Args[0], "%f", &temp); err != nil {
		ce.Reply("Invalid temperature value. Use a number between 0 and 1.")
		return
	}

	if temp < 0 || temp > 1 {
		ce.Reply("Temperature must be between 0 and 1.")
		return
	}

	oldTemp := meta.Temperature
	meta.Temperature = &temp
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.Temperature = oldTemp // Rollback in-memory state on save failure
		ce.Reply("Failed to save temperature: %v", err)
		return
	}

	ce.Reply("Temperature set to %.2f.", temp)
}
