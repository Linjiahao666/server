package blacklist

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "auth:bl:"

// Store manages revoked access token JTIs in Redis.
type Store struct {
	client *redis.Client
}

// NewStore creates a blacklist store.
func NewStore(client *redis.Client) *Store {
	return &Store{client: client}
}

// Revoke marks a JTI as revoked until the given expiry.
func (s *Store) Revoke(ctx context.Context, jti string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		ttl = time.Second
	}
	return s.client.Set(ctx, keyPrefix+jti, "1", ttl).Err()
}

// IsRevoked reports whether the JTI is blacklisted.
func (s *Store) IsRevoked(ctx context.Context, jti string) (bool, error) {
	result, err := s.client.Exists(ctx, keyPrefix+jti).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// Key returns the Redis key for a JTI, exposed for external verifiers.
func Key(jti string) string {
	return fmt.Sprintf("%s%s", keyPrefix, jti)
}
