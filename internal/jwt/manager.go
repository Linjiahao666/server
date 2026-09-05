package jwt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	AccessTokenTTL     = 15 * time.Minute
	RefreshTokenTTL    = 7 * 24 * time.Hour
	FileAccessTokenTTL = 5 * time.Minute
	FileAccessAudience = "file-access"
)

// AccessClaims are JWT claims for user access tokens.
type AccessClaims struct {
	jwt.RegisteredClaims
}

// FileAccessClaims are JWT claims for short-lived file download tokens.
type FileAccessClaims struct {
	FileID string `json:"file_id"`
	jwt.RegisteredClaims
}

// Manager signs and verifies RS256 access tokens.
type Manager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	keyID      string
}

// NewManager creates a JWT manager from PEM-encoded RSA keys.
func NewManager(privateKeyPEM, publicKeyPEM string) (*Manager, error) {
	if privateKeyPEM == "" || publicKeyPEM == "" {
		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		return &Manager{
			privateKey: privateKey,
			publicKey:  &privateKey.PublicKey,
			keyID:      uuid.NewString(),
		}, nil
	}

	privateKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	publicKey, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		return nil, err
	}

	return &Manager{
		privateKey: privateKey,
		publicKey:  publicKey,
		keyID:      uuid.NewString(),
	}, nil
}

// IssueAccessToken signs a new access token for the given user.
func (m *Manager) IssueAccessToken(userID uuid.UUID) (string, string, time.Time, error) {
	jti := uuid.NewString()
	now := time.Now().UTC()
	expiresAt := now.Add(AccessTokenTTL)

	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = m.keyID

	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", "", time.Time{}, err
	}

	return signed, jti, expiresAt, nil
}

// IssueFileAccessToken signs a short-lived token bound to a file.
func (m *Manager) IssueFileAccessToken(userID, fileID uuid.UUID) (string, string, time.Time, error) {
	jti := uuid.NewString()
	now := time.Now().UTC()
	expiresAt := now.Add(FileAccessTokenTTL)

	claims := FileAccessClaims{
		FileID: fileID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ID:        jti,
			Audience:  jwt.ClaimStrings{FileAccessAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = m.keyID

	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", "", time.Time{}, err
	}

	return signed, jti, expiresAt, nil
}

// ParseFileAccessToken validates and parses a file-access token.
func (m *Manager) ParseFileAccessToken(tokenString string) (*FileAccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &FileAccessClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}
		return m.publicKey, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*FileAccessClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	if !hasAudience(claims.Audience, FileAccessAudience) {
		return nil, errors.New("invalid token audience")
	}

	return claims, nil
}

// ParseAccessToken validates and parses an access token.
func (m *Manager) ParseAccessToken(tokenString string) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}
		return m.publicKey, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*AccessClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// JWKS returns the JSON Web Key Set for the public key.
func (m *Manager) JWKS() map[string]interface{} {
	return map[string]interface{}{
		"keys": []map[string]interface{}{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": m.keyID,
				"n":   base64.RawURLEncoding.EncodeToString(m.publicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.publicKey.E)).Bytes()),
			},
		},
	}
}

// MarshalJWKS serializes JWKS as JSON bytes.
func (m *Manager) MarshalJWKS() ([]byte, error) {
	return json.Marshal(m.JWKS())
}

func hasAudience(audiences jwt.ClaimStrings, expected string) bool {
	for _, audience := range audiences {
		if audience == expected {
			return true
		}
	}
	return false
}

func parsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("failed to decode private key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key is not RSA")
		}
		return rsaKey, nil
	}
	return key, nil
}

func parsePublicKey(pemData string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("failed to decode public key PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}
	return rsaKey, nil
}
