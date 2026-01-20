package candygo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	userIDRegex  = regexp.MustCompile(`gid://candy-ai/User/(\d+)`)
	userGIDRegex = regexp.MustCompile(`"(gid://candy-ai/User/\d+)"`)
	// For extracting user ID from various places in HTML
	userDataRegex = regexp.MustCompile(`data-user-id="(\d+)"`)
	streamNameUserRegex = regexp.MustCompile(`Z2lkOi8vY2FuZHktYWkvVXNlci8([A-Za-z0-9+/=]+)`)
)

// LoginCredentials holds the credentials for logging in.
type LoginCredentials struct {
	Email    string
	Password string
}

// Login authenticates with candy.ai using email and password.
func (c *Client) Login(ctx context.Context, creds *LoginCredentials) error {
	c.Log.Info().Str("email", creds.Email).Msg("Logging in to candy.ai")

	// First, load the homepage to get a CSRF token
	html, err := c.GetHTML(ctx, "/")
	if err != nil {
		return fmt.Errorf("failed to load homepage: %w", err)
	}

	// Extract CSRF token
	if !c.csrf.UpdateFromHTML(html) {
		return fmt.Errorf("failed to extract CSRF token from homepage")
	}

	// Prepare login form data
	formData := url.Values{
		"authenticity_token": {c.csrf.GetToken()},
		"user[email]":        {creds.Email},
		"user[password]":     {creds.Password},
		"profile_id":         {""},
		"prompt":             {""},
		"button":             {""},
	}

	// Create login request
	req, err := c.newRequest(ctx, http.MethodPost, "/users/sign_in", strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Turbo-Frame", "sign-in-modal")
	req.Header.Set("X-Turbo-Request-Id", generateTurboRequestID())

	// Send login request
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Check for success - redirect response or turbo stream redirect
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusFound {
		// Check if it's a turbo stream redirect (successful login)
		if strings.Contains(bodyStr, `action="redirect_to"`) || resp.StatusCode == http.StatusFound {
			c.Log.Info().Msg("Login successful")

			// Extract session info
			session := &Session{
				Email: creds.Email,
			}

			// Get session cookie
			for _, cookie := range c.HTTP.Jar.Cookies(req.URL) {
				if cookie.Name == "_chat_chat_session" {
					session.Cookie = cookie.Value
					break
				}
			}

			if session.Cookie == "" {
				return fmt.Errorf("no session cookie received")
			}

			// Fetch user info
			if err := c.fetchUserInfo(ctx, session); err != nil {
				c.Log.Warn().Err(err).Msg("Failed to fetch user info, continuing anyway")
			}

			c.SetSession(session)
			return nil
		}
	}

	// Check for error messages in response
	if strings.Contains(bodyStr, "Invalid Email or password") {
		return fmt.Errorf("invalid email or password")
	}

	return fmt.Errorf("login failed with status %d", resp.StatusCode)
}

// LoginWithCookie authenticates using an existing session cookie.
func (c *Client) LoginWithCookie(ctx context.Context, cookie string) error {
	c.Log.Info().Msg("Logging in with existing cookie")

	// Set the cookie first
	session := &Session{
		Cookie: cookie,
	}
	c.SetSession(session)

	// Verify the session is valid by fetching user info
	if err := c.fetchUserInfo(ctx, session); err != nil {
		c.SetSession(nil)
		return fmt.Errorf("invalid session cookie: %w", err)
	}

	c.SetSession(session)
	c.Log.Info().Int64("user_id", session.UserID).Msg("Cookie login successful")

	return nil
}

// fetchUserInfo fetches and populates user information.
func (c *Client) fetchUserInfo(ctx context.Context, session *Session) error {
	// Load a page that contains user info
	html, err := c.GetHTML(ctx, "/")
	if err != nil {
		return fmt.Errorf("failed to load homepage: %w", err)
	}

	// Update CSRF token
	c.csrf.UpdateFromHTML(html)

	// Try to extract user ID from various sources

	// Try data attribute
	if matches := userDataRegex.FindStringSubmatch(html); len(matches) > 1 {
		if id, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			session.UserID = id
			session.UserGID = fmt.Sprintf("gid://candy-ai/User/%d", id)
		}
	}

	// Try stream name (base64 encoded GID)
	if session.UserID == 0 {
		if matches := userGIDRegex.FindStringSubmatch(html); len(matches) > 1 {
			session.UserGID = matches[1]
			if idMatches := userIDRegex.FindStringSubmatch(matches[1]); len(idMatches) > 1 {
				session.UserID, _ = strconv.ParseInt(idMatches[1], 10, 64)
			}
		}
	}

	// Try to extract from signed stream names
	if session.UserID == 0 {
		session.UserID = extractUserIDFromStreamNames(html)
		if session.UserID != 0 {
			session.UserGID = fmt.Sprintf("gid://candy-ai/User/%d", session.UserID)
		}
	}

	if session.UserID == 0 {
		// Not critical, we can still operate
		c.Log.Warn().Msg("Could not extract user ID from page")
	}

	return nil
}

// Logout logs out of the current session.
func (c *Client) Logout(ctx context.Context) error {
	if !c.IsLoggedIn() {
		return nil
	}

	c.Disconnect()

	// Send logout request
	req, err := c.newRequest(ctx, http.MethodDelete, "/users/sign_out", nil)
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	c.SetSession(nil)
	c.Log.Info().Msg("Logged out")

	return nil
}

// generateTurboRequestID generates a UUID-like request ID for Turbo.
func generateTurboRequestID() string {
	// Simple UUID v4 generation
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		randUint32(),
		randUint16(),
		(randUint16()&0x0fff)|0x4000,
		(randUint16()&0x3fff)|0x8000,
		randUint48(),
	)
}

// Simple random number generators for UUID
func randUint32() uint32 {
	return uint32(randSource.Int63())
}

func randUint16() uint16 {
	return uint16(randSource.Int63())
}

func randUint48() uint64 {
	return uint64(randSource.Int63()) & 0xffffffffffff
}

// extractUserIDFromStreamNames tries to extract user ID from signed stream name in HTML.
func extractUserIDFromStreamNames(html string) int64 {
	// Look for base64 encoded stream names that contain user GID
	// Pattern: Z2lkOi8vY2FuZHktYWkvVXNlci8... (base64 of "gid://candy-ai/User/...")

	// The pattern after "User/" in base64 will vary based on the user ID
	// We need to find and decode it

	streamNameRegex := regexp.MustCompile(`signed_stream_name['":\s]+['"]([A-Za-z0-9+/=]+--[a-f0-9]+)['"]`)
	matches := streamNameRegex.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) > 1 {
			// Split on -- to get the base64 part
			parts := strings.Split(match[1], "--")
			if len(parts) > 0 {
				decoded, err := decodeBase64(parts[0])
				if err == nil && strings.Contains(decoded, "gid://candy-ai/User/") {
					if idMatches := userIDRegex.FindStringSubmatch(decoded); len(idMatches) > 1 {
						id, _ := strconv.ParseInt(idMatches[1], 10, 64)
						return id
					}
				}
			}
		}
	}

	return 0
}
