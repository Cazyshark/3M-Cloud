package auth

import (
	"crypto/subtle"
	"errors"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// UserStore holds user credentials and TOTP secrets
type UserStore struct {
	users map[string]*User
	mu    sync.RWMutex
}

type User struct {
	Username   string
	Password   string // hashed
	TOTPSecret string
	Role       string // admin, viewer
	Enabled    bool
}

func NewUserStore() *UserStore {
	return &UserStore{
		users: make(map[string]*User),
	}
}

var (
	ErrInvalidPassword = errors.New("invalid password")
	ErrHashPassword    = errors.New("failed to hash password")
)

// AddUser creates a new user with bcrypt-hashed password
func (s *UserStore) AddUser(username, password, totpSecret, role string) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		// Fallback: store as-is (should never happen)
		hash = []byte(password)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[username] = &User{
		Username:   username,
		Password:   string(hash),
		TOTPSecret: totpSecret,
		Role:       role,
		Enabled:    true,
	}
}

func (s *UserStore) GetUser(username string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[username]
	if !ok {
		return nil, false
	}
	cp := *u
	return &cp, true
}

func (s *UserStore) ValidatePassword(username, password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[username]
	if !ok || !u.Enabled {
		return false
	}
	// Try bcrypt first
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err == nil {
		return true
	}
	// Fallback to plain-text comparison for backwards compatibility
	return subtle.ConstantTimeCompare([]byte(u.Password), []byte(password)) == 1
}

func (s *UserStore) ValidateTOTP(username, code string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[username]
	if !ok || !u.Enabled || u.TOTPSecret == "" {
		return false
	}
	return ValidateCode(u.TOTPSecret, code)
}

func (s *UserStore) ListUsers() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]User, 0, len(s.users))
	for _, u := range s.users {
		result = append(result, *u)
	}
	return result
}

func (s *UserStore) UpdateTOTPSecret(username, secret string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[username]; ok {
		u.TOTPSecret = secret
		return true
	}
	return false
}

func (s *UserStore) DeleteUser(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, username)
}
