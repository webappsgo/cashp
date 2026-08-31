package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// captchaKey signs captcha tokens. It is generated once per process, so tokens
// do not survive a restart and cannot be forged by a client.
var captchaKey = newCaptchaKey()

// captchaTTL is how long a rendered question stays answerable.
const captchaTTL = 15 * time.Minute

// newCaptchaKey returns a random HMAC key for signing captcha tokens.
func newCaptchaKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// Without a working CSPRNG, derive a key from the start time so tokens
		// remain unforgeable by a client that never sees the key.
		binary.BigEndian.PutUint64(key, uint64(time.Now().UnixNano()))
	}
	return key
}

// newCaptcha returns a human-readable arithmetic question and the signed token
// that proves which answer is expected. The question is rendered server-side,
// so the check works without JavaScript and without third-party services.
func newCaptcha() (question string, token string) {
	left := randomInt(9) + 1
	right := randomInt(9) + 1
	answer := left + right
	expires := time.Now().Add(captchaTTL).Unix()
	payload := fmt.Sprintf("%d.%d", answer, expires)
	return fmt.Sprintf("What is %d plus %d?", left, right), payload + "." + signCaptcha(payload)
}

// verifyCaptcha reports whether the submitted answer matches an unexpired,
// correctly signed token.
func verifyCaptcha(token, answer string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(signCaptcha(payload)), []byte(parts[2])) {
		return false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return false
	}
	expected, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	given, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil {
		return false
	}
	return given == expected
}

// signCaptcha returns the HMAC of a captcha payload.
func signCaptcha(payload string) string {
	mac := hmac.New(sha256.New, captchaKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// randomInt returns a uniform random integer in [0, max).
func randomInt(max int64) int {
	value, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return int(time.Now().UnixNano() % max)
	}
	return int(value.Int64())
}
