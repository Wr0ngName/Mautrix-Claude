# Implementation Plan: mautrix-claude - Matrix Bridge for Claude API

## Overview

This document outlines the comprehensive plan to convert the existing `mautrix-candy` bridge into `mautrix-claude`, a Matrix bridge that connects to Anthropic's Claude API. The bridge will allow users to interact with Claude AI models directly through Matrix chat rooms.

**Current State**: The codebase is a functional Matrix bridge for Candy.ai (an AI character chat platform) with ~3000 lines of Go code, using the mautrix bridgev2 framework.

**Target State**: A Matrix bridge that:
- Connects to Claude API (official Anthropic API)
- Supports multiple Claude models (claude-3-opus, claude-3.5-sonnet, claude-3-haiku, etc.)
- Manages multiple conversations as separate Matrix rooms
- Handles conversation context properly
- Supports API key authentication
- Allows per-room model selection
- Respects API rate limits

---

## Architecture Analysis

### Current Architecture (Candy.ai Bridge)
The existing bridge has three main layers:

1. **Main Entry Point** (`cmd/mautrix-candy/main.go`)
   - Initializes the mautrix bridge
   - Registers the connector

2. **Connector Layer** (`pkg/connector/`)
   - `connector.go`: Bridge connector interface implementation
   - `config.go`: Configuration handling
   - `login.go`: Authentication flows (password, cookie)
   - `client.go`: Network API implementation (message handling, events)
   - `chatinfo.go`: Chat/portal resolution and info
   - `ghost.go`: Ghost user (AI character) handling

3. **API Client Layer** (`pkg/candygo/`)
   - `client.go`: HTTP client with session management
   - `auth.go`: Login/authentication logic
   - `websocket.go`: ActionCable WebSocket client for real-time events
   - `messages.go`: Message sending/receiving
   - `conversations.go`: Conversation management
   - `turbostream.go`: Turbo Stream protocol parsing
   - `csrf.go`: CSRF token management
   - `types.go`: Data structures

### Target Architecture (Claude API Bridge)

The new architecture will be similar but simplified:

1. **Main Entry Point** (`cmd/mautrix-claude/main.go`)
   - Initialize bridge for Claude API
   - Register connector

2. **Connector Layer** (`pkg/connector/`)
   - `connector.go`: Bridge connector for Claude
   - `config.go`: Configuration (API keys, model selection)
   - `login.go`: API key-based authentication
   - `client.go`: Claude API client wrapper
   - `chatinfo.go`: Chat/room management
   - `conversation.go`: NEW - Conversation context management

3. **Claude API Client Layer** (`pkg/claudeapi/`)
   - `client.go`: Claude API HTTP client
   - `auth.go`: API key authentication
   - `messages.go`: Messages API integration
   - `conversations.go`: Conversation/context management
   - `streaming.go`: Server-Sent Events (SSE) streaming support
   - `types.go`: Claude API data structures
   - `errors.go`: Error handling

**Key Differences from Candy.ai**:
- No WebSocket (Claude API uses REST + SSE)
- No CSRF tokens (API key auth)
- No HTML parsing (pure JSON API)
- Simpler authentication (API key vs cookies/sessions)
- Client-side conversation context management (no server-side conversation IDs)

---

## Component-by-Component Implementation Plan

### Phase 1: Foundation & API Client (Priority: HIGH)

#### 1.1 Create Claude API Client Package

**File**: `pkg/claudeapi/types.go`
- **Action**: Create new file
- **Purpose**: Define Claude API data structures
- **Key Types**:
  ```go
  type Message struct {
      Role    string     // "user" or "assistant"
      Content []Content  // Text, images, etc.
  }
  
  type Content struct {
      Type string // "text", "image"
      Text string
      Source *ImageSource
  }
  
  type CreateMessageRequest struct {
      Model       string
      Messages    []Message
      MaxTokens   int
      Temperature float64
      System      string
      Stream      bool
      Metadata    map[string]interface{}
  }
  
  type CreateMessageResponse struct {
      ID           string
      Type         string
      Role         string
      Content      []Content
      Model        string
      StopReason   string
      StopSequence string
      Usage        Usage
  }
  
  type Usage struct {
      InputTokens  int
      OutputTokens int
  }
  
  type StreamEvent struct {
      Type string // "message_start", "content_block_delta", "message_stop", etc.
      Index int
      Delta *ContentDelta
      Message *CreateMessageResponse
  }
  
  type APIError struct {
      Type    string
      Message string
  }
  ```

**File**: `pkg/claudeapi/client.go`
- **Action**: Create new file
- **Purpose**: HTTP client for Claude API
- **Key Functions**:
  ```go
  type Client struct {
      HTTPClient *http.Client
      APIKey     string
      BaseURL    string
      Version    string // API version (e.g., "2023-06-01")
      Log        zerolog.Logger
  }
  
  func NewClient(apiKey string, log zerolog.Logger) *Client
  func (c *Client) CreateMessage(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResponse, error)
  func (c *Client) CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error)
  func (c *Client) ValidateAPIKey(ctx context.Context) error
  ```
- **Implementation Details**:
  - Base URL: `https://api.anthropic.com/v1`
  - Headers: `x-api-key`, `anthropic-version`, `content-type`
  - Error handling for rate limits (429), invalid auth (401), etc.
  - Retry logic with exponential backoff

**File**: `pkg/claudeapi/streaming.go`
- **Action**: Create new file
- **Purpose**: SSE streaming support
- **Key Functions**:
  ```go
  func (c *Client) streamMessages(ctx context.Context, resp *http.Response) (<-chan StreamEvent, error)
  func parseSSELine(line string) (*StreamEvent, error)
  ```
- **Implementation Details**:
  - Parse Server-Sent Events format
  - Handle `data:`, `event:`, and `id:` lines
  - Emit structured events on channel
  - Handle context cancellation

**File**: `pkg/claudeapi/conversations.go`
- **Action**: Create new file
- **Purpose**: Manage conversation history/context
- **Key Functions**:
  ```go
  type ConversationManager struct {
      messages []Message
      maxTokens int
      mu sync.RWMutex
  }
  
  func NewConversationManager(maxTokens int) *ConversationManager
  func (cm *ConversationManager) AddMessage(role, content string)
  func (cm *ConversationManager) GetMessages() []Message
  func (cm *ConversationManager) Clear()
  func (cm *ConversationManager) TrimToTokenLimit() error
  ```
- **Implementation Details**:
  - Store message history per conversation
  - Implement token counting (approximate or via tokenizer)
  - Trim old messages when approaching context limit
  - Persist to database via metadata

**File**: `pkg/claudeapi/errors.go`
- **Action**: Create new file
- **Purpose**: Error handling and classification
- **Key Functions**:
  ```go
  func ParseAPIError(resp *http.Response) error
  func IsRateLimitError(err error) bool
  func IsAuthError(err error) bool
  func GetRetryAfter(err error) time.Duration
  ```

**File**: `pkg/claudeapi/models.go`
- **Action**: Create new file
- **Purpose**: Model definitions and selection
- **Constants**:
  ```go
  const (
      ModelOpus4_5      = "claude-opus-4.5-20251101"
      ModelSonnet4_5    = "claude-sonnet-4.5-20250924"
      ModelSonnet3_5    = "claude-3-5-sonnet-20241022"
      ModelHaiku3_5     = "claude-3-5-haiku-20241022"
      ModelOpus3        = "claude-3-opus-20240229"
  )
  
  var DefaultModel = ModelSonnet3_5
  var ValidModels = []string{...}
  ```
- **Key Functions**:
  ```go
  func ValidateModel(model string) bool
  func GetModelMaxTokens(model string) int
  ```

#### 1.2 Testing the Claude API Client

**File**: `pkg/claudeapi/client_test.go`
- **Action**: Create new file
- **Purpose**: Unit tests for API client
- **Tests**:
  - API key validation
  - Message creation (non-streaming)
  - Message creation (streaming)
  - Error handling (401, 429, 500)
  - Retry logic

---

### Phase 2: Connector Layer Modification (Priority: HIGH)

#### 2.1 Update Connector Core

**File**: `pkg/connector/connector.go`
- **Action**: Modify existing file
- **Changes**:
  1. Rename `CandyConnector` → `ClaudeConnector`
  2. Update `GetName()` to return Claude bridge info:
     ```go
     return bridgev2.BridgeName{
         DisplayName:      "Claude AI",
         NetworkURL:       "https://claude.ai",
         NetworkIcon:      "mxc://maunium.net/claude", // TODO: upload icon
         NetworkID:        "claude",
         BeeperBridgeType: "go.mau.fi/mautrix-claude",
         DefaultPort:      29320, // Different from candy
     }
     ```
  3. Update metadata types:
     ```go
     type GhostMetadata struct {
         Model string `json:"model"` // Which Claude model this "ghost" represents
     }
     
     type PortalMetadata struct {
         ConversationName string `json:"conversation_name"`
         Model           string `json:"model"` // Selected model for this room
         SystemPrompt    string `json:"system_prompt,omitempty"`
         Temperature     float64 `json:"temperature,omitempty"`
     }
     
     type MessageMetadata struct {
         ClaudeMessageID string `json:"claude_message_id"`
         TokensUsed      int    `json:"tokens_used"`
     }
     
     type UserLoginMetadata struct {
         APIKey    string `json:"api_key"`
         Email     string `json:"email,omitempty"` // For display
     }
     ```
  4. Update `GetLoginFlows()`:
     ```go
     return []bridgev2.LoginFlow{
         {
             Name:        "API Key",
             Description: "Log in with your Claude API key from console.anthropic.com",
             ID:          "api_key",
         },
     }
     ```

**File**: `pkg/connector/config.go`
- **Action**: Modify existing file
- **Changes**:
  1. Update `Config` struct:
     ```go
     type Config struct {
         DefaultModel       string  `yaml:"default_model"`
         MaxTokens          int     `yaml:"max_tokens"`
         Temperature        float64 `yaml:"temperature"`
         SystemPrompt       string  `yaml:"system_prompt"`
         ConversationMaxAge int     `yaml:"conversation_max_age_hours"`
         RateLimitPerMinute int     `yaml:"rate_limit_per_minute"`
     }
     ```
  2. Update `ExampleConfig` constant with Claude-specific options
  3. Add validation methods for config values

#### 2.2 Update Login Flow

**File**: `pkg/connector/login.go`
- **Action**: Modify existing file
- **Changes**:
  1. Remove `PasswordLogin` and `CookieLogin` structs
  2. Create new `APIKeyLogin` struct:
     ```go
     type APIKeyLogin struct {
         User      *bridgev2.User
         Connector *ClaudeConnector
     }
     
     func (a *APIKeyLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
         return &bridgev2.LoginStep{
             Type:         bridgev2.LoginStepTypeUserInput,
             StepID:       "api_key",
             Instructions: "Enter your Claude API key. Get one from: https://console.anthropic.com/settings/keys",
             UserInputParams: &bridgev2.LoginUserInputParams{
                 Fields: []bridgev2.LoginInputDataField{
                     {
                         Type:        bridgev2.LoginInputFieldTypePassword,
                         ID:          "api_key",
                         Name:        "API Key",
                         Description: "Your Claude API key (sk-ant-...)",
                     },
                 },
             },
         }, nil
     }
     
     func (a *APIKeyLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
         apiKey := input["api_key"]
         // Validate API key format
         if !strings.HasPrefix(apiKey, "sk-ant-") {
             return nil, fmt.Errorf("invalid API key format")
         }
         
         // Test the API key
         client := claudeapi.NewClient(apiKey, a.Connector.Log)
         if err := client.ValidateAPIKey(ctx); err != nil {
             return nil, fmt.Errorf("invalid API key: %w", err)
         }
         
         // Create user login
         userLogin, err := a.User.NewLogin(ctx, &database.UserLogin{
             ID:         networkid.UserLoginID(apiKey[:20]), // Use truncated key as ID
             RemoteName: "Claude API User",
             Metadata: &UserLoginMetadata{
                 APIKey: apiKey,
             },
         }, nil)
         if err != nil {
             return nil, err
         }
         
         // Set up client
         claudeClient := &ClaudeClient{
             Client:    client,
             UserLogin: userLogin,
             Connector: a.Connector,
             conversations: make(map[int64]*claudeapi.ConversationManager),
         }
         userLogin.Client = claudeClient
         
         return &bridgev2.LoginStep{
             Type:         bridgev2.LoginStepTypeComplete,
             StepID:       "complete",
             Instructions: "Successfully authenticated with Claude API",
             CompleteParams: &bridgev2.LoginCompleteParams{
                 UserLogin: userLogin,
             },
         }, nil
     }
     ```

#### 2.3 Update Client Implementation

**File**: `pkg/connector/client.go`
- **Action**: Modify existing file
- **Changes**:
  1. Rename `CandyClient` → `ClaudeClient`
  2. Update struct fields:
     ```go
     type ClaudeClient struct {
         Client        *claudeapi.Client
         UserLogin     *bridgev2.UserLogin
         Connector     *ClaudeConnector
         conversations map[int64]*claudeapi.ConversationManager
         convMu        sync.RWMutex
     }
     ```
  3. Remove WebSocket-related methods (`Connect`, `Disconnect`, WebSocket handlers)
  4. Update `HandleMatrixMessage`:
     ```go
     func (c *ClaudeClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
         meta := msg.Portal.Metadata.(*PortalMetadata)
         
         // Get or create conversation manager for this portal
         convMgr := c.getConversationManager(msg.Portal)
         
         // Add user message to history
         convMgr.AddMessage("user", msg.Content.Body)
         
         // Prepare API request
         req := &claudeapi.CreateMessageRequest{
             Model:       meta.Model,
             Messages:    convMgr.GetMessages(),
             MaxTokens:   c.Connector.Config.MaxTokens,
             Temperature: meta.Temperature,
             System:      meta.SystemPrompt,
             Stream:      true, // Use streaming for better UX
         }
         
         // Send to Claude API
         stream, err := c.Client.CreateMessageStream(ctx, req)
         if err != nil {
             return nil, err
         }
         
         // Collect response
         var responseText strings.Builder
         var claudeMessageID string
         var tokensUsed int
         
         for event := range stream {
             switch event.Type {
             case "message_start":
                 claudeMessageID = event.Message.ID
             case "content_block_delta":
                 if event.Delta != nil && event.Delta.Text != "" {
                     responseText.WriteString(event.Delta.Text)
                     // Optionally send typing indicator or live update
                 }
             case "message_delta":
                 if event.Usage != nil {
                     tokensUsed = event.Usage.OutputTokens
                 }
             }
         }
         
         // Add assistant response to conversation history
         convMgr.AddMessage("assistant", responseText.String())
         
         // Create response message
         resp := &bridgev2.MatrixMessageResponse{
             DB: &database.Message{
                 ID:        networkid.MessageID(claudeMessageID),
                 Timestamp: time.Now(),
                 Metadata: &MessageMetadata{
                     ClaudeMessageID: claudeMessageID,
                     TokensUsed:      tokensUsed,
                 },
             },
         }
         
         // Queue the assistant's response as an incoming message
         c.queueAssistantResponse(msg.Portal, responseText.String(), claudeMessageID, tokensUsed)
         
         return resp, nil
     }
     ```
  5. Add helper methods:
     ```go
     func (c *ClaudeClient) getConversationManager(portal *bridgev2.Portal) *claudeapi.ConversationManager
     func (c *ClaudeClient) queueAssistantResponse(portal *bridgev2.Portal, text, messageID string, tokens int)
     func (c *ClaudeClient) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error
     ```

**File**: `pkg/connector/conversation.go`
- **Action**: Create new file
- **Purpose**: Conversation context persistence and management
- **Key Functions**:
  ```go
  func (c *ClaudeClient) loadConversationHistory(ctx context.Context, portal *bridgev2.Portal) error
  func (c *ClaudeClient) saveConversationHistory(ctx context.Context, portal *bridgev2.Portal) error
  func (c *ClaudeClient) clearConversationHistory(ctx context.Context, portal *bridgev2.Portal) error
  ```
- **Implementation Details**:
  - Store conversation messages in portal metadata
  - Implement token-based trimming
  - Handle context window limits

#### 2.4 Update Chat Info & Portal Management

**File**: `pkg/connector/chatinfo.go`
- **Action**: Modify existing file
- **Changes**:
  1. Update `GetChatInfo` to reflect Claude conversations:
     ```go
     func (c *ClaudeClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
         meta := portal.Metadata.(*PortalMetadata)
         
         roomType := database.RoomTypeDM
         ghostID := MakeClaudeGhostID(meta.Model)
         
         return &bridgev2.ChatInfo{
             Name:    &meta.ConversationName,
             Members: &bridgev2.ChatMemberList{
                 IsFull: true,
                 Members: []bridgev2.ChatMember{
                     {
                         EventSender: bridgev2.EventSender{
                             IsFromMe: false,
                             Sender:   ghostID,
                         },
                     },
                 },
             },
             Type: &roomType,
         }, nil
     }
     ```
  2. Update `GetCapabilities`:
     ```go
     func (c *ClaudeClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *bridgev2.NetworkRoomCapabilities {
         return &bridgev2.NetworkRoomCapabilities{
             FormattedText:    true,
             UserMentions:     false,
             RoomMentions:     false,
             LocationMessages: false,
             Captions:         false,
             MaxTextLength:    100000, // Claude has large context window
             Edits:            false,
             Deletes:          false,
             Reactions:        false,
             Replies:          true, // Could implement as conversation context
             ReadReceipts:     false,
         }
     }
     ```
  3. Remove `ResolveIdentifier` or simplify it (no need to resolve external identifiers)

**File**: `pkg/connector/ghost.go`
- **Action**: Modify existing file
- **Changes**:
  1. Update `GetUserInfo` for Claude models:
     ```go
     func (c *ClaudeClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
         meta := ghost.Metadata.(*GhostMetadata)
         
         modelName := meta.Model
         displayName := fmt.Sprintf("Claude (%s)", modelName)
         
         return &bridgev2.UserInfo{
             Name:  &displayName,
             IsBot: ptr.Ptr(true),
             Identifiers: []string{fmt.Sprintf("claude:%s", modelName)},
         }, nil
     }
     ```
  2. Helper functions:
     ```go
     func MakeClaudeGhostID(model string) networkid.UserID {
         return networkid.UserID(fmt.Sprintf("claude_%s", model))
     }
     ```

---

### Phase 3: Bridge Commands & Features (Priority: MEDIUM)

#### 3.1 Implement Bridge Commands

**File**: `pkg/connector/commands.go`
- **Action**: Create new file
- **Purpose**: Bridge bot commands for users
- **Commands to Implement**:
  - `!claude help` - Show help
  - `!claude login <api-key>` - Alternative login method
  - `!claude new [model]` - Start new conversation with optional model
  - `!claude model <model-name>` - Change model for current room
  - `!claude models` - List available models
  - `!claude clear` - Clear conversation history
  - `!claude system <prompt>` - Set system prompt for room
  - `!claude temperature <0.0-1.0>` - Set temperature
  - `!claude info` - Show current room settings and token usage
  - `!claude logout` - Logout and delete API key

**Implementation**:
```go
type CommandHandler struct {
    Connector *ClaudeConnector
}

func (h *CommandHandler) Handle(ctx context.Context, cmd *bridgev2.Command) (*bridgev2.CommandResult, error) {
    switch cmd.Command {
    case "help":
        return h.handleHelp(ctx, cmd)
    case "new":
        return h.handleNew(ctx, cmd)
    case "model":
        return h.handleModel(ctx, cmd)
    // ... etc
    }
}
```

#### 3.2 Add Rate Limiting

**File**: `pkg/connector/ratelimit.go`
- **Action**: Create new file
- **Purpose**: Client-side rate limiting
- **Implementation**:
  ```go
  type RateLimiter struct {
      tokensPerMinute int
      tokens          int
      lastRefill      time.Time
      mu              sync.Mutex
  }
  
  func (rl *RateLimiter) Wait(ctx context.Context) error
  ```

---

### Phase 4: Main Entry Point & Configuration (Priority: HIGH)

#### 4.1 Update Main Entry Point

**File**: `cmd/mautrix-claude/main.go`
- **Action**: Modify existing file
- **Changes**:
  1. Change package comment from candy to claude
  2. Update `BridgeMain`:
     ```go
     m := mxmain.BridgeMain{
         Name:        "mautrix-claude",
         URL:         "https://github.com/mautrix/claude",
         Description: "A Matrix-Claude API bridge",
         Version:     "0.1.0",
         Connector:   connector.NewConnector(),
     }
     ```

#### 4.2 Update Configuration Files

**File**: `example-config.yaml`
- **Action**: Modify existing file
- **Changes**:
  1. Update appservice ID and bot username:
     ```yaml
     id: claude
     bot_username: claudebot
     bot_displayname: Claude AI bridge bot
     bot_avatar: mxc://maunium.net/claude
     ```
  2. Update bridge config section:
     ```yaml
     bridge:
       username_template: claude_{{.}}
       displayname_template: "Claude ({{.Model}})"
       command_prefix: "!claude"
     ```
  3. Add Claude-specific config:
     ```yaml
     claude:
       # Default Claude model to use
       default_model: claude-3-5-sonnet-20241022
       
       # Maximum tokens for responses
       max_tokens: 4096
       
       # Temperature (0.0-1.0)
       temperature: 1.0
       
       # Default system prompt
       system_prompt: "You are a helpful AI assistant."
       
       # Maximum conversation age in hours (0 = unlimited)
       conversation_max_age_hours: 24
       
       # Rate limiting (requests per minute, 0 = unlimited)
       rate_limit_per_minute: 60
     ```

**File**: `docker-compose.yaml`
- **Action**: Modify existing file
- **Changes**:
  - Update service name from `mautrix-candy` to `mautrix-claude`
  - Update image name
  - Update volume paths

**File**: `Dockerfile`
- **Action**: Modify existing file
- **Changes**:
  - Update binary name from `mautrix-candy` to `mautrix-claude`
  - Update build path: `./cmd/mautrix-claude`

**File**: `.gitlab-ci.yml`
- **Action**: Modify existing file (if applicable)
- **Changes**:
  - Update references from candy to claude

---

### Phase 5: Build System & Dependencies (Priority: HIGH)

#### 5.1 Update Go Module

**File**: `go.mod`
- **Action**: Modify existing file
- **Changes**:
  1. Change module name: `go.mau.fi/mautrix-claude`
  2. Remove unused dependencies (PuerkitoBio/goquery if not needed)
  3. No new dependencies needed (Claude API is REST/SSE, standard library is sufficient)

#### 5.2 Update Import Paths

**Files**: All Go files
- **Action**: Find and replace
- **Changes**:
  - `go.mau.fi/mautrix-candy` → `go.mau.fi/mautrix-claude`
  - `pkg/candygo` → `pkg/claudeapi`
  - `CandyConnector` → `ClaudeConnector`
  - `CandyClient` → `ClaudeClient`

---

### Phase 6: Cleanup & Removal (Priority: MEDIUM)

#### 6.1 Remove Unused Files

**Files to Delete**:
- `pkg/candygo/turbostream.go` - No HTML parsing needed
- `pkg/candygo/csrf.go` - No CSRF tokens with API
- `pkg/candygo/websocket.go` - No WebSocket, using SSE
- Any capture data or documentation specific to Candy.ai

#### 6.2 Remove Unused Code

**In remaining files**, remove:
- All Turbo Stream parsing logic
- HTML parsing (goquery usage)
- ActionCable/WebSocket code
- CSRF token handling
- Cookie/session management (beyond API key storage)

---

### Phase 7: Testing & Documentation (Priority: MEDIUM)

#### 7.1 Create Tests

**Files to Create**:
- `pkg/claudeapi/*_test.go` - Unit tests for API client
- `pkg/connector/*_test.go` - Unit tests for connector logic

**Test Coverage**:
- API key validation
- Message sending/receiving
- Streaming events
- Conversation context management
- Error handling (rate limits, auth failures)
- Model selection
- Configuration validation

#### 7.2 Documentation

**File**: `README.md`
- **Action**: Create new file
- **Content**:
  - What the bridge does
  - How to get a Claude API key
  - Installation instructions (Docker, manual)
  - Configuration guide
  - Usage examples
  - Troubleshooting

**File**: `docs/setup.md`
- **Action**: Create new file
- **Content**:
  - Step-by-step setup guide
  - Homeserver configuration
  - Bridge configuration
  - Testing the bridge

**File**: `docs/commands.md`
- **Action**: Create new file
- **Content**:
  - List of all bot commands
  - Usage examples
  - Advanced features

---

## Implementation Steps (Ordered)

### Step 1: Preparation
1. Create backup/tag of current mautrix-candy state
2. Create feature branch: `feature/claude-api-migration`
3. Update `go.mod` module name
4. Update all import paths across codebase

### Step 2: Build Claude API Client
1. Create `pkg/claudeapi/` directory
2. Implement `types.go` with Claude API structures
3. Implement `client.go` with HTTP client
4. Implement `streaming.go` for SSE support
5. Implement `conversations.go` for context management
6. Implement `errors.go` for error handling
7. Implement `models.go` for model definitions
8. Write unit tests for API client

### Step 3: Update Connector Layer
1. Update `pkg/connector/connector.go`:
   - Rename structs
   - Update metadata types
   - Update GetName()
   - Update GetLoginFlows()
2. Update `pkg/connector/config.go`:
   - New Config struct
   - New example config
3. Update `pkg/connector/login.go`:
   - Remove old login flows
   - Implement APIKeyLogin
4. Update `pkg/connector/client.go`:
   - Remove WebSocket code
   - Implement HandleMatrixMessage with Claude API
   - Add conversation management
5. Create `pkg/connector/conversation.go`:
   - Conversation persistence
   - Context management
6. Update `pkg/connector/chatinfo.go`:
   - Update GetChatInfo
   - Update GetCapabilities
7. Update `pkg/connector/ghost.go`:
   - Update ghost info for Claude models

### Step 4: Remove Unused Code
1. Delete unused files from old `pkg/candygo/`
2. Remove HTML parsing dependencies
3. Remove WebSocket code
4. Clean up unused imports

### Step 5: Update Configuration & Main
1. Update `cmd/mautrix-claude/main.go`
2. Update `example-config.yaml`
3. Update `Dockerfile`
4. Update `docker-compose.yaml`
5. Update `.dockerignore` if needed

### Step 6: Implement Commands
1. Create `pkg/connector/commands.go`
2. Implement all bridge commands
3. Add rate limiting in `pkg/connector/ratelimit.go`

### Step 7: Testing
1. Build the bridge: `go build ./cmd/mautrix-claude`
2. Create test config with Claude API key
3. Run bridge and test:
   - Login with API key
   - Send message
   - Receive response
   - Test model switching
   - Test conversation context
   - Test commands
   - Test error handling

### Step 8: Documentation
1. Write `README.md`
2. Write `docs/setup.md`
3. Write `docs/commands.md`
4. Add inline code comments
5. Create migration guide for any Candy users (if needed)

### Step 9: Polish & Release
1. Add proper error messages
2. Add logging for debugging
3. Performance optimization
4. Security review (API key storage)
5. Create GitHub release
6. Publish Docker image

---

## Claude API Integration Details

### Authentication
- **Method**: API Key via `x-api-key` header
- **Storage**: Encrypted in database via UserLoginMetadata
- **Validation**: Test API call on login

### API Endpoints
- **Messages**: `POST /v1/messages`
  - Non-streaming: Returns complete response
  - Streaming: Returns SSE stream

### Headers Required
```
x-api-key: sk-ant-...
anthropic-version: 2023-06-01
content-type: application/json
```

### Streaming Format
Server-Sent Events (SSE) with these event types:
- `message_start`: Message begins
- `content_block_start`: Content block starts
- `content_block_delta`: Incremental content (text streaming)
- `content_block_stop`: Content block ends
- `message_delta`: Message metadata (usage stats)
- `message_stop`: Message complete

### Rate Limits
- Tier-based (depends on API usage/payment)
- 429 status code when exceeded
- Should implement client-side rate limiting
- Respect `retry-after` header

### Error Handling
- 400: Invalid request (bad model, malformed JSON)
- 401: Invalid API key
- 403: Forbidden (permission issue)
- 429: Rate limit exceeded
- 500: Server error (retry with backoff)
- 529: Overloaded (retry with backoff)

### Conversation Context Management
Claude API is **stateless** - must send full conversation history each time:
1. Store messages in portal metadata
2. Build message array for each request
3. Trim old messages if approaching token limit
4. Alternate roles: user, assistant, user, assistant...
5. System prompt separate from messages

### Token Management
- Input + Output tokens counted
- Different models have different limits:
  - Opus: 200k context, 4k output
  - Sonnet: 200k context, 4k output
  - Haiku: 200k context, 4k output
- Track usage per message for statistics
- Implement conversation trimming based on token count

---

## Configuration Options

### Required Config
- `api_key`: User's Claude API key (via login, stored in database)

### Optional Config (with defaults)
- `default_model`: claude-3-5-sonnet-20241022
- `max_tokens`: 4096
- `temperature`: 1.0
- `system_prompt`: "You are a helpful AI assistant."
- `conversation_max_age_hours`: 24
- `rate_limit_per_minute`: 60

### Per-Room Settings (stored in PortalMetadata)
- `model`: Override default model
- `system_prompt`: Custom system prompt
- `temperature`: Custom temperature

---

## Matrix Integration

### Room Management
- Each conversation = one Matrix room (DM with Claude ghost)
- Ghost user ID: `@claude_<model>:bridge`
- Room name: "Conversation with Claude (<model>)"
- Support multiple rooms per user (multiple conversations)

### Message Handling
1. User sends message in Matrix room
2. Bridge receives via HandleMatrixMessage
3. Add to conversation history
4. Send to Claude API with full context
5. Stream response back
6. Queue as incoming message from ghost
7. Update conversation history

### User Mapping
- One Matrix user can have one login (API key)
- One login can have multiple portals (conversations)
- No concept of "ghost users" for other humans (only Claude models)

---

## Security Considerations

### API Key Storage
- Store encrypted in database
- Never log API keys
- Validate format before storage
- Allow users to delete/update keys

### Rate Limiting
- Implement client-side rate limiting
- Prevent API abuse
- Respect Claude's rate limits
- Handle 429 responses gracefully

### Input Validation
- Validate model names against whitelist
- Validate temperature range (0.0-1.0)
- Validate max_tokens (within model limits)
- Sanitize user input

### Error Messages
- Don't expose API keys in errors
- Don't expose internal details
- User-friendly error messages
- Log detailed errors server-side

---

## Potential Issues & Mitigations

### Issue 1: Conversation Context Too Large
- **Problem**: Conversation history exceeds token limit
- **Mitigation**: 
  - Implement token counting
  - Automatically trim oldest messages
  - Allow manual "clear context" command
  - Show warning when approaching limit

### Issue 2: Rate Limiting
- **Problem**: User hits API rate limits
- **Mitigation**:
  - Client-side rate limiting
  - Queue messages if needed
  - Clear error messages
  - Exponential backoff on retries

### Issue 3: API Key Exposure
- **Problem**: API key could be leaked
- **Mitigation**:
  - Encrypt in database
  - Never log keys
  - Secure login flow
  - Allow easy key rotation

### Issue 4: Streaming Interruption
- **Problem**: SSE stream disconnects mid-response
- **Mitigation**:
  - Detect partial responses
  - Retry with fallback to non-streaming
  - Handle context cancellation
  - Clear error messages

### Issue 5: Cost Control
- **Problem**: Users could rack up API costs
- **Mitigation**:
  - Show token usage in info command
  - Implement conversation age limits
  - Rate limiting
  - Clear documentation about costs

### Issue 6: Model Selection Confusion
- **Problem**: Users may not understand model differences
- **Mitigation**:
  - Clear model descriptions in help
  - Sensible defaults
  - Show current model in room name/topic
  - Easy model switching

---

## Estimated Complexity

### Backend Complexity: **MEDIUM**
- Simplified from Candy.ai (no WebSocket, no HTML parsing)
- REST API integration is straightforward
- SSE streaming adds moderate complexity
- Conversation context management is the main challenge

### Overall Complexity: **MEDIUM**
- Well-defined API (official Claude API docs)
- No frontend needed (Matrix clients)
- Existing mautrix framework handles Matrix side
- Main work is adapting Candy bridge to Claude API

### Time Estimate: **2-3 days** for experienced Go developer
- Day 1: API client + basic connector (Phases 1-2)
- Day 2: Commands + testing (Phases 3, 6-7)
- Day 3: Polish + documentation (Phases 4-5, 8-9)

---

## Dependencies & Requirements

### Build Dependencies
- Go 1.23+
- Standard library only for core functionality
- Existing mautrix dependencies (already in go.mod)

### Runtime Dependencies
- Matrix homeserver (Synapse, Dendrite, or Conduit)
- SQLite or PostgreSQL database
- Claude API key (from user)

### No New External Dependencies Needed
- HTTP client: `net/http` (standard library)
- SSE parsing: manual (simple text parsing)
- JSON: `encoding/json` (standard library)
- Logging: `zerolog` (already imported)

---

## Success Criteria

### Must Have (MVP)
- [ ] Users can login with Claude API key
- [ ] Users can send messages and receive responses
- [ ] Conversation context is maintained
- [ ] Multiple conversations supported (multiple rooms)
- [ ] Basic error handling (auth, rate limits)
- [ ] Model selection works
- [ ] Configuration via YAML

### Should Have
- [ ] Streaming responses (SSE)
- [ ] Bridge commands (model, clear, info, etc.)
- [ ] Rate limiting
- [ ] Token usage tracking
- [ ] Automatic context trimming
- [ ] System prompt customization
- [ ] Temperature control

### Nice to Have
- [ ] Conversation persistence across restarts
- [ ] Usage statistics
- [ ] Multiple API keys (different users)
- [ ] Image support (Claude can process images)
- [ ] Cost estimation/tracking
- [ ] Conversation export

---

## Next Steps

1. **Review this plan** with stakeholders/users
2. **Confirm Claude API access** (have valid API key for testing)
3. **Create feature branch** for development
4. **Begin Phase 1** - Build Claude API client
5. **Iterative development** following the implementation steps
6. **Continuous testing** with real Claude API
7. **Documentation** throughout development
8. **Release** when success criteria met

---

## References

- [Claude API Documentation](https://docs.anthropic.com/claude/reference/)
- [mautrix-go Documentation](https://pkg.go.dev/maunium.net/go/mautrix)
- [Matrix Specification](https://spec.matrix.org/)
- [Server-Sent Events Spec](https://html.spec.whatwg.org/multipage/server-sent-events.html)

---

**Document Version**: 1.0  
**Created**: 2026-01-24  
**Status**: Ready for Implementation
