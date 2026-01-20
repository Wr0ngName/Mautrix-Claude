package candygo

import (
	"context"
	"net/http"
	"regexp"
	"sync"
)

var (
	csrfMetaRegex = regexp.MustCompile(`<meta\s+name="csrf-token"\s+content="([^"]+)"`)
	csrfInputRegex = regexp.MustCompile(`name="authenticity_token"\s+value="([^"]+)"`)
)

// CSRFManager manages CSRF tokens for candy.ai requests.
type CSRFManager struct {
	client *Client
	token  string
	mu     sync.RWMutex
}

// NewCSRFManager creates a new CSRF token manager.
func NewCSRFManager(client *Client) *CSRFManager {
	return &CSRFManager{
		client: client,
	}
}

// GetToken returns the current CSRF token.
func (m *CSRFManager) GetToken() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.token
}

// SetToken sets the CSRF token.
func (m *CSRFManager) SetToken(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = token
	m.client.Log.Debug().Str("token", token[:min(10, len(token))]+"...").Msg("CSRF token updated")
}

// ExtractFromHTML extracts a CSRF token from HTML content.
func (m *CSRFManager) ExtractFromHTML(html string) string {
	// Try meta tag first
	if matches := csrfMetaRegex.FindStringSubmatch(html); len(matches) > 1 {
		return matches[1]
	}

	// Try hidden input field
	if matches := csrfInputRegex.FindStringSubmatch(html); len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// UpdateFromHTML extracts and sets the CSRF token from HTML.
func (m *CSRFManager) UpdateFromHTML(html string) bool {
	token := m.ExtractFromHTML(html)
	if token != "" {
		m.SetToken(token)
		return true
	}
	return false
}

// UpdateFromResponse extracts CSRF token from response headers or body.
func (m *CSRFManager) UpdateFromResponse(resp *http.Response) {
	// Check for CSRF token in headers (some frameworks do this)
	if token := resp.Header.Get("X-CSRF-Token"); token != "" {
		m.SetToken(token)
	}
}

// Refresh fetches a fresh CSRF token by loading a page.
func (m *CSRFManager) Refresh(ctx context.Context) error {
	html, err := m.client.GetHTML(ctx, "/")
	if err != nil {
		return err
	}

	if !m.UpdateFromHTML(html) {
		m.client.Log.Warn().Msg("Failed to extract CSRF token from homepage")
	}

	return nil
}

// GetAuthenticityToken returns a token suitable for form submissions.
// This may be different from the X-CSRF-Token header value.
func (m *CSRFManager) GetAuthenticityToken(ctx context.Context, pageHTML string) string {
	// First try to extract from the provided page HTML
	if matches := csrfInputRegex.FindStringSubmatch(pageHTML); len(matches) > 1 {
		return matches[1]
	}

	// Fall back to the stored token
	return m.GetToken()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
