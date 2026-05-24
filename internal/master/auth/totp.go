package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const (
	totpPeriod    = 30
	totpDigits    = 6
	totpSkew     = 1 // allow ±1 period window
)

// GenerateSecret creates a new TOTP secret and returns secret + otpauth URL
func GenerateSecret(issuer, account string) (string, string) {
	secret := make([]byte, 20)
	rand.Read(secret)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)

	url := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		issuer, account, encoded, issuer, totpDigits, totpPeriod)

	return encoded, url
}

// ValidateCode checks if a TOTP code is valid for the given secret
func ValidateCode(secret, code string) bool {
	if len(code) != totpDigits {
		return false
	}

	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false
	}

	now := time.Now().Unix()
	for i := -int64(totpSkew); i <= int64(totpSkew); i++ {
		expected := generateCode(decoded, now+int64(totpPeriod)*i)
		if expected == code {
			return true
		}
	}
	return false
}

func generateCode(secret []byte, timestamp int64) string {
	counter := uint64(timestamp) / uint64(totpPeriod)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%06d", truncated%1000000)
}
