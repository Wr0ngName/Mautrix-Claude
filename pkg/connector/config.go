package connector

// Config contains the configuration for the candy.ai connector.
type Config struct {
	// BaseURL is the base URL for candy.ai (default: https://candy.ai)
	BaseURL string `yaml:"base_url"`

	// UserAgent is the user agent to use for requests
	UserAgent string `yaml:"user_agent"`

	// SyncOnConnect whether to sync conversations on connect
	SyncOnConnect bool `yaml:"sync_on_connect"`

	// BackfillEnabled whether to backfill message history
	BackfillEnabled bool `yaml:"backfill_enabled"`

	// BackfillMaxMessages maximum messages to backfill per conversation
	BackfillMaxMessages int `yaml:"backfill_max_messages"`
}

// ExampleConfig is the example configuration for the connector.
const ExampleConfig = `
    # Candy.ai connector configuration

    # Base URL for candy.ai (default: https://candy.ai)
    base_url: https://candy.ai

    # Custom user agent (optional, uses default Firefox if not set)
    user_agent: ""

    # Sync conversations on connect
    sync_on_connect: true

    # Backfill message history
    backfill_enabled: true
    backfill_max_messages: 50
`

// GetBaseURL returns the base URL, using default if not set.
func (c *Config) GetBaseURL() string {
	if c.BaseURL == "" {
		return "https://candy.ai"
	}
	return c.BaseURL
}

// GetBackfillMaxMessages returns the max messages to backfill.
func (c *Config) GetBackfillMaxMessages() int {
	if c.BackfillMaxMessages == 0 {
		return 50
	}
	return c.BackfillMaxMessages
}
