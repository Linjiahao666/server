package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User represents a registered account.
type User struct {
	ID           uuid.UUID
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Session represents a refresh token session.
type Session struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	RefreshTokenHash string
	ExpiresAt        time.Time
	CreatedAt        time.Time
}

// UserRepository persists users.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository creates a user repository.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Create inserts a new user.
func (r *UserRepository) Create(ctx context.Context, username, passwordHash string) (User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING id, username, password_hash, created_at, updated_at
	`, username, passwordHash)

	var user User
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return User{}, err
	}
	return user, nil
}

// FindByUsername loads a user by username.
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, created_at, updated_at
		FROM users
		WHERE username = $1
	`, username)

	var user User
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return user, nil
}

// FindByID loads a user by ID.
func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, created_at, updated_at
		FROM users
		WHERE id = $1
	`, id)

	var user User
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return user, nil
}

// IsUniqueViolation reports whether an error is a unique constraint violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// SessionRepository persists refresh sessions.
type SessionRepository struct {
	pool *pgxpool.Pool
}

// NewSessionRepository creates a session repository.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create inserts a new session.
func (r *SessionRepository) Create(ctx context.Context, userID uuid.UUID, refreshTokenHash string, expiresAt time.Time) (Session, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, refresh_token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, refresh_token_hash, expires_at, created_at
	`, userID, refreshTokenHash, expiresAt)

	var session Session
	if err := row.Scan(&session.ID, &session.UserID, &session.RefreshTokenHash, &session.ExpiresAt, &session.CreatedAt); err != nil {
		return Session{}, err
	}
	return session, nil
}

// FindByRefreshTokenHash loads a session by refresh token hash.
func (r *SessionRepository) FindByRefreshTokenHash(ctx context.Context, refreshTokenHash string) (Session, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, refresh_token_hash, expires_at, created_at
		FROM sessions
		WHERE refresh_token_hash = $1
	`, refreshTokenHash)

	var session Session
	if err := row.Scan(&session.ID, &session.UserID, &session.RefreshTokenHash, &session.ExpiresAt, &session.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return session, nil
}

// Delete removes a session by ID.
func (r *SessionRepository) Delete(ctx context.Context, sessionID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	return err
}

// ErrNotFound indicates the requested row does not exist.
var ErrNotFound = errors.New("not found")
