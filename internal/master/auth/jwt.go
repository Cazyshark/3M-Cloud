package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"` // admin, viewer
	Exp      int64  `json:"exp"`
	Iat      int64  `json:"iat"`
}

type JWTManager struct {
	secret []byte
	ttl    time.Duration
}

func NewJWTManager(secret string, ttl time.Duration) *JWTManager {
	return &JWTManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

func (j *JWTManager) GenerateToken(username, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		Username: username,
		Role:     role,
		Exp:      now.Add(j.ttl).Unix(),
		Iat:      now.Unix(),
	}

	header := base64urlEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadEncoded := base64urlEncode(payload)

	signingInput := header + "." + payloadEncoded
	sig := j.sign([]byte(signingInput))

	return signingInput + "." + base64urlEncode(sig), nil
}

func (j *JWTManager) ValidateToken(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	sig := j.sign([]byte(parts[0] + "." + parts[1]))
	if base64urlEncode(sig) != parts[2] {
		return nil, fmt.Errorf("invalid signature")
	}

	payload, err := base64urlDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid claims: %w", err)
	}

	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

func (j *JWTManager) sign(data []byte) []byte {
	mac := hmac.New(sha256.New, j.secret)
	mac.Write(data)
	return mac.Sum(nil)
}

func base64urlEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func base64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
