package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Linjiahao666/server/internal/httpx"
)

// Handler exposes auth HTTP endpoints.
type Handler struct {
	service *Service
}

// NewHandler creates an auth handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Register handles POST /v1/auth/register.
//
//	@Summary		Register a user
//	@Description	Create a new user account. Limited by client IP.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		registerRequest	true	"username and password"
//	@Success		201		{object}	UserView
//	@Failure		409		{object}	httpx.APIError
//	@Failure		422		{object}	httpx.APIError
//	@Failure		429		{object}	httpx.APIError
//	@Router			/v1/auth/register [post]
func (h *Handler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, httpx.StatusUnprocessableEntity, "VALIDATION_FAILED", "request body is invalid")
		return
	}

	user, err := h.service.Register(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameTaken):
			httpx.WriteError(c, httpx.StatusConflict, "AUTH_USERNAME_TAKEN", "username already exists")
		case errors.Is(err, ErrValidationFailed):
			httpx.WriteError(c, httpx.StatusUnprocessableEntity, "VALIDATION_FAILED", "username must be 3-64 characters and password at least 8 characters")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to register user")
		}
		return
	}

	httpx.WriteJSON(c, httpx.StatusCreated, user)
}

// Login handles POST /v1/auth/login.
//
//	@Summary		Login
//	@Description	Issue access and refresh tokens. Limited by client IP.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		registerRequest	true	"username and password"
//	@Success		200		{object}	TokenPair
//	@Failure		401		{object}	httpx.APIError
//	@Failure		429		{object}	httpx.APIError
//	@Router			/v1/auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, httpx.StatusUnprocessableEntity, "VALIDATION_FAILED", "request body is invalid")
		return
	}

	tokens, err := h.service.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "username or password is incorrect")
			return
		}
		httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to login")
		return
	}

	httpx.WriteJSON(c, http.StatusOK, tokens)
}

// Refresh handles POST /v1/auth/refresh.
//
//	@Summary		Refresh access token
//	@Description	Issue a new access token for an existing session. Limited by client IP.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		refreshRequest	true	"refresh token"
//	@Success		200		{object}	TokenPair
//	@Failure		401		{object}	httpx.APIError
//	@Failure		429		{object}	httpx.APIError
//	@Router			/v1/auth/refresh [post]
func (h *Handler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RefreshToken == "" {
		httpx.WriteError(c, httpx.StatusUnprocessableEntity, "VALIDATION_FAILED", "refresh_token is required")
		return
	}

	tokens, err := h.service.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrInvalidRefreshToken) {
			httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "refresh token is invalid or expired")
			return
		}
		httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to refresh token")
		return
	}

	httpx.WriteJSON(c, http.StatusOK, tokens)
}

// Logout handles POST /v1/auth/logout.
//
//	@Summary		Logout current session
//	@Description	Blacklist the access jti and delete the session bound to sid. refresh_token in the body is optional.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body	logoutRequest	false	"optional refresh token"
//	@Success		204
//	@Failure		401	{object}	httpx.APIError
//	@Router			/v1/auth/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	claims, ok := accessClaimsFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	var req logoutRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.service.Logout(c.Request.Context(), claims, req.RefreshToken); err != nil {
		httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to logout")
		return
	}

	httpx.NoContent(c, httpx.StatusNoContent)
}

// LogoutAll handles POST /v1/auth/logout-all.
//
//	@Summary		Logout all sessions
//	@Description	Delete every session for the user and blacklist the current access jti.
//	@Tags			auth
//	@Produce		json
//	@Security		BearerAuth
//	@Success		204
//	@Failure		401	{object}	httpx.APIError
//	@Router			/v1/auth/logout-all [post]
func (h *Handler) LogoutAll(c *gin.Context) {
	claims, ok := accessClaimsFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	if err := h.service.LogoutAll(c.Request.Context(), claims); err != nil {
		httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to logout")
		return
	}

	httpx.NoContent(c, httpx.StatusNoContent)
}

// Me handles GET /v1/auth/me.
//
//	@Summary		Current user
//	@Description	Return the authenticated user profile.
//	@Tags			auth
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	UserView
//	@Failure		401	{object}	httpx.APIError
//	@Router			/v1/auth/me [get]
func (h *Handler) Me(c *gin.Context) {
	userID, ok := userIDFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	user, err := h.service.GetUser(c.Request.Context(), userID)
	if err != nil {
		httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load user")
		return
	}

	httpx.WriteJSON(c, http.StatusOK, user)
}

// JWKS handles GET /v1/auth/.well-known/jwks.json.
//
//	@Summary		JSON Web Key Set
//	@Description	Public keys for local RS256 verification. Callers then check auth:bl:{jti} in Redis.
//	@Tags			auth
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/v1/auth/.well-known/jwks.json [get]
func (h *Handler) JWKS(c *gin.Context) {
	httpx.WriteJSON(c, http.StatusOK, h.service.JWKS())
}

// AccessContext stores validated access token data in the request context.
type AccessContext struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	JTI       string
	ExpiresAt time.Time
}

type contextKey string

const accessContextKey contextKey = "access_context"

func accessClaimsFromContext(c *gin.Context) (AccessContext, bool) {
	value, exists := c.Get(string(accessContextKey))
	if !exists {
		return AccessContext{}, false
	}
	claims, ok := value.(AccessContext)
	return claims, ok
}

func userIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	return UserIDFromContext(c)
}

// UserIDFromContext returns the authenticated user ID from the request context.
func UserIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	claims, ok := accessClaimsFromContext(c)
	if !ok {
		return uuid.Nil, false
	}
	return claims.UserID, true
}

// RequireAuth is middleware that validates bearer access tokens.
func (h *Handler) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		if token == "" {
			httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
			return
		}

		claims, err := h.service.ValidateAccessToken(c.Request.Context(), token)
		if err != nil {
			httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
			return
		}

		c.Set(string(accessContextKey), claims)
		c.Next()
	}
}

func bearerToken(header string) string {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
