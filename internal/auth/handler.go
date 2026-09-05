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
func (h *Handler) Logout(c *gin.Context) {
	claims, ok := accessClaimsFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	var req logoutRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.service.Logout(c.Request.Context(), claims.UserID, claims.JTI, claims.ExpiresAt, req.RefreshToken); err != nil {
		httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to logout")
		return
	}

	httpx.NoContent(c, httpx.StatusNoContent)
}

// Me handles GET /v1/auth/me.
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
func (h *Handler) JWKS(c *gin.Context) {
	httpx.WriteJSON(c, http.StatusOK, h.service.JWKS())
}

// AccessContext stores validated access token data in the request context.
type AccessContext struct {
	UserID    uuid.UUID
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

		userID, jti, expiresAt, err := h.service.ValidateAccessToken(c.Request.Context(), token)
		if err != nil {
			httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
			return
		}

		c.Set(string(accessContextKey), AccessContext{
			UserID:    userID,
			JTI:       jti,
			ExpiresAt: expiresAt,
		})
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
