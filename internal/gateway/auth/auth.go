package auth

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

type Authenticator struct {
	tokens map[string]bool
}

func New(tokenList []string) *Authenticator {
	tokens := make(map[string]bool)
	for _, t := range tokenList {
		tokens[t] = true
	}
	return &Authenticator{tokens: tokens}
}

// GenerateDefaultToken creates a cryptographically random token for agent auth
func GenerateDefaultToken() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *Authenticator) ValidateAgent(agentID, token string) bool {
	// Always require a valid token — no open-by-default
	return a.tokens[token]
}

func (a *Authenticator) ValidateMaster(r *http.Request, masterSecret string) bool {
	// Always require master secret — no open-by-default
	return r.Header.Get("X-Master-Secret") == masterSecret
}
