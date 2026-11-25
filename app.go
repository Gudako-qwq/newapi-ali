package main

import (
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed web/dist
var frontend embed.FS

var projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]$`)

type App struct {
	config        Config
	database      *sql.DB
	upstream      *Upstream
	logger        *log.Logger
	limiter       rateLimiter
	passwordSlots chan struct{}
	upstreamSlots chan struct{}
	dummyHash     string
}

type authenticatedHandler func(http.ResponseWriter, *http.Request, User)
type channelHandler func(http.ResponseWriter, *http.Request, User, Channel)

func newApp(config Config, database *sql.DB, logger *log.Logger) *App {
	dummyHash, _ := hashPassword(randomToken())
	return &App{config: config, database: database, upstream: newUpstream(config), logger: logger, passwordSlots: make(chan struct{}, 2), upstreamSlots: make(chan struct{}, 4), dummyHash: dummyHash}
}

func (app *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		respond(writer, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/login", app.login)
	mux.HandleFunc("GET /api/session", app.protect(false, func(writer http.ResponseWriter, request *http.Request, user User) {
		_, csrf, _ := app.session(request)
		respond(writer, 200, map[string]any{"user": user, "csrf_token": csrf})
	}))
	mux.HandleFunc("POST /api/logout", app.protect(false, app.logout))
	mux.HandleFunc("POST /api/password", app.protect(false, app.changePassword))
	mux.HandleFunc("GET /api/templates", app.protect(false, app.catalog))
	mux.HandleFunc("GET /api/channels", app.protect(false, app.listChannels))
	mux.HandleFunc("POST /api/channels", app.protect(false, app.withUpstreamSlot(app.createChannel)))
	mux.HandleFunc("POST /api/channels/{id}/archive", app.protect(false, app.withChannel(app.archiveRejectedChannel)))
	mux.HandleFunc("POST /api/channels/{id}/sync", app.protect(false, app.withUpstreamChannel(app.syncChannel)))
	mux.HandleFunc("PUT /api/channels/{id}", app.protect(false, app.withUpstreamChannel(app.editChannel)))
	mux.HandleFunc("POST /api/channels/{id}/lifecycle", app.protect(false, app.withUpstreamChannel(app.channelAction)))
	mux.HandleFunc("GET /api/channels/{id}/logs", app.protect(false, app.withUpstreamChannel(app.channelLogs)))
	mux.HandleFunc("GET /api/admin/users", app.protect(true, app.listUsers))
	mux.HandleFunc("POST /api/admin/users", app.protect(true, app.createUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", app.protect(true, app.updateUser))
	mux.HandleFunc("POST /api/admin/bind", app.protect(true, app.withUpstreamSlot(app.bindChannel)))
	mux.HandleFunc("POST /api/admin/channels/{id}/reconcile", app.protect(true, app.withUpstreamChannel(app.reconcileChannel)))
	mux.HandleFunc("GET /api/admin/audit", app.protect(true, app.listAudit))
	mux.HandleFunc("GET /api/admin/connection", app.protect(true, app.withUpstreamSlot(func(writer http.ResponseWriter, request *http.Request, user User) {
		var profile struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if err := app.upstream.call(request.Context(), http.MethodGet, "/api/user/self", nil, &profile); err != nil {
			fail(writer, 503, "连接失败：请检查服务端凭据、登录策略或网络")
			return
		}
		respond(writer, 200, map[string]bool{"channel_read": profile.Capabilities["channel.read"], "channel_write": profile.Capabilities["channel.write"], "usage_read": profile.Capabilities["usage.read"]})
	})))
	mux.HandleFunc("/api/", func(writer http.ResponseWriter, request *http.Request) { fail(writer, 404, "接口不存在") })
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(writer, request)
			return
		}
		path := strings.TrimPrefix(request.URL.Path, "/")
		switch request.URL.Path {
		case "/", "/sign-in", "/channels", "/supplier/logs", "/customers", "/audit", "/security":
			path = "index.html"
		case "/favicon.ico", "/logo.png", "/LICENSE", "/NOTICE", "/THIRD-PARTY-LICENSES.md":
		default:
			if !strings.HasPrefix(path, "static/") || !fs.ValidPath(path) {
				http.NotFound(writer, request)
				return
			}
		}
		assets, _ := fs.Sub(frontend, "web/dist")
		info, err := fs.Stat(assets, path)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(writer, request)
			return
		}
		if strings.HasPrefix(path, "static/") {
			writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(writer, request, assets, path)
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'")
		if app.config.SecureCookies {
			writer.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if request.Method != "GET" && request.Method != "HEAD" {
			mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
			if request.Header.Get("Origin") != app.config.Origin || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(writer, 403, "请求来源不被允许")
				return
			}
			if err != nil || mediaType != "application/json" {
				fail(writer, 415, "只接受 JSON 请求")
				return
			}
		}
		defer func() {
			if recover() != nil {
				app.logger.Print("request failed unexpectedly")
				fail(writer, 500, "服务暂不可用")
			}
		}()
		mux.ServeHTTP(writer, request)
	})
}

func (app *App) protect(admin bool, next authenticatedHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		user, csrf, valid := app.session(request)
		if !valid {
			fail(writer, 401, "请先登录")
			return
		}
		if admin && user.Role != "admin" {
			fail(writer, 403, "没有操作权限")
			return
		}
		limit := 120
		bucket := "read:"
		if request.Method != "GET" && request.Method != "HEAD" {
			limit = 30
			bucket = "write:"
			if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
				fail(writer, 403, "安全校验失败，请刷新页面")
				return
			}
		}
		if !app.limiter.allow(bucket+strconv.FormatInt(user.ID, 10), limit, time.Minute) {
			fail(writer, 429, "操作过于频繁，请稍后再试")
			return
		}
		next(writer, request, user)
	}
}

func (app *App) withUpstreamSlot(next authenticatedHandler) authenticatedHandler {
	return func(writer http.ResponseWriter, request *http.Request, user User) {
		select {
		case app.upstreamSlots <- struct{}{}:
			defer func() { <-app.upstreamSlots }()
		default:
			fail(writer, 429, "渠道服务繁忙，请稍后重试")
			return
		}
		next(writer, request, user)
	}
}

func (app *App) withChannel(next channelHandler) authenticatedHandler {
	return func(writer http.ResponseWriter, request *http.Request, user User) {
		id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(writer, 404, "渠道不存在")
			return
		}
		channel, err := app.ownedChannel(user, id)
		if err != nil {
			fail(writer, 404, "渠道不存在")
			return
		}
		next(writer, request, user, channel)
	}
}

func (app *App) withUpstreamChannel(next channelHandler) authenticatedHandler {
	return app.withChannel(func(writer http.ResponseWriter, request *http.Request, user User, channel Channel) {
		if channel.State == "archived" {
			fail(writer, http.StatusConflict, "这条失败记录已在本地归档，不能操作上游")
			return
		}
		app.withUpstreamSlot(func(writer http.ResponseWriter, request *http.Request, user User) {
			next(writer, request, user, channel)
		})(writer, request, user)
	})
}

func readJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 192*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(writer, 400, "请求格式不正确、包含不允许的字段或内容过大")
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		fail(writer, 400, "只接受单个 JSON 对象")
		return false
	}
	return true
}

func respond(writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(map[string]any{"success": true, "data": data})
}

func fail(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(map[string]any{"success": false, "message": message})
}

func (app *App) changePassword(writer http.ResponseWriter, request *http.Request, user User) {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if !validPassword(input.NewPassword) || len(input.CurrentPassword) > 512 {
		fail(writer, 400, "新密码须为 15–128 字符")
		return
	}
	if !app.limiter.allow("password:"+strconv.FormatInt(user.ID, 10), 5, 15*time.Minute) {
		fail(writer, 429, "操作过于频繁")
		return
	}
	select {
	case app.passwordSlots <- struct{}{}:
		defer func() { <-app.passwordSlots }()
	default:
		fail(writer, 429, "服务繁忙")
		return
	}
	var encoded string
	if app.database.QueryRow("SELECT password_hash FROM users WHERE id=?", user.ID).Scan(&encoded) != nil || !verifyPassword(encoded, input.CurrentPassword) {
		fail(writer, 403, "当前密码不正确")
		return
	}
	nextHash, _ := hashPassword(input.NewPassword)
	transaction, err := app.database.Begin()
	if err != nil {
		fail(writer, 500, "修改失败")
		return
	}
	defer transaction.Rollback()
	_, err = transaction.Exec("UPDATE users SET password_hash=? WHERE id=?", nextHash, user.ID)
	if err == nil {
		_, err = transaction.Exec("DELETE FROM local_bootstrap_passwords WHERE user_id=?", user.ID)
	}
	if err == nil {
		_, err = transaction.Exec("DELETE FROM sessions WHERE user_id=?", user.ID)
	}
	if err != nil || transaction.Commit() != nil {
		fail(writer, 500, "修改失败")
		return
	}
	app.audit(user.ID, "password.change", 0, "ok")
	http.SetCookie(writer, &http.Cookie{Name: "ali_session", Value: "", Path: "/", HttpOnly: true, Secure: app.config.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	respond(writer, 200, nil)
}

func (app *App) listUsers(writer http.ResponseWriter, request *http.Request, user User) {
	rows, err := app.database.Query("SELECT id,username,role,enabled FROM users ORDER BY id DESC LIMIT 1000")
	if err != nil {
		fail(writer, 500, "查询失败")
		return
	}
	defer rows.Close()
	items := []User{}
	for rows.Next() {
		var item User
		if rows.Scan(&item.ID, &item.Username, &item.Role, &item.Enabled) != nil {
			fail(writer, 500, "查询失败")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		fail(writer, 500, "查询失败")
		return
	}
	respond(writer, 200, items)
}

func (app *App) createUser(writer http.ResponseWriter, request *http.Request, user User) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if !usernamePattern.MatchString(input.Username) || !validPassword(input.Password) {
		fail(writer, 400, "账号须为 3–40 位字母、数字、下划线或连字符；密码须为 15–128 字符")
		return
	}
	select {
	case app.passwordSlots <- struct{}{}:
		defer func() { <-app.passwordSlots }()
	default:
		fail(writer, 429, "服务繁忙")
		return
	}
	hash, _ := hashPassword(input.Password)
	result, err := app.database.Exec("INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,'user',?)", input.Username, hash, time.Now().Unix())
	if err != nil {
		fail(writer, 409, "账号已存在或暂时无法创建")
		return
	}
	id, _ := result.LastInsertId()
	app.audit(user.ID, "user.create", 0, "ok")
	respond(writer, 201, User{ID: id, Username: input.Username, Role: "user", Enabled: true})
}

func (app *App) updateUser(writer http.ResponseWriter, request *http.Request, user User) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || id == user.ID {
		fail(writer, 400, "不能操作此账号")
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if input.Enabled == nil {
		fail(writer, 400, "请指定账号状态")
		return
	}
	transaction, err := app.database.Begin()
	if err != nil {
		fail(writer, 500, "操作失败")
		return
	}
	defer transaction.Rollback()
	result, err := transaction.Exec("UPDATE users SET enabled=? WHERE id=? AND role='user'", *input.Enabled, id)
	if err != nil {
		fail(writer, 500, "操作失败")
		return
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		fail(writer, 404, "客户不存在")
		return
	}
	_, err = transaction.Exec("DELETE FROM sessions WHERE user_id=?", id)
	if err != nil || transaction.Commit() != nil {
		fail(writer, 500, "操作失败")
		return
	}
	app.audit(user.ID, "user.status", 0, "ok")
	respond(writer, 200, nil)
}

func (app *App) bindChannel(writer http.ResponseWriter, request *http.Request, user User) {
	var input struct {
		UserID     int64  `json:"user_id"`
		UpstreamID int64  `json:"upstream_id"`
		TemplateID string `json:"template_id"`
		Name       string `json:"name"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	template, exists := app.template(input.TemplateID)
	if !exists || input.UserID <= 0 || input.UpstreamID <= 0 || len(strings.TrimSpace(input.Name)) == 0 || len(input.Name) > 240 {
		fail(writer, 400, "绑定参数不正确")
		return
	}
	var enabled bool
	if app.database.QueryRow("SELECT enabled FROM users WHERE id=?", input.UserID).Scan(&enabled) != nil || !enabled {
		fail(writer, 400, "客户不存在或已停用")
		return
	}
	var upstream upstreamChannel
	if app.upstream.call(request.Context(), http.MethodGet, "/api/channel/"+strconv.FormatInt(input.UpstreamID, 10), nil, &upstream) != nil || upstream.ID != input.UpstreamID || upstream.Type != template.Type {
		fail(writer, 400, "无法验证渠道或类型不匹配")
		return
	}
	result, err := app.database.Exec(`INSERT INTO channels(user_id,name,template_id,models,upstream_id,upstream_name,request_key,request_hash,state,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'','ready',?,?,?)`, input.UserID, strings.TrimSpace(input.Name), input.TemplateID, upstream.Models, upstream.ID, upstream.Name, randomToken(), upstream.Status, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		fail(writer, 409, "此渠道已被绑定，或记录无法保存")
		return
	}
	id, _ := result.LastInsertId()
	app.audit(user.ID, "channel.bind", id, "ok")
	respond(writer, 201, map[string]int64{"id": id})
}

func (app *App) listAudit(writer http.ResponseWriter, request *http.Request, user User) {
	rows, err := app.database.Query("SELECT id,user_id,action,channel_id,outcome,created_at FROM audit ORDER BY id DESC LIMIT 100")
	if err != nil {
		fail(writer, 500, "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, userID, channelID, createdAt int64
		var action, outcome string
		if rows.Scan(&id, &userID, &action, &channelID, &outcome, &createdAt) != nil {
			fail(writer, 500, "查询失败")
			return
		}
		items = append(items, map[string]any{"id": id, "user_id": userID, "action": action, "channel_id": channelID, "outcome": outcome, "created_at": createdAt})
	}
	if rows.Err() != nil {
		fail(writer, 500, "查询失败")
		return
	}
	respond(writer, 200, items)
}
