package jwt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNewManagerStableKeyID(t *testing.T) {
	privatePEM, publicPEM := rsaPEMs(t)

	first, err := NewManager(privatePEM, publicPEM)
	require.NoError(t, err)
	second, err := NewManager(privatePEM, publicPEM)
	require.NoError(t, err)

	require.Equal(t, managerKID(t, first), managerKID(t, second))
}

func TestSignedHeaderKidMatchesJWKS(t *testing.T) {
	manager, err := NewManager("", "")
	require.NoError(t, err)

	kid := managerKID(t, manager)
	userID := uuid.New()
	sessionID := uuid.New()
	fileID := uuid.New()

	accessToken, _, _, err := manager.IssueAccessToken(userID, sessionID)
	require.NoError(t, err)
	fileToken, _, _, err := manager.IssueFileAccessToken(userID, fileID)
	require.NoError(t, err)

	require.Equal(t, kid, tokenHeaderKID(t, accessToken))
	require.Equal(t, kid, tokenHeaderKID(t, fileToken))
}

func rsaPEMs(t *testing.T) (string, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	publicPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicDER,
	})

	return string(privatePEM), string(publicPEM)
}

func managerKID(t *testing.T, manager *Manager) string {
	t.Helper()

	keys, ok := manager.JWKS()["keys"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, keys, 1)
	kid, ok := keys[0]["kid"].(string)
	require.True(t, ok)
	require.NotEmpty(t, kid)
	return kid
}

func tokenHeaderKID(t *testing.T, tokenString string) string {
	t.Helper()

	token, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	require.NoError(t, err)
	kid, ok := token.Header["kid"].(string)
	require.True(t, ok)
	require.NotEmpty(t, kid)
	return kid
}
