package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultAdminUsername = "admin"
	defaultAdminPassword = "admin"
	passwordSaltBytes    = 16
	passwordHashBytes    = 32
	passwordIterations   = 210_000
)

// EnsureAdmin creates the first local administrator exactly once. The default
// password is intentionally only used during first-run initialization; it is
// never stored or returned by the API.
func (store *Store) EnsureAdmin() error {
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ea_auth_users WHERE username=?`, defaultAdminUsername).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return store.createUser(defaultAdminUsername, defaultAdminPassword, time.Now())
}

func (store *Store) createUser(username, password string, now time.Time) error {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("生成密码盐失败: %w", err)
	}
	hashValue := derivePassword(password, salt, passwordIterations)
	_, err := store.db.Exec(`INSERT INTO ea_auth_users(username,password_salt,password_hash,password_iterations,created_at,updated_at) VALUES(?,?,?,?,?,?)`, username, salt, hashValue, passwordIterations, formatTime(now), formatTime(now))
	return err
}

// Authenticate checks the only local account used by the single-user UI.
func (store *Store) Authenticate(username, password string) (bool, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return false, nil
	}
	var salt, expected []byte
	var iterations int
	err := store.db.QueryRow(`SELECT password_salt,password_hash,password_iterations FROM ea_auth_users WHERE username=?`, username).Scan(&salt, &expected, &iterations)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if iterations <= 0 || len(salt) == 0 || len(expected) == 0 {
		return false, nil
	}
	actual := derivePassword(password, salt, iterations)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func (store *Store) ChangePassword(username, currentPassword, newPassword string) error {
	valid, err := store.Authenticate(username, currentPassword)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("当前密码不正确")
	}
	if len(newPassword) < 8 {
		return errors.New("新密码至少需要 8 个字符")
	}
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("生成密码盐失败: %w", err)
	}
	hashValue := derivePassword(newPassword, salt, passwordIterations)
	result, err := store.db.Exec(`UPDATE ea_auth_users SET password_salt=?,password_hash=?,password_iterations=?,updated_at=? WHERE username=?`, salt, hashValue, passwordIterations, formatTime(time.Now()), username)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("管理员账号不存在")
	}
	return nil
}

func derivePassword(password string, salt []byte, iterations int) []byte {
	// PBKDF2-HMAC-SHA256 is kept dependency-free so the embedded server can be
	// built with the existing Go module and still resist cheap offline guesses.
	return pbkdf2SHA256([]byte(password), salt, iterations, passwordHashBytes)
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLength int) []byte {
	result := make([]byte, 0, keyLength)
	for block := 1; len(result) < keyLength; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for index := 1; index < iterations; index++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for offset := range t {
				t[offset] ^= u[offset]
			}
		}
		result = append(result, t...)
	}
	return result[:keyLength]
}
