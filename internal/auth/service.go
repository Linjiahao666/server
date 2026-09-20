package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Linjiahao666/server/internal/blacklist"
	jwtmanager "github.com/Linjiahao666/server/internal/jwt"
	"github.com/Linjiahao666/server/internal/password"
	"github.com/Linjiahao666/server/internal/repository"
)

// Service handles authentication workflows.
type Service struct {
	users      *repository.UserRepository
	sessions   *repository.SessionRepository
	jwt        *jwtmanager.Manager
	blacklist  *blacklist.Store
	refreshTTL time.Duration
}

// NewService creates an auth service.
func NewService(
	users *repository.UserRepository,
	sessions *repository.SessionRepository,
	jwt *jwtmanager.Manager,
	blacklist *blacklist.Store,
) *Service {
	return &Service{
		users:      users,
		sessions:   sessions,
		jwt:        jwt,
		blacklist:  blacklist,
		refreshTTL: jwtmanager.RefreshTokenTTL,
	}
}

// UserView is the public user representation.
type UserView struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// TokenPair is returned on login and refresh.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// Register creates a new user account.
func (s *Service) Register(ctx context.Context, username, plainPassword string) (UserView, error) {
	if err := validateCredentials(username, plainPassword); err != nil {
		return UserView{}, err
	}

	hash, err := password.Hash(plainPassword)
	if err != nil {
		return UserView{}, err
	}

	user, err := s.users.Create(ctx, username, hash)
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return UserView{}, ErrUsernameTaken
		}
		return UserView{}, err
	}

	return toUserView(user), nil
}

// Login authenticates a user and issues tokens.
func (s *Service) Login(ctx context.Context, username, plainPassword string) (TokenPair, error) {
	user, err := s.users.FindByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return TokenPair{}, ErrInvalidCredentials
		}
		return TokenPair{}, err
	}

	ok, err := password.Verify(user.PasswordHash, plainPassword)
	if err != nil || !ok {
		return TokenPair{}, ErrInvalidCredentials
	}

	return s.issueTokens(ctx, user.ID)
}

// Refresh exchanges a refresh token for a new access token.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	hash := hashRefreshToken(refreshToken)
	session, err := s.sessions.FindByRefreshTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return TokenPair{}, ErrInvalidRefreshToken
		}
		return TokenPair{}, err
	}

	if time.Now().UTC().After(session.ExpiresAt) {
		_ = s.sessions.Delete(ctx, session.ID)
		return TokenPair{}, ErrInvalidRefreshToken
	}

	accessToken, _, _, err := s.jwt.IssueAccessToken(session.UserID, session.ID)
	if err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(jwtmanager.AccessTokenTTL.Seconds()),
	}, nil
}

// Logout revokes the access token JTI and deletes the refresh session.
func (s *Service) Logout(ctx context.Context, claims AccessContext, refreshToken string) error {
	if err := s.blacklist.Revoke(ctx, claims.JTI, claims.ExpiresAt); err != nil {
		return err
	}

	if err := s.sessions.DeleteOwned(ctx, claims.SessionID, claims.UserID); err != nil {
		return err
	}

	if refreshToken != "" {
		hash := hashRefreshToken(refreshToken)
		session, err := s.sessions.FindByRefreshTokenHash(ctx, hash)
		if err == nil && session.UserID == claims.UserID {
			_ = s.sessions.Delete(ctx, session.ID)
		}
	}

	return nil
}

// LogoutAll revokes the current access token JTI and deletes every session for the user.
func (s *Service) LogoutAll(ctx context.Context, claims AccessContext) error {
	if err := s.blacklist.Revoke(ctx, claims.JTI, claims.ExpiresAt); err != nil {
		return err
	}
	return s.sessions.DeleteByUserID(ctx, claims.UserID)
}

// GetUser returns a user by ID.
func (s *Service) GetUser(ctx context.Context, userID uuid.UUID) (UserView, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	return toUserView(user), nil
}

// ValidateAccessToken parses and checks whether an access token is valid.
func (s *Service) ValidateAccessToken(ctx context.Context, tokenString string) (AccessContext, error) {
	claims, err := s.jwt.ParseAccessToken(tokenString)
	if err != nil {
		return AccessContext{}, ErrInvalidToken
	}

	revoked, err := s.blacklist.IsRevoked(ctx, claims.ID)
	if err != nil {
		return AccessContext{}, err
	}
	if revoked {
		return AccessContext{}, ErrInvalidToken
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return AccessContext{}, ErrInvalidToken
	}

	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return AccessContext{}, ErrInvalidToken
	}

	return AccessContext{
		UserID:    userID,
		SessionID: sessionID,
		JTI:       claims.ID,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

// JWKS returns the JSON Web Key Set.
func (s *Service) JWKS() map[string]interface{} {
	return s.jwt.JWKS()
}

func (s *Service) issueTokens(ctx context.Context, userID uuid.UUID) (TokenPair, error) {
	refreshToken, err := newRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}

	expiresAt := time.Now().UTC().Add(s.refreshTTL)
	session, err := s.sessions.Create(ctx, userID, hashRefreshToken(refreshToken), expiresAt)
	if err != nil {
		return TokenPair{}, err
	}

	accessToken, _, _, err := s.jwt.IssueAccessToken(userID, session.ID)
	if err != nil {
		_ = s.sessions.Delete(ctx, session.ID)
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(jwtmanager.AccessTokenTTL.Seconds()),
	}, nil
}

func validateCredentials(username, plainPassword string) error {
	length := utf8.RuneCountInString(username)
	if length < 3 || length > 64 {
		return ErrValidationFailed
	}
	if len(plainPassword) < 8 {
		return ErrValidationFailed
	}
	return nil
}

func toUserView(user repository.User) UserView {
	return UserView{
		ID:        user.ID,
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
	}
}

func newRefreshToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ErrUsernameTaken indicates the username already exists.
var ErrUsernameTaken = errors.New("username taken")

// ErrInvalidCredentials indicates login failed.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrInvalidRefreshToken indicates the refresh token is invalid or expired.
var ErrInvalidRefreshToken = errors.New("invalid refresh token")

// ErrInvalidToken indicates the access token is invalid or revoked.
var ErrInvalidToken = errors.New("invalid token")

// ErrValidationFailed indicates request validation failed.
var ErrValidationFailed = errors.New("validation failed")
