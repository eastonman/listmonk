package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/utils"
	"github.com/labstack/echo/v4"
	"github.com/zerodha/simplesessions/v3"
)

type loginTpl struct {
	Title       string
	Description string

	NextURI          string
	Nonce            string
	PasswordEnabled  bool
	OIDCProvider     string
	OIDCProviderLogo string
	Error            string
}

var oidcProviders = map[string]bool{
	"google.com":          true,
	"microsoftonline.com": true,
	"auth0.com":           true,
	"github.com":          true,
}

// handleLoginPage renders the login page and handles the login form.
func handleLoginPage(c echo.Context) error {
	// Process POST login request.
	var loginErr error
	if c.Request().Method == http.MethodPost {
		loginErr = doLogin(c)
		if loginErr == nil {
			return c.Redirect(http.StatusFound, utils.SanitizeURI(c.FormValue("next")))
		}
	}

	return renderLoginPage(c, loginErr)
}

// handleLogout logs a user out.
func handleLogout(c echo.Context) error {
	var (
		sess = c.Get(auth.SessionKey).(*simplesessions.Session)
	)

	// Clear the session.
	_ = sess.Destroy()

	return c.JSON(http.StatusOK, okResp{true})
}

// handleOIDCLogin initializes an OIDC request and redirects to the OIDC provider for login.
func handleOIDCLogin(c echo.Context) error {
	app := c.Get("app").(*App)

	// Verify that the request came from the login page (CSRF).
	nonce, err := c.Cookie("nonce")
	if err != nil || nonce.Value == "" || nonce.Value != c.FormValue("nonce") {
		return echo.NewHTTPError(http.StatusUnauthorized, app.i18n.T("users.invalidRequest"))
	}

	next := utils.SanitizeURI(c.FormValue("next"))
	if next == "/" {
		next = uriAdmin
	}

	return c.Redirect(http.StatusFound, app.auth.GetOIDCAuthURL(next, nonce.Value))
}

// handleOIDCFinish receives the redirect callback from the OIDC provider and completes the handshake.
func handleOIDCFinish(c echo.Context) error {
	app := c.Get("app").(*App)

	nonce, err := c.Cookie("nonce")
	if err != nil || nonce.Value == "" {
		return renderLoginPage(c, echo.NewHTTPError(http.StatusUnauthorized, app.i18n.T("users.invalidRequest")))
	}

	// Validate the OIDC token.
	oidcToken, claims, err := app.auth.ExchangeOIDCToken(c.Request().URL.Query().Get("code"), nonce.Value)
	if err != nil {
		return renderLoginPage(c, err)
	}

	// Get the user by e-mail received from OIDC.
	user, err := app.core.GetUser(0, "", claims.Email)
	if err != nil {
		return renderLoginPage(c, err)
	}

	// Update user login.
	if err := app.core.UpdateUserLogin(user.ID, claims.Picture); err != nil {
		return renderLoginPage(c, err)
	}

	// Set the session.
	if err := app.auth.SetSession(user, oidcToken, c); err != nil {
		return renderLoginPage(c, err)
	}

	return c.Redirect(http.StatusFound, utils.SanitizeURI(c.QueryParam("state")))
}

// renderLoginPage renders the login page and handles the login form.
func renderLoginPage(c echo.Context, loginErr error) error {
	var (
		app  = c.Get("app").(*App)
		next = utils.SanitizeURI(c.FormValue("next"))
	)

	if next == "/" {
		next = uriAdmin
	}

	oidcProvider := ""
	oidcProviderLogo := ""
	if app.constants.Security.OIDC.Enabled {
		oidcProviderLogo = "oidc.png"
		u, err := url.Parse(app.constants.Security.OIDC.Provider)
		if err == nil {
			h := strings.Split(u.Hostname(), ".")

			// Get the last two h for the root domain
			if len(h) >= 2 {
				oidcProvider = h[len(h)-2] + "." + h[len(h)-1]
			} else {
				oidcProvider = u.Hostname()
			}

			if _, ok := oidcProviders[oidcProvider]; ok {
				oidcProviderLogo = oidcProvider + ".png"
			}
		}
	}

	out := loginTpl{
		Title:            app.i18n.T("users.login"),
		PasswordEnabled:  true,
		OIDCProvider:     oidcProvider,
		OIDCProviderLogo: oidcProviderLogo,
		NextURI:          next,
	}

	if loginErr != nil {
		if e, ok := loginErr.(*echo.HTTPError); ok {
			out.Error = e.Message.(string)
		} else {
			out.Error = loginErr.Error()
		}
	}

	// Generate and set a nonce for preventing CSRF requests.
	nonce, err := utils.GenerateRandomString(16)
	if err != nil {
		app.log.Printf("error generating OIDC nonce: %v", err)
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.T("globals.messages.internalError"))
	}
	c.SetCookie(&http.Cookie{
		Name:     "nonce",
		Value:    nonce,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})
	out.Nonce = nonce

	return c.Render(http.StatusOK, "admin-login", out)
}

// doLogin logs a user in with a username and password.
func doLogin(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
	)

	// Verify that the request came from the login page (CSRF).
	// nonce, err := c.Cookie("nonce")
	// if err != nil || nonce.Value == "" || nonce.Value != c.FormValue("nonce") {
	// 	return echo.NewHTTPError(http.StatusUnauthorized, app.i18n.T("users.invalidRequest"))
	// }

	var (
		username = strings.TrimSpace(c.FormValue("username"))
		password = strings.TrimSpace(c.FormValue("password"))
	)

	if !strHasLen(username, 3, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "username"))
	}

	if !strHasLen(password, 8, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "password"))
	}

	start := time.Now()

	user, err := app.core.LoginUser(username, password)
	if err != nil {
		return err
	}

	// Resist potential constant-time-comparison attacks with a min response time.
	if ms := time.Now().Sub(start).Milliseconds(); ms < 100 {
		time.Sleep(time.Duration(ms))
	}

	// Set the session.
	if err := app.auth.SetSession(user, "", c); err != nil {
		return err
	}

	return nil
}
