package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-candy/pkg/candygo"
)

// PasswordLogin implements the password-based login flow.
type PasswordLogin struct {
	User      *bridgev2.User
	Connector *CandyConnector
	Email     string
	Password  string
}

var _ bridgev2.LoginProcess = (*PasswordLogin)(nil)

// Start starts the login process.
func (p *PasswordLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "email",
		Instructions: "Enter your candy.ai email address",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypeEmail,
					ID:          "email",
					Name:        "Email",
					Description: "Your candy.ai account email",
				},
			},
		},
	}, nil
}

// SubmitUserInput handles user input during login.
func (p *PasswordLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if email, ok := input["email"]; ok && p.Email == "" {
		p.Email = email
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       "password",
			Instructions: "Enter your candy.ai password",
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{
						Type:        bridgev2.LoginInputFieldTypePassword,
						ID:          "password",
						Name:        "Password",
						Description: "Your candy.ai account password",
					},
				},
			},
		}, nil
	}

	if password, ok := input["password"]; ok {
		p.Password = password
		return p.doLogin(ctx)
	}

	return nil, fmt.Errorf("unexpected input state")
}

// doLogin performs the actual login.
func (p *PasswordLogin) doLogin(ctx context.Context) (*bridgev2.LoginStep, error) {
	p.Connector.Log.Info().Str("email", p.Email).Msg("Attempting login")

	client := candygo.NewClient(p.Connector.Log.With().Str("user", string(p.User.MXID)).Logger())

	creds := &candygo.LoginCredentials{
		Email:    p.Email,
		Password: p.Password,
	}

	if err := client.Login(ctx, creds); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	session := client.GetSession()
	if session == nil {
		return nil, fmt.Errorf("no session after login")
	}

	// Create user login
	userLogin, err := p.User.NewLogin(ctx, &database.UserLogin{
		ID:         networkid.UserLoginID(fmt.Sprintf("%d", session.UserID)),
		RemoteName: p.Email,
		Metadata: &UserLoginMetadata{
			Cookie: session.Cookie,
			UserID: session.UserID,
			Email:  session.Email,
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create user login: %w", err)
	}

	// Set up the client
	candyClient := &CandyClient{
		Client:    client,
		UserLogin: userLogin,
		Connector: p.Connector,
	}
	userLogin.Client = candyClient

	// Connect WebSocket in background
	go candyClient.Connect(context.Background())

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "complete",
		Instructions: fmt.Sprintf("Successfully logged in as %s", p.Email),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLogin: userLogin,
		},
	}, nil
}

// Cancel cancels the login process.
func (p *PasswordLogin) Cancel() {}

// CookieLogin implements the cookie-based login flow.
type CookieLogin struct {
	User      *bridgev2.User
	Connector *CandyConnector
}

var _ bridgev2.LoginProcess = (*CookieLogin)(nil)

// Start starts the cookie login process.
func (c *CookieLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "cookie",
		Instructions: "Enter your candy.ai session cookie. You can find this in your browser's developer tools after logging in to candy.ai. Look for the cookie named '_chat_chat_session'.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "cookie",
					Name:        "Session Cookie",
					Description: "The _chat_chat_session cookie value",
				},
			},
		},
	}, nil
}

// SubmitUserInput handles the cookie input.
func (c *CookieLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	cookie, ok := input["cookie"]
	if !ok || cookie == "" {
		return nil, fmt.Errorf("cookie is required")
	}

	c.Connector.Log.Info().Msg("Attempting cookie login")

	client := candygo.NewClient(c.Connector.Log.With().Str("user", string(c.User.MXID)).Logger())

	if err := client.LoginWithCookie(ctx, cookie); err != nil {
		return nil, fmt.Errorf("cookie login failed: %w", err)
	}

	session := client.GetSession()
	if session == nil {
		return nil, fmt.Errorf("no session after login")
	}

	// Create user login
	userLogin, err := c.User.NewLogin(ctx, &database.UserLogin{
		ID:         networkid.UserLoginID(fmt.Sprintf("%d", session.UserID)),
		RemoteName: session.Email,
		Metadata: &UserLoginMetadata{
			Cookie: session.Cookie,
			UserID: session.UserID,
			Email:  session.Email,
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create user login: %w", err)
	}

	// Set up the client
	candyClient := &CandyClient{
		Client:    client,
		UserLogin: userLogin,
		Connector: c.Connector,
	}
	userLogin.Client = candyClient

	// Connect WebSocket in background
	go candyClient.Connect(context.Background())

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "complete",
		Instructions: "Successfully logged in with cookie",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLogin: userLogin,
		},
	}, nil
}

// Cancel cancels the login process.
func (c *CookieLogin) Cancel() {}
