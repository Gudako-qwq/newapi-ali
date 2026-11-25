package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,40}$`)
var identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,64}$`)

func validPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 15 && utf8.RuneCountInString(password) <= 128 && len(password) <= 512
}

func randomToken() string { return base64.RawURLEncoding.EncodeToString(randomBytes(32)) }

func randomBytes(size int) []byte {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic("system random source unavailable")
	}
	return value
}

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func hashPassword(password string) (string, error) {
	salt := randomBytes(16)
	digest := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("argon2id$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[1])
	wanted, hashErr := base64.RawStdEncoding.DecodeString(parts[2])
	if saltErr != nil || hashErr != nil || len(salt) != 16 || len(wanted) != 32 {
		return false
	}
	digest := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return subtle.ConstantTimeCompare(digest, wanted) == 1
}

type rateEntry struct {
	Count   int
	Expires time.Time
}
type rateLimiter struct {
	mutex   sync.Mutex
	entries map[string]rateEntry
}

func (limiter *rateLimiter) allow(key string, maximum int, period time.Duration) bool {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	now := time.Now()
	if limiter.entries == nil {
		limiter.entries = map[string]rateEntry{}
	}
	if len(limiter.entries) >= 10000 {
		for entryKey, entry := range limiter.entries {
			if now.After(entry.Expires) {
				delete(limiter.entries, entryKey)
			}
		}
		if _, exists := limiter.entries[key]; !exists && len(limiter.entries) >= 10000 {
			return false
		}
	}
	entry := limiter.entries[key]
	if now.After(entry.Expires) {
		entry = rateEntry{Expires: now.Add(period)}
	}
	entry.Count++
	limiter.entries[key] = entry
	return entry.Count <= maximum
}

func (app *App) session(request *http.Request) (User, string, bool) {
	cookie, err := request.Cookie("ali_session")
	if err != nil || len(cookie.Value) != 43 {
		return User{}, "", false
	}
	var user User
	var csrf string
	err = app.database.QueryRow(`SELECT u.id,u.username,u.role,u.enabled,s.csrf FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>? AND u.enabled=1`, tokenHash(cookie.Value), time.Now().Unix()).Scan(&user.ID, &user.Username, &user.Role, &user.Enabled, &csrf)
	return user, csrf, err == nil
}

func (app *App) login(writer http.ResponseWriter, request *http.Request) {
	host, _, _ := net.SplitHostPort(request.RemoteAddr)
	if !app.limiter.allow("login-ip:"+host, 20, 15*time.Minute) {
		fail(writer, 429, "登录过于频繁，请稍后再试")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if !usernamePattern.MatchString(input.Username) || len(input.Password) > 512 {
		fail(writer, 401, "账号或密码错误")
		return
	}
	if !app.limiter.allow("login-user:"+strings.ToLower(input.Username), 10, 15*time.Minute) {
		fail(writer, 429, "登录过于频繁，请稍后再试")
		return
	}
	select {
	case app.passwordSlots <- struct{}{}:
		defer func() { <-app.passwordSlots }()
	default:
		fail(writer, 429, "登录繁忙，请稍后再试")
		return
	}
	var user User
	var encoded string
	err := app.database.QueryRow("SELECT id,username,password_hash,role,enabled FROM users WHERE username=?", input.Username).Scan(&user.ID, &user.Username, &encoded, &user.Role, &user.Enabled)
	if err != nil {
		encoded = app.dummyHash
	}
	valid := verifyPassword(encoded, input.Password)
	input.Password = ""
	if err != nil || !valid || !user.Enabled {
		app.audit(0, "login", 0, "denied")
		fail(writer, 401, "账号或密码错误")
		return
	}
	token, csrf := randomToken(), randomToken()
	expires := time.Now().Add(12 * time.Hour)
	transaction, err := app.database.Begin()
	if err != nil {
		fail(writer, 500, "登录失败")
		return
	}
	defer transaction.Rollback()
	if cookie, cookieErr := request.Cookie("ali_session"); cookieErr == nil {
		_, err = transaction.Exec("DELETE FROM sessions WHERE token_hash=?", tokenHash(cookie.Value))
	}
	if err == nil {
		_, err = transaction.Exec("DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix())
	}
	if err == nil {
		_, err = transaction.Exec("INSERT INTO sessions(token_hash,csrf,user_id,expires_at) VALUES(?,?,?,?)", tokenHash(token), csrf, user.ID, expires.Unix())
	}
	if err != nil || transaction.Commit() != nil {
		fail(writer, 500, "登录失败")
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: "ali_session", Value: token, Path: "/", HttpOnly: true, Secure: app.config.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 43200, Expires: expires})
	app.audit(user.ID, "login", 0, "ok")
	respond(writer, 200, map[string]any{"user": user, "csrf_token": csrf})
}

func (app *App) logout(writer http.ResponseWriter, request *http.Request, user User) {
	cookie, err := request.Cookie("ali_session")
	if err == nil {
		_, err = app.database.Exec("DELETE FROM sessions WHERE token_hash=?", tokenHash(cookie.Value))
	}
	if err != nil {
		fail(writer, 500, "退出失败，请重试")
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: "ali_session", Value: "", Path: "/", HttpOnly: true, Secure: app.config.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	app.audit(user.ID, "logout", 0, "ok")
	respond(writer, 200, nil)
}
