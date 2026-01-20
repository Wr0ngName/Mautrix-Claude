package candygo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	DefaultBaseURL   = "https://candy.ai"
	DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:147.0) Gecko/20100101 Firefox/147.0"
	DefaultTimeout   = 30 * time.Second
)

// Client is the main candy.ai API client.
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
	Log       zerolog.Logger

	session      *Session
	sessionLock  sync.RWMutex
	csrf         *CSRFManager
	ws           *ActionCableClient
	eventHandler EventHandler

	// Channel for stopping background tasks
	stopChan chan struct{}
}

// NewClient creates a new candy.ai client.
func NewClient(log zerolog.Logger) *Client {
	jar, _ := cookiejar.New(nil)

	c := &Client{
		HTTP: &http.Client{
			Timeout: DefaultTimeout,
			Jar:     jar,
		},
		BaseURL:   DefaultBaseURL,
		UserAgent: DefaultUserAgent,
		Log:       log.With().Str("component", "candygo").Logger(),
		stopChan:  make(chan struct{}),
	}

	c.csrf = NewCSRFManager(c)

	return c
}

// SetEventHandler sets the handler for candy.ai events.
func (c *Client) SetEventHandler(handler EventHandler) {
	c.eventHandler = handler
}

// emitEvent sends an event to the handler if one is set.
func (c *Client) emitEvent(evt Event) {
	if c.eventHandler != nil {
		c.eventHandler(evt)
	}
}

// GetSession returns the current session (thread-safe).
func (c *Client) GetSession() *Session {
	c.sessionLock.RLock()
	defer c.sessionLock.RUnlock()
	if c.session == nil {
		return nil
	}
	// Return a copy
	s := *c.session
	return &s
}

// SetSession sets the session (thread-safe).
func (c *Client) SetSession(session *Session) {
	c.sessionLock.Lock()
	defer c.sessionLock.Unlock()
	c.session = session

	// Update cookies
	if session != nil && session.Cookie != "" {
		c.setCookie(session.Cookie)
	}
}

// setCookie sets the session cookie.
func (c *Client) setCookie(cookie string) {
	u, _ := url.Parse(c.BaseURL)
	c.HTTP.Jar.SetCookies(u, []*http.Cookie{
		{
			Name:     "_chat_chat_session",
			Value:    cookie,
			Path:     "/",
			Domain:   u.Host,
			Secure:   true,
			HttpOnly: true,
		},
	})
}

// IsLoggedIn returns whether the client has an active session.
func (c *Client) IsLoggedIn() bool {
	return c.GetSession() != nil
}

// newRequest creates a new HTTP request with common headers.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := c.BaseURL + path

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "text/vnd.turbo-stream.html, text/html, application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", c.BaseURL)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	// Add CSRF token if available
	if token := c.csrf.GetToken(); token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}

	return req, nil
}

// do performs an HTTP request.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	c.Log.Debug().
		Str("method", req.Method).
		Str("url", req.URL.String()).
		Msg("HTTP request")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	c.Log.Debug().
		Int("status", resp.StatusCode).
		Str("url", req.URL.String()).
		Msg("HTTP response")

	// Update CSRF token from response if present
	c.csrf.UpdateFromResponse(resp)

	// Update session cookie if changed
	c.updateSessionCookie(resp)

	return resp, nil
}

// updateSessionCookie extracts and updates the session cookie from a response.
func (c *Client) updateSessionCookie(resp *http.Response) {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "_chat_chat_session" && cookie.Value != "" {
			c.sessionLock.Lock()
			if c.session != nil {
				c.session.Cookie = cookie.Value
			}
			c.sessionLock.Unlock()
			break
		}
	}
}

// Get performs a GET request.
func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// GetHTML performs a GET request and returns the HTML body.
func (c *Client) GetHTML(ctx context.Context, path string) (string, error) {
	resp, err := c.Get(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read body: %w", err)
	}

	return string(body), nil
}

// Connect establishes the WebSocket connection for real-time events.
func (c *Client) Connect(ctx context.Context) error {
	if !c.IsLoggedIn() {
		return fmt.Errorf("not logged in")
	}

	c.ws = NewActionCableClient(c)

	if err := c.ws.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect WebSocket: %w", err)
	}

	return nil
}

// Disconnect closes the WebSocket connection.
func (c *Client) Disconnect() {
	if c.ws != nil {
		c.ws.Close()
		c.ws = nil
	}
}

// SubscribeToConversation subscribes to real-time updates for a conversation.
func (c *Client) SubscribeToConversation(ctx context.Context, conversationID int64) error {
	if c.ws == nil {
		return fmt.Errorf("not connected")
	}

	// We need the signed stream names from the conversation page
	// This will be fetched when loading the conversation
	return c.ws.SubscribeToConversation(ctx, conversationID)
}

// Stop stops all background tasks and disconnects.
func (c *Client) Stop() {
	close(c.stopChan)
	c.Disconnect()
}
