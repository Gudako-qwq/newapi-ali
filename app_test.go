package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakePool struct {
	mutex       sync.Mutex
	requests    int
	creates     int
	logins      int
	channel     upstreamChannel
	payload     map[string]any
	lastQuery   string
	createMode  string
	editMode    string
	editPayload map[string]any
	mixedLogs   bool
	expireOnce  bool
}

func (pool *fakePool) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	pool.requests++
	switch {
	case request.URL.Path == "/api/status":
		respond(writer, 200, map[string]any{"password_transport": map[string]any{"mode": "disabled"}, "turnstile_check": false})
	case request.URL.Path == "/api/user/login":
		pool.logins++
		http.SetCookie(writer, &http.Cookie{Name: "new_api_session", Value: "upstream-session-secret", Path: "/"})
		respond(writer, 200, map[string]any{"user": map[string]int{"id": 77}, "session": map[string]string{"sid": "upstream-session-secret"}})
	case request.URL.Path == "/api/user/self":
		if pool.expireOnce {
			pool.expireOnce = false
			fail(writer, 401, "private-upstream-error")
			return
		}
		respond(writer, 200, map[string]any{"capabilities": map[string]bool{"channel.read": true, "channel.write": true, "usage.read": true}, "private": "private-upstream-error"})
	case request.URL.Path == "/api/channel" && request.Method == "POST":
		pool.creates++
		json.NewDecoder(request.Body).Decode(&pool.payload)
		payload := pool.payload["channel"].(map[string]any)
		if payload["type"] == float64(41) {
			gateway, _ := pool.payload["security_gateway"].(map[string]any)
			requestID, _ := pool.payload["request_id"].(string)
			parsed, err := uuid.Parse(requestID)
			if err != nil || parsed.Version() != 4 || gateway["mode"] != "random" || gateway["request_type"] != "distill" || gateway["request_region"] != "intl" || gateway["timeline"] != "SHORT" || gateway["static"] != false || gateway["proxy_address"] != "" || len(gateway) != 6 {
				writer.WriteHeader(409)
				json.NewEncoder(writer).Encode(map[string]any{"success": false, "code": "SECURITY_GATEWAY_REQUIRED"})
				return
			}
		}
		pool.channel = upstreamChannel{ID: 990 + int64(pool.creates), Name: payload["name"].(string), Type: int(payload["type"].(float64)), Status: 1, Models: payload["models"].(string)}
		if pool.createMode == "uncertain" {
			fail(writer, 502, "private-upstream-error with upstream-session-secret")
			return
		}
		if pool.createMode == "malformed" {
			writer.Header().Set("Content-Type", "application/json")
			writer.Write([]byte(`{"unexpected":"private-upstream-error"}`))
			return
		}
		if pool.createMode == "rejected" {
			pool.channel = upstreamChannel{}
			fail(writer, 400, "private-upstream-error with customer-credential-secret")
			return
		}
		if pool.createMode == "no-id" {
			respond(writer, 200, nil)
			return
		}
		respond(writer, 200, map[string]any{"ids": []int64{pool.channel.ID}})
	case request.URL.Path == "/api/channel/search":
		items := []upstreamChannel{}
		if pool.channel.ID > 0 && pool.channel.Name == request.URL.Query().Get("keyword") {
			items = append(items, pool.channel)
		}
		respond(writer, 200, map[string]any{"items": items, "total": len(items)})
	case request.Method == "GET" && request.URL.Path == fmt.Sprintf("/api/channel/%d", pool.channel.ID):
		respond(writer, 200, map[string]any{"id": pool.channel.ID, "name": pool.channel.Name, "type": pool.channel.Type, "status": pool.channel.Status, "models": pool.channel.Models, "model_mapping": "{\"alias\":\"target\"}", "auto_ban": 0, "climbing_rules": []any{map[string]any{"rpm": 7}}, "tag": "internal-tag", "remark": "internal-remark", "key": "private-upstream-key", "owner_username": "private-upstream-owner", "setting": "private-proxy-password"})
	case request.Method == "PUT" && request.URL.Path == "/api/channel/":
		json.NewDecoder(request.Body).Decode(&pool.editPayload)
		if pool.editMode == "reject" {
			fail(writer, 400, "private-upstream-key")
			return
		}
		if pool.editMode != "unchanged" {
			pool.channel.Name = pool.editPayload["name"].(string)
			pool.channel.Models = pool.editPayload["models"].(string)
		}
		respond(writer, 200, nil)
	case request.URL.Path == "/api/channel/991/lifecycle":
		var input struct {
			Action string `json:"action"`
		}
		json.NewDecoder(request.Body).Decode(&input)
		if input.Action == "disable" {
			pool.channel.Status = 2
		} else {
			pool.channel.Status = 1
		}
		respond(writer, 200, nil)
	case request.URL.Path == "/api/supplier/logs":
		pool.lastQuery = request.URL.RawQuery
		channelID := int64(991)
		if pool.mixedLogs {
			channelID = 992
		}
		respond(writer, 200, map[string]any{"total": 1, "items": []map[string]any{{"channel": channelID, "created_at": 1700000000, "model_name": "gemini-4-argon", "prompt_tokens": 80, "completion_tokens": 20, "attempt_status": "success", "is_stream": true, "request_id": "private-request-id", "metering": map[string]any{"secret": "private-upstream-error"}, "attempt_quota": 9, "content": "customer-credential-secret", "owner_username": "private-upstream-owner"}}})
	default:
		fail(writer, 404, "private-upstream-error")
	}
}

type testSession struct {
	cookie *http.Cookie
	csrf   string
}
type fixture struct {
	app          *App
	pool         *fakePool
	handler      http.Handler
	databasePath string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := &fakePool{}
	server := httptest.NewServer(pool)
	t.Cleanup(server.Close)
	config := Config{Origin: "http://127.0.0.1:3000", Database: filepath.Join(t.TempDir(), "ali.db"), AdminUsername: "admin", AdminPassword: "Admin-Password-For-Tests", UpstreamURL: server.URL, UpstreamUsername: "supplier-test", UpstreamPassword: "upstream-password-secret", Templates: []Template{{ID: "vertex-key", Label: "Vertex key", Type: 41, CredentialKind: "api_key", Models: []string{"gemini-4-argon"}, Group: "private-pool-group", Regions: []string{"global"}, DefaultRegion: "global", Setting: map[string]any{"proxy": "private-proxy-password"}}}}
	database, err := openDatabase(config.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := bootstrapAdmin(database, config); err != nil {
		t.Fatal(err)
	}
	password, _ := hashPassword("Customer-Password-For-Tests")
	for _, username := range []string{"alice", "bob"} {
		if _, err := database.Exec("INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,'user',?)", username, password, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	app := newApp(config, database, log.New(io.Discard, "", 0))
	return &fixture{app: app, pool: pool, handler: app.handler(), databasePath: config.Database}
}

func (fixture *fixture) request(method, path, body string, session testSession, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", fixture.app.config.Origin)
	request.Header.Set("X-CSRF-Token", session.csrf)
	if session.cookie != nil {
		request.AddCookie(session.cookie)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	writer := httptest.NewRecorder()
	fixture.handler.ServeHTTP(writer, request)
	return writer
}

func (fixture *fixture) login(t *testing.T, username string) testSession {
	t.Helper()
	password := "Customer-Password-For-Tests"
	if username == "admin" {
		password = "Admin-Password-For-Tests"
	}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	response := fixture.request("POST", "/api/login", string(body), testSession{}, nil)
	if response.Code != 200 {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	return testSession{cookie: cookies[0], csrf: result.Data.CSRF}
}

const createBody = `{"name":"Customer channel","template_id":"vertex-key","credential":"customer-credential-secret","models":["gemini-4-argon"],"region":"global","project_id":""}`

func (fixture *fixture) create(t *testing.T, session testSession) Channel {
	t.Helper()
	response := fixture.request("POST", "/api/channels", createBody, session, map[string]string{"Idempotency-Key": "request-key-1234567890"})
	if response.Code != 200 && response.Code != 201 && response.Code != 202 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Data Channel `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Data
}

func TestCreateIsolationIdempotencyAndSecretRedaction(t *testing.T) {
	fixture := newFixture(t)
	alice := fixture.login(t, "alice")
	bob := fixture.login(t, "bob")
	admin := fixture.login(t, "admin")
	channel := fixture.create(t, alice)
	if channel.State != "ready" || channel.Status != 1 {
		t.Fatalf("unexpected state: %+v", channel)
	}
	duplicate := fixture.create(t, alice)
	if duplicate.ID != channel.ID || fixture.pool.creates != 1 {
		t.Fatal("duplicate submission reached upstream")
	}
	conflict := fixture.request("POST", "/api/channels", strings.Replace(createBody, "Customer channel", "Changed", 1), alice, map[string]string{"Idempotency-Key": "request-key-1234567890"})
	if conflict.Code != 409 {
		t.Fatal("idempotency conflict accepted")
	}
	payload := fixture.pool.payload["channel"].(map[string]any)
	if payload["name"] != "Customer channel" || payload["group"] != "private-pool-group" || payload["key"] != "customer-credential-secret" || fixture.pool.payload["mode"] != "single" {
		t.Fatalf("incorrect forwarding: %v", payload)
	}
	for _, path := range []string{fmt.Sprintf("/api/channels/%d/logs", channel.ID), fmt.Sprintf("/api/channels/%d/sync", channel.ID), fmt.Sprintf("/api/channels/%d/lifecycle", channel.ID)} {
		method := "POST"
		if strings.HasSuffix(path, "logs") {
			method = "GET"
		}
		before := fixture.pool.requests
		response := fixture.request(method, path, `{"action":"disable"}`, bob, nil)
		if response.Code != 404 || fixture.pool.requests != before {
			t.Fatalf("ownership bypass for %s", path)
		}
	}
	for _, entry := range []struct {
		path    string
		session testSession
	}{{"/api/channels", alice}, {"/api/channels", bob}, {"/api/templates", alice}, {"/api/admin/audit", admin}} {
		response := fixture.request("GET", entry.path, "", entry.session, nil)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		for _, secret := range []string{"customer-credential-secret", "private-pool-group", "private-proxy-password", "upstream-session-secret", "upstream-password-secret", "\"upstream_id\"", "\"upstream_name\""} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("secret leaked via %s", entry.path)
			}
		}
		if entry.session == bob && strings.Contains(response.Body.String(), "Customer channel") {
			t.Fatal("cross-user listing")
		}
	}
	for _, path := range []string{fixture.databasePath, fixture.databasePath + "-wal"} {
		contents, _ := os.ReadFile(path)
		for _, secret := range []string{"customer-credential-secret", "upstream-password-secret", alice.cookie.Value} {
			if bytes.Contains(contents, []byte(secret)) {
				t.Fatalf("credential persisted in %s", path)
			}
		}
	}
}

func TestAuthenticationOriginCSRFAndHiddenRoutes(t *testing.T) {
	fixture := newFixture(t)
	session := fixture.login(t, "alice")
	for _, test := range []struct {
		method, path, body string
		session            testSession
		headers            map[string]string
		status             int
	}{
		{"GET", "/api/channels", "", testSession{}, nil, 401},
		{"POST", "/api/channels", createBody, session, map[string]string{"X-CSRF-Token": ""}, 403},
		{"POST", "/api/login", `{"username":"alice","password":"anything"}`, testSession{}, map[string]string{"Origin": "https://evil.invalid"}, 403},
		{"POST", "/api/logout", "{}", session, map[string]string{"Origin": ""}, 403},
		{"POST", "/api/logout", "{}", session, map[string]string{"Content-Type": "text/plain"}, 415},
		{"GET", "/api/admin/users", "", session, nil, 403},
		{"GET", "/api/channel/991/key", "", session, nil, 404},
		{"GET", "/api/option", "", session, nil, 404},
		{"GET", "/v1/models", "", session, nil, 404},
		{"POST", "/api/channels", strings.Replace(createBody, `"name":`, `"upstream_id":991,"name":`, 1), session, map[string]string{"Idempotency-Key": "request-key-1234567890"}, 400},
	} {
		response := fixture.request(test.method, test.path, test.body, test.session, test.headers)
		if response.Code != test.status {
			t.Errorf("%s %s: got %d want %d", test.method, test.path, response.Code, test.status)
		}
	}
	if fixture.pool.requests != 0 {
		t.Fatal("invalid requests reached upstream")
	}
	fixture.app.database.Exec("UPDATE sessions SET expires_at=?", time.Now().Unix()-1)
	if response := fixture.request("GET", "/api/session", "", session, nil); response.Code != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestLogScopeWhitelistingAndPagination(t *testing.T) {
	fixture := newFixture(t)
	session := fixture.login(t, "alice")
	channel := fixture.create(t, session)
	path := fmt.Sprintf("/api/channels/%d/logs", channel.ID)
	response := fixture.request("GET", path+"?page=1&days=7", "", session, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"prompt_tokens":80`) {
		t.Fatal(response.Body.String())
	}
	if !strings.Contains(fixture.pool.lastQuery, "channel_id=991") {
		t.Fatal("missing upstream scope")
	}
	for _, field := range []string{"request_id", "metering", "attempt_quota", "content", "owner_username", "private-", "customer-credential-secret"} {
		if strings.Contains(response.Body.String(), field) {
			t.Fatalf("log leaked %s", field)
		}
	}
	for _, query := range []string{"?channel_id=992", "?page=0", "?page=-1", "?days=31", "?days=1&days=30", "?view=summary"} {
		if response := fixture.request("GET", path+query, "", session, nil); response.Code != 400 {
			t.Errorf("query accepted: %s", query)
		}
	}
	fixture.pool.mixedLogs = true
	response = fixture.request("GET", path, "", session, nil)
	if response.Code != 502 || strings.Contains(response.Body.String(), "gemini-4-argon") {
		t.Fatal("cross-channel log response leaked")
	}
}

func TestUncertainSubmissionReconcilesWithoutWriteRetry(t *testing.T) {
	fixture := newFixture(t)
	fixture.pool.createMode = "uncertain"
	session := fixture.login(t, "alice")
	channel := fixture.create(t, session)
	if channel.State != "uncertain" || fixture.pool.creates != 1 {
		t.Fatal("uncertain write not preserved")
	}
	duplicate := fixture.create(t, session)
	if duplicate.ID != channel.ID || fixture.pool.creates != 1 {
		t.Fatal("uncertain write replayed")
	}
	response := fixture.request("POST", fmt.Sprintf("/api/channels/%d/sync", channel.ID), "{}", session, nil)
	if response.Code != 409 || fixture.pool.creates != 1 {
		t.Fatal(response.Body.String())
	}
	path := fmt.Sprintf("/api/admin/channels/%d/reconcile", channel.ID)
	if fixture.request("POST", path, `{"upstream_id":991}`, session, nil).Code != 403 {
		t.Fatal("customer could self-bind")
	}
	admin := fixture.login(t, "admin")
	if fixture.request("POST", path, `{"upstream_id":992}`, admin, nil).Code != 409 {
		t.Fatal("wrong ID accepted")
	}
	if r := fixture.request("POST", path, `{"upstream_id":991}`, admin, nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	response = fixture.request("POST", fmt.Sprintf("/api/channels/%d/lifecycle", channel.ID), `{"action":"disable"}`, session, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":2`) {
		t.Fatal(response.Body.String())
	}
}

func TestRejectedSubmissionDoesNotExposeUpstreamErrors(t *testing.T) {
	fixture := newFixture(t)
	fixture.pool.createMode = "rejected"
	session := fixture.login(t, "alice")
	channel := fixture.create(t, session)
	if channel.State != "rejected" {
		t.Fatal("rejection not recorded")
	}
	var diagnostic string
	if err := fixture.app.database.QueryRow("SELECT outcome FROM audit WHERE channel_id=? AND action='channel.create.upstream'", channel.ID).Scan(&diagnostic); err != nil || diagnostic != "HTTP 400 · 上游拒绝创建请求" {
		t.Fatal("expected sanitized rejection in administrator audit")
	}
	response := fixture.request("POST", fmt.Sprintf("/api/channels/%d/sync", channel.ID), "{}", session, nil)
	if response.Code != 409 || strings.Contains(response.Body.String(), "secret") {
		t.Fatal("raw error leaked")
	}
}

func TestMalformedCreateResponseRemainsUncertain(t *testing.T) {
	fixture := newFixture(t)
	fixture.pool.createMode = "malformed"
	session := fixture.login(t, "alice")
	channel := fixture.create(t, session)
	if channel.State != "uncertain" {
		t.Fatal("missing success field was incorrectly treated as a definite rejection")
	}
	response := fixture.request("POST", fmt.Sprintf("/api/channels/%d/sync", channel.ID), "{}", session, nil)
	if response.Code != 409 || fixture.pool.creates != 1 {
		t.Fatal("malformed successful write must not be claimed by name or replayed")
	}
}

func TestOriginalNamesAndReceipts(t *testing.T) {
	fixture := newFixture(t)
	alice, bob := fixture.login(t, "alice"), fixture.login(t, "bob")
	name := "同名 渠道🚀"
	body := strings.Replace(createBody, "Customer channel", name, 1)
	for _, entry := range []struct {
		user       string
		session    testSession
		mode, want string
		id         int64
	}{{"alice", alice, "", "ready", 991}, {"bob", bob, "", "ready", 992}, {"bob", bob, "no-id", "uncertain", 0}} {
		fixture.pool.createMode = entry.mode
		response := fixture.request("POST", "/api/channels", body, entry.session, map[string]string{"Idempotency-Key": "original-name-test-" + entry.user + entry.mode})
		var reply struct {
			Data Channel `json:"data"`
		}
		if json.Unmarshal(response.Body.Bytes(), &reply) != nil || reply.Data.State != entry.want || reply.Data.Name != name {
			t.Fatalf("incorrect same-name create: %s", response.Body.String())
		}
		var id int64
		if err := fixture.app.database.QueryRow("SELECT COALESCE(upstream_id,0) FROM channels WHERE id=?", reply.Data.ID).Scan(&id); err != nil || id != entry.id {
			t.Fatal("wrong upstream binding", id, err)
		}
		if fixture.pool.payload["channel"].(map[string]any)["name"] != name {
			t.Fatal("public name modified")
		}
	}
	for _, bad := range []string{`{"ids":[8,9]}`, `{"id":7,"ids":[8]}`, `{"results":[{"channel_id":-7}]}`, `null`} {
		var receipt createReceipt
		if json.Unmarshal([]byte(bad), &receipt) != nil || receipt.singleID() != 0 {
			t.Fatal("ambiguous receipt accepted")
		}
	}
}

func TestEditOwnedChannelByBoundID(t *testing.T) {
	fixture := newFixture(t)
	alice, bob := fixture.login(t, "alice"), fixture.login(t, "bob")
	channel := fixture.create(t, alice)
	path := fmt.Sprintf("/api/channels/%d", channel.ID)
	body := `{"name":"中文名称","models":["custom/model-1","custom-2"]}`
	before := fixture.pool.requests
	for _, test := range []struct {
		session testSession
		body    string
		headers map[string]string
		code    int
	}{{bob, body, nil, 404}, {alice, body, map[string]string{"X-CSRF-Token": ""}, 403}, {alice, `{"id":999,"name":"other","models":["x"]}`, nil, 400}, {alice, `{"key":"do-not-forward","name":"x","models":["x"]}`, nil, 400}, {alice, `{"name":"  ","models":["x"]}`, nil, 400}, {alice, `{"name":"x","models":[]}`, nil, 400}} {
		if r := fixture.request("PUT", path, test.body, test.session, test.headers); r.Code != test.code {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if fixture.pool.requests != before {
		t.Fatal("unauthorized or invalid operation reached upstream")
	}
	for _, mode := range []string{"reject", "unchanged", ""} {
		fixture.pool.editMode = mode
		r := fixture.request("PUT", path, body, alice, nil)
		var name, models string
		err := fixture.app.database.QueryRow("SELECT name,models FROM channels WHERE id=?", channel.ID).Scan(&name, &models)
		if err != nil {
			t.Fatal(err)
		}
		if mode != "" {
			if r.Code != 502 || name != channel.Name || models != channel.Models || strings.Contains(r.Body.String(), "private-") {
				t.Fatal("unconfirmed write changed local state or leaked data")
			}
		} else if r.Code != 200 || name != "中文名称" || models != "custom/model-1,custom-2" {
			t.Fatal("edit not confirmed locally", r.Body.String())
		}
	}
	payload := fixture.pool.editPayload
	if payload["id"] != float64(991) || payload["auto_ban"] != float64(0) || payload["tag"] != "internal-tag" || payload["remark"] != "internal-remark" || payload["model_mapping"] != `{"alias":"target"}` || len(payload["climbing_rules"].([]any)) != 1 {
		t.Fatal("existing non-editable fields not preserved")
	}
	for _, sensitive := range []string{"key", "setting", "settings", "security_gateway", "proxy", "type", "group", "upstream_id"} {
		if _, ok := payload[sensitive]; ok {
			t.Fatal("unexpected field", sensitive)
		}
	}
	for _, state := range []string{"uncertain", "rejected", "archived"} {
		fixture.app.database.Exec("UPDATE channels SET state=? WHERE id=?", state, channel.ID)
		if r := fixture.request("PUT", path, body, alice, nil); r.Code != 409 {
			t.Fatal("uneditable record accepted", state)
		}
	}
}

func TestNameMigrationPreservesLegacyBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY,username TEXT);INSERT INTO users VALUES(2,'legacy');
 CREATE TABLE channels(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id),name TEXT NOT NULL,template_id TEXT NOT NULL,models TEXT NOT NULL,upstream_id INTEGER UNIQUE,upstream_name TEXT NOT NULL UNIQUE,request_key TEXT NOT NULL,request_hash TEXT NOT NULL,state TEXT NOT NULL,status INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,UNIQUE(user_id,request_key));
 CREATE INDEX channels_owner ON channels(user_id,id);INSERT INTO channels VALUES(21,2,'原名称','vertex-json','model',1162,'old-name','old-key','digest','ready',1,100,101);INSERT INTO channels VALUES(22,2,'待确认','vertex-json','model',NULL,?,'old-key-2','digest-2','creating',0,100,101);`, "ali_"+strings.Repeat("x", 43))
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = migrateChannelNames(database); err != nil {
		t.Fatal(err)
	}
	bound, err := scanChannel(database.QueryRow(channelSelect + " WHERE c.id=21"))
	if err != nil || bound.UpstreamID != 1162 || bound.Name != "原名称" || bound.UpstreamName != "old-name" || bound.RequestHash != "digest" {
		t.Fatal("lost legacy binding", err)
	}
	pending, err := scanChannel(database.QueryRow(channelSelect + " WHERE c.id=22"))
	if err != nil || pending.UpstreamName != "ali_"+strings.Repeat("x", 43) || pending.State != "uncertain" {
		t.Fatal("lost legacy record needed for manual reconciliation", err)
	}
	if _, err := database.Exec(`INSERT INTO channels(user_id,name,template_id,models,upstream_name,request_key,request_hash,state,created_at,updated_at) VALUES(2,'old-name','vertex-key','model','old-name','new-key','','uncertain',0,0)`); err != nil {
		t.Fatal("name still unique", err)
	}
}

func TestDisableAndPasswordChangeRevokeSessions(t *testing.T) {
	fixture := newFixture(t)
	admin := fixture.login(t, "admin")
	alice := fixture.login(t, "alice")
	var aliceID int64
	fixture.app.database.QueryRow("SELECT id FROM users WHERE username='alice'").Scan(&aliceID)
	response := fixture.request("PATCH", fmt.Sprintf("/api/admin/users/%d", aliceID), `{"enabled":false}`, admin, nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if fixture.request("GET", "/api/session", "", alice, nil).Code != 401 {
		t.Fatal("disabled session still valid")
	}
	bob := fixture.login(t, "bob")
	response = fixture.request("POST", "/api/password", `{"current_password":"Customer-Password-For-Tests","new_password":"Next-Customer-Password-2026"}`, bob, nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if fixture.request("GET", "/api/session", "", bob, nil).Code != 401 {
		t.Fatal("password change did not revoke sessions")
	}
	response = fixture.request("POST", "/api/login", `{"username":"bob","password":"Next-Customer-Password-2026"}`, testSession{}, nil)
	if response.Code != 200 {
		t.Fatal("new password failed")
	}
	response = fixture.request("POST", "/api/logout", "{}", admin, nil)
	if response.Code != 200 || fixture.request("GET", "/api/session", "", admin, nil).Code != 401 {
		t.Fatal("logout did not invalidate session")
	}
}

func TestReadReauthenticationAndNoRedirectFollowing(t *testing.T) {
	fixture := newFixture(t)
	if err := fixture.app.upstream.ensureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.pool.expireOnce = true
	var result map[string]any
	if err := fixture.app.upstream.call(context.Background(), "GET", "/api/user/self", nil, &result); err != nil {
		t.Fatal(err)
	}
	if fixture.pool.logins != 2 {
		t.Fatal("expired read session not renewed")
	}
	hits := 0
	sink := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { hits++ }))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { http.Redirect(writer, request, sink.URL, 307) }))
	defer redirect.Close()
	upstream := newUpstream(Config{UpstreamURL: redirect.URL})
	_, err := upstream.raw(context.Background(), "POST", "/anything", map[string]string{"secret": "credential"}, nil)
	if err == nil || hits != 0 {
		t.Fatal("credentials followed a redirect")
	}
}

func TestRateLimitAndAdminBinding(t *testing.T) {
	fixture := newFixture(t)
	admin := fixture.login(t, "admin")
	fixture.pool.channel = upstreamChannel{ID: 991, Name: "existing-pool-channel", Type: 41, Status: 2}
	var bobID int64
	fixture.app.database.QueryRow("SELECT id FROM users WHERE username='bob'").Scan(&bobID)
	body := fmt.Sprintf(`{"user_id":%d,"upstream_id":991,"template_id":"vertex-key","name":"Imported"}`, bobID)
	response := fixture.request("POST", "/api/admin/bind", body, admin, nil)
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	if response := fixture.request("POST", "/api/admin/bind", body, admin, nil); response.Code != 409 {
		t.Fatal("duplicate upstream owner allowed")
	}
	if fixture.pool.creates != 0 {
		t.Fatal("binding wrote to upstream")
	}
	bob := fixture.login(t, "bob")
	if response := fixture.request("GET", "/api/channels", "", bob, nil); !strings.Contains(response.Body.String(), "Imported") {
		t.Fatal("binding not visible to owner")
	}
	limiter := rateLimiter{}
	for count := 0; count < 3; count++ {
		if !limiter.allow("user", 3, time.Minute) {
			t.Fatal("early limit")
		}
	}
	if limiter.allow("user", 3, time.Minute) {
		t.Fatal("limit not enforced")
	}
	limiter.entries["user"] = rateEntry{Count: 100, Expires: time.Now().Add(-time.Second)}
	if !limiter.allow("user", 3, time.Minute) {
		t.Fatal("expired rate bucket did not recover")
	}
}

func TestCredentialAndTemplateValidation(t *testing.T) {
	template := Template{ID: "vertex-json", Type: 41, CredentialKind: "vertex_json", Models: []string{"model"}, Regions: []string{"global"}, Group: "vertex"}
	valid := channelInput{Name: "name", TemplateID: template.ID, Models: []string{"model"}, Region: "global", Credential: `{"type":"service_account","project_id":"test-project","client_email":"test@test-project.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----test","token_uri":"https://oauth2.googleapis.com/token"}`}
	if _, err := channelPayload(valid, template); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{`[]`, `{"type":"external_account"}`, strings.Replace(valid.Credential, "https://oauth2.googleapis.com/token", "http://127.0.0.1/private", 1)} {
		input := valid
		input.Credential = credential
		if _, err := channelPayload(input, template); err == nil {
			t.Fatal("unsafe credential accepted")
		}
	}
	input := valid
	input.Models = []string{"invalid,model"}
	if _, err := channelPayload(input, template); err == nil {
		t.Fatal("embedded model delimiter accepted")
	}
	input = valid
	input.Region = "http://127.0.0.1"
	if _, err := channelPayload(input, template); err == nil {
		t.Fatal("unknown region accepted")
	}
}

func TestVertexFormatPayloadAndRegionMapping(t *testing.T) {
	fixture := newFixture(t)
	session := fixture.login(t, "alice")
	response := fixture.request("GET", "/api/templates", "", session, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"type":41`) || !strings.Contains(response.Body.String(), `"related_models":`) {
		t.Fatal("missing safe Vertex form metadata")
	}
	template := Template{Type: 41, CredentialKind: "api_key", Models: []string{"alpha", "beta"}, Regions: []string{"global", "us-central1"}, Group: "vertex"}
	input := channelInput{Name: "Test", Credential: "unit-test-key", Models: []string{"alpha"}, Region: `{"alpha":"us-central1","default":"global"}`, ProjectID: "demo-project"}
	payload, err := channelPayload(input, template)
	if err != nil {
		t.Fatal(err)
	}
	channel := payload["channel"].(map[string]any)
	var regions map[string]string
	var settings map[string]any
	if json.Unmarshal([]byte(channel["other"].(string)), &regions) != nil || regions["alpha"] != "us-central1" || regions["default"] != "global" {
		t.Fatal("wrong Vertex other field")
	}
	if json.Unmarshal([]byte(channel["settings"].(string)), &settings) != nil || settings["vertex_key_type"] != "api_key" || settings["vertex_project_id"] != "demo-project" || payload["mode"] != "single" || channel["models"] != "alpha" || channel["type"] != 41 {
		t.Fatal("incorrect single-key payload")
	}
	for _, value := range []string{`{"default":"global","beta":"global"}`, `{"default":"external"}`, `{"alpha":"global"}`, `{"default":2}`, `{}`, `{bad`, `https://example.invalid`} {
		input.Region = value
		if _, err := channelPayload(input, template); err == nil {
			t.Fatalf("invalid region %q accepted", value)
		}
	}
}

func TestVertexSupplierCreateWireDefaults(t *testing.T) {
	template := Template{Type: 41, CredentialKind: "api_key", Regions: []string{"us-central1"}, Group: "vertex"}
	input := channelInput{Name: "local label", Credential: "fake-unit-key", Models: []string{"custom"}, Region: "us-central1"}
	payload, err := channelPayload(input, template)
	if err != nil {
		t.Fatal(err)
	}
	channel := payload["channel"].(map[string]any)
	if channel["other"] != "us-central1" || channel["name"] != input.Name || channel["key"] != input.Credential || channel["group"] != "vertex" {
		t.Fatal("wrong plain-region channel")
	}
	for _, key := range []string{"base_url", "openai_organization", "model_mapping", "test_model", "status_code_mapping", "tag", "remark", "param_override", "header_override"} {
		if val, ok := channel[key]; !ok || val != nil {
			t.Fatalf("%s should serialize as null", key)
		}
	}
	var settings, setting map[string]any
	if json.Unmarshal([]byte(channel["settings"].(string)), &settings) != nil || settings["vertex_key_type"] != "api_key" || settings["disable_task_polling_sleep"] != false || len(settings) != 2 {
		t.Fatal("unexpected default Vertex settings")
	}
	if json.Unmarshal([]byte(channel["setting"].(string)), &setting) != nil || len(setting) != 6 || setting["proxy"] != "" || setting["system_prompt"] != "" {
		t.Fatal("unexpected default channel settings")
	}
	for _, key := range []string{"force_format", "thinking_to_content", "pass_through_body_enabled", "system_prompt_override"} {
		if setting[key] != false {
			t.Fatal("missing default " + key)
		}
	}
	other, err := channelPayload(input, template)
	if err != nil || other["request_id"] == payload["request_id"] {
		t.Fatal("request IDs reused between submissions")
	}
	template.BaseURL = "https://example.invalid"
	template.Setting = map[string]any{"force_format": true}
	template.Settings = map[string]any{"disable_task_polling_sleep": true}
	configured, err := channelPayload(input, template)
	if err != nil {
		t.Fatal(err)
	}
	overrides := configured["channel"].(map[string]any)
	if overrides["base_url"] != template.BaseURL || !strings.Contains(overrides["setting"].(string), `"force_format":true`) || !strings.Contains(overrides["settings"].(string), `"disable_task_polling_sleep":true`) {
		t.Fatal("server template override was lost")
	}
}

func TestUpstreamRejectionOnlyKeepsKnownDiagnosticCodes(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{`{"success":false,"code":"SECURITY_GATEWAY_REQUIRED","message":"secret"}`, "HTTP 409 · SECURITY_GATEWAY_REQUIRED"},
		{`{"error":{"code":"SECURITY_GATEWAY_REQUIRED","message":"secret"}}`, "HTTP 409 · SECURITY_GATEWAY_REQUIRED"},
		{`{"code":"private-test-key","message":"secret"}`, "HTTP 409 · 上游拒绝创建请求"},
	} {
		err := rejectionFromResponse(409, []byte(test.body))
		if !errors.Is(err, errRejected) || err.Error() != test.want {
			t.Fatal("unsafe or incorrect diagnostic")
		}
	}
}

func TestVertexManuallyEnteredModelsForwardedAndSaved(t *testing.T) {
	fixture := newFixture(t)
	session := fixture.login(t, "alice")
	names := "publishers/google/models/custom-v1,partner.model@20261126"
	body := strings.Replace(createBody, `["gemini-4-argon"]`, `["publishers/google/models/custom-v1","partner.model@20261126"]`, 1)
	response := fixture.request("POST", "/api/channels", body, session, map[string]string{"Idempotency-Key": "manual-model-submit-12345"})
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	if fixture.pool.payload["channel"].(map[string]any)["models"] != names {
		t.Fatal("manual IDs not forwarded in order")
	}
	var stored string
	if err := fixture.app.database.QueryRow("SELECT models FROM channels").Scan(&stored); err != nil || stored != names {
		t.Fatal("manual IDs not persisted correctly")
	}
}

func TestVertexManualModelLimitsAndOtherTemplateScope(t *testing.T) {
	template := Template{Type: 41, CredentialKind: "api_key", Regions: []string{"global"}, Models: []string{"preset"}}
	input := channelInput{Name: "name", Credential: "fixture-only", Region: "global"}
	for _, models := range [][]string{nil, {""}, {"bad name"}, {"wild*"}, {"name,alias"}, {"https://invalid.example"}, {"model/../other"}, {"name\nother"}, {"x", "x"}, {strings.Repeat("x", 257)}, make([]string, 101)} {
		input.Models = models
		if _, err := channelPayload(input, template); err == nil {
			t.Fatal("invalid manual model list accepted")
		}
	}
	input.Models = []string{strings.Repeat("x", 256), "alpha:version@1"}
	input.Region = `{"default":"global","alpha:version@1":"global"}`
	if _, err := channelPayload(input, template); err != nil {
		t.Fatal(err)
	}
	template.Type = 1
	input.Region = ""
	if _, err := channelPayload(input, template); err == nil {
		t.Fatal("non-Vertex template restriction removed")
	}
}

func TestConcurrentCreateAndStaticBoundary(t *testing.T) {
	fixture := newFixture(t)
	session := fixture.login(t, "alice")
	responses := make(chan *httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			responses <- fixture.request("POST", "/api/channels", createBody, session, map[string]string{"Idempotency-Key": "concurrent-request-12345"})
		})
	}
	workers.Wait()
	close(responses)
	for response := range responses {
		if response.Code != 200 && response.Code != 201 && response.Code != 409 {
			t.Fatal(response.Body.String())
		}
	}
	fixture.pool.mutex.Lock()
	creates := fixture.pool.creates
	fixture.pool.mutex.Unlock()
	if creates != 1 {
		t.Fatalf("concurrent duplicate created %d channels", creates)
	}
	for _, path := range []string{"/.env", "/data/ali.db", "/templates.json", "/app.go"} {
		if response := fixture.request("GET", path, "", session, nil); response.Code != 404 {
			t.Fatalf("private file exposed: %s", path)
		}
	}
	home := fixture.request("GET", "/channels", "", testSession{}, nil)
	assetPattern := regexp.MustCompile(`(?:src|href)="(/static/[^"?]+\.(?:js|css))"`)
	assets := assetPattern.FindAllStringSubmatch(home.Body.String(), -1)
	if len(assets) < 2 || !strings.Contains(home.Body.String(), `id="root"`) {
		t.Fatal("missing built React entry or assets")
	}
	paths := []string{"/", "/channels", "/sign-in", "/supplier/logs", "/customers", "/audit", "/security", "/logo.png", "/LICENSE"}
	for _, asset := range assets {
		paths = append(paths, asset[1])
	}
	for _, path := range paths {
		response := fixture.request("GET", path, "", testSession{}, nil)
		if response.Code != 200 || response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("invalid static response: %s", path)
		}
		if strings.Contains(response.Body.String(), "mangoglow.org") {
			t.Fatal("upstream origin in frontend")
		}
	}
}

func TestChannelFiltersAndPaginationKeepOwnerScope(t *testing.T) {
	fixture := newFixture(t)
	alice := fixture.login(t, "alice")
	bob := fixture.login(t, "bob")
	channel := fixture.create(t, alice)
	for _, tc := range []struct {
		session testSession
		query   string
		total   int
	}{
		{alice, "q=Customer&model=argon&template=vertex-key&status=enabled&sort=name&page_size=20", 1},
		{bob, "q=Customer&model=argon&template=vertex-key&status=enabled&sort=name&page_size=20", 0},
		{alice, fmt.Sprintf("q=%%23%d&page_size=20", channel.ID), 1},
		{alice, "status=disabled&page_size=20", 0},
	} {
		response := fixture.request("GET", "/api/channels?"+tc.query, "", tc.session, nil)
		var result struct {
			Data struct {
				Total    int
				PageSize int `json:"page_size"`
				Items    []Channel
			} `json:"data"`
		}
		if json.Unmarshal(response.Body.Bytes(), &result) != nil || response.Code != 200 || result.Data.PageSize != 20 || result.Data.Total != tc.total || len(result.Data.Items) != tc.total {
			t.Fatalf("incorrect filtered page for %q", tc.query)
		}
	}
	for _, query := range []string{"sort=invalid", "status=invalid", "page_size=101"} {
		if fixture.request("GET", "/api/channels?"+query, "", alice, nil).Code != 400 {
			t.Fatal("invalid option accepted")
		}
	}
}

func TestInterruptedSubmissionRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'alice','irrelevant','user',1);
INSERT INTO channels(user_id,name,template_id,models,upstream_name,request_key,request_hash,state,created_at,updated_at) VALUES(1,'pending','vertex-key','model','unique-name','request-key','digest','creating',1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var state string
	if err := reopened.QueryRow("SELECT state FROM channels WHERE id=1").Scan(&state); err != nil || state != "uncertain" {
		t.Fatal("interrupted submission not recoverable")
	}
}

func TestLocalArchiveOfRejectedRecord(t *testing.T) {
	fixture := newFixture(t)
	fixture.pool.createMode = "rejected"
	alice, bob, admin := fixture.login(t, "alice"), fixture.login(t, "bob"), fixture.login(t, "admin")
	channel := fixture.create(t, alice)
	if !channel.CanArchive {
		t.Fatal("rejected record missing archive affordance")
	}
	base := fmt.Sprintf("/api/channels/%d", channel.ID)
	before := fixture.pool.requests
	assertList := func(session testSession, query string, want int, state string) {
		t.Helper()
		response := fixture.request("GET", "/api/channels?"+query, "", session, nil)
		var envelope struct {
			Data struct {
				Items []Channel `json:"items"`
				Total int       `json:"total"`
			} `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Data.Total != want || len(envelope.Data.Items) != want {
			t.Fatalf("unexpected result for %s: %s", query, response.Body.String())
		}
		if want == 1 && (envelope.Data.Items[0].ID != channel.ID || envelope.Data.Items[0].State != state || envelope.Data.Items[0].CanArchive != (state == "rejected")) {
			t.Fatalf("incorrect archive state for %s", query)
		}
	}
	assertList(alice, "", 1, "rejected")
	assertList(alice, "status=archived", 0, "")
	for _, check := range []struct {
		session testSession
		headers map[string]string
		code    int
	}{
		{testSession{}, nil, 401}, {bob, nil, 404}, {alice, map[string]string{"X-CSRF-Token": ""}, 403}, {alice, map[string]string{"Origin": "https://elsewhere.invalid"}, 403},
	} {
		if got := fixture.request("POST", base+"/archive", "{}", check.session, check.headers).Code; got != check.code {
			t.Fatalf("unexpected archive authorization result: %d", got)
		}
	}
	for _, invalid := range []string{`{"action":"archive"}`, `{"user_id":1}`} {
		if response := fixture.request("POST", base+"/archive", invalid, alice, nil); response.Code != 400 {
			t.Fatal("accepted extra input")
		}
	}
	for range cap(fixture.app.upstreamSlots) {
		fixture.app.upstreamSlots <- struct{}{}
	}
	for range 2 {
		response := fixture.request("POST", base+"/archive", "{}", alice, nil)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	for range cap(fixture.app.upstreamSlots) {
		<-fixture.app.upstreamSlots
	}
	assertList(alice, "", 0, "")
	assertList(alice, "status=rejected", 0, "")
	assertList(admin, "", 0, "")
	assertList(bob, "status=archived", 0, "")
	assertList(alice, "status=archived&q=Customer&model=argon&page_size=1", 1, "archived")
	assertList(admin, "status=archived", 1, "archived")
	if response := fixture.request("POST", base+"/archive", "{}", bob, nil); response.Code != 404 {
		t.Fatal("archived record disclosed to another customer")
	}
	for _, action := range []struct{ method, path, body string }{{"POST", "/sync", "{}"}, {"POST", "/lifecycle", `{"action":"enable"}`}, {"GET", "/logs", ""}} {
		if response := fixture.request(action.method, base+action.path, action.body, alice, nil); response.Code != 409 {
			t.Fatalf("allowed operation on archive: %s", action.path)
		}
	}
	replayed := fixture.create(t, alice)
	if replayed.ID != channel.ID || replayed.State != "archived" {
		t.Fatal("archive broke original submission identity")
	}
	var oldAudit, newAudit int
	if err := fixture.app.database.QueryRow(`SELECT COUNT(*) FILTER(WHERE action='channel.create' AND outcome='rejected'), COUNT(*) FILTER(WHERE action='channel.archive_local' AND user_id=? AND outcome='ok') FROM audit WHERE channel_id=?`, channel.UserID, channel.ID).Scan(&oldAudit, &newAudit); err != nil || oldAudit != 1 || newAudit != 1 {
		t.Fatal("missing or duplicated audit history", err)
	}
	if fixture.pool.requests != before {
		t.Fatal("local archive, replay or read-only archive operation reached upstream")
	}
}

func TestLocalArchiveEligibilityAndAuditAtomicity(t *testing.T) {
	fixture := newFixture(t)
	owner, admin := fixture.login(t, "alice"), fixture.login(t, "admin")
	channel := fixture.create(t, owner)
	path := fmt.Sprintf("/api/channels/%d/archive", channel.ID)
	before := fixture.pool.requests
	for _, current := range []struct {
		state      string
		upstreamID any
	}{{"ready", 991}, {"creating", nil}, {"uncertain", nil}, {"rejected", 991}} {
		if _, err := fixture.app.database.Exec("UPDATE channels SET state=?,upstream_id=? WHERE id=?", current.state, current.upstreamID, channel.ID); err != nil {
			t.Fatal(err)
		}
		if response := fixture.request("POST", path, "{}", admin, nil); response.Code != 409 {
			t.Fatalf("archived ineligible %s row", current.state)
		}
	}
	if _, err := fixture.app.database.Exec(`UPDATE channels SET upstream_id=NULL WHERE id=?`, channel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.database.Exec(`CREATE TRIGGER test_archive_audit_failure BEFORE INSERT ON audit WHEN NEW.action='channel.archive_local' BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if response := fixture.request("POST", path, "{}", admin, nil); response.Code != 500 {
		t.Fatal("archive reported success without audit")
	}
	var state string
	if err := fixture.app.database.QueryRow("SELECT state FROM channels WHERE id=?", channel.ID).Scan(&state); err != nil || state != "rejected" {
		t.Fatal("failed archive not rolled back")
	}
	if _, err := fixture.app.database.Exec("DROP TRIGGER test_archive_audit_failure"); err != nil {
		t.Fatal(err)
	}
	if response := fixture.request("POST", path, "{}", admin, nil); response.Code != 200 {
		t.Fatal("administrator could not archive a customer's rejected record")
	}
	var actor string
	if err := fixture.app.database.QueryRow(`SELECT username FROM users JOIN audit ON users.id=audit.user_id WHERE audit.action='channel.archive_local' AND audit.channel_id=?`, channel.ID).Scan(&actor); err != nil || actor != "admin" {
		t.Fatal("administrator audit attributed to wrong user")
	}
	if fixture.pool.requests != before {
		t.Fatal("archive contacted upstream")
	}
}

func TestProductionOriginAndSecureCookies(t *testing.T) {
	for _, name := range []string{"LISTEN_ADDR", "PUBLIC_ORIGIN", "TEMPLATES_FILE", "UPSTREAM_USERNAME", "UPSTREAM_PASSWORD", "UPSTREAM_URL", "LOCAL_ALLOW_WEAK_BOOTSTRAP_PASSWORD"} {
		t.Setenv(name, "")
	}
	t.Setenv("LISTEN_ADDR", "0.0.0.0:3000")
	if _, err := loadConfig(); err == nil {
		t.Fatal("public plain HTTP allowed")
	}
	t.Setenv("PUBLIC_ORIGIN", "https://portal.example")
	config, err := loadConfig()
	if err != nil || !config.SecureCookies {
		t.Fatalf("HTTPS config invalid: %v", err)
	}
	fixture := newFixture(t)
	fixture.app.config.SecureCookies = true
	fixture.app.config.Origin = "https://portal.example"
	session := fixture.login(t, "alice")
	if !session.cookie.Secure {
		t.Fatal("HTTPS session missing Secure flag")
	}
}

func TestLiveReadOnlyProtocol(t *testing.T) {
	if os.Getenv("LIVE_UPSTREAM_CHECK") != "1" {
		t.Skip("opt-in read-only upstream check")
	}
	config := Config{UpstreamURL: envOr("UPSTREAM_URL", "https://mangoglow.org"), UpstreamUsername: os.Getenv("UPSTREAM_USERNAME"), UpstreamPassword: os.Getenv("UPSTREAM_PASSWORD")}
	upstream := newUpstream(config)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := upstream.ensureLogin(ctx); err != nil {
		t.Fatal("upstream login failed")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, err := upstream.raw(cleanup, http.MethodPost, "/api/user/auth/logout", nil, nil); err != nil {
			t.Error("read-only check session logout failed")
		}
	}()
	var profile struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := upstream.call(ctx, http.MethodGet, "/api/user/self", nil, &profile); err != nil {
		t.Fatal("self profile read failed")
	}
	for _, capability := range []string{"channel.read", "channel.write", "usage.read"} {
		if !profile.Capabilities[capability] {
			t.Fatal("required capability is unavailable")
		}
	}
	var page struct {
		Items []upstreamChannel `json:"items"`
	}
	if err := upstream.call(ctx, http.MethodGet, "/api/channel?p=1&page_size=1", nil, &page); err != nil {
		t.Fatal("channel list read failed")
	}
	if len(page.Items) == 0 {
		t.Log("profile and channel list verified; no existing channel to test log scope")
		return
	}
	channel := page.Items[0]
	found, err := upstream.findChannel(ctx, channel.Name)
	if err != nil || found.ID != channel.ID {
		t.Fatal("exact channel search could not be verified")
	}
	var detail upstreamChannel
	if upstream.call(ctx, http.MethodGet, fmt.Sprintf("/api/channel/%d", channel.ID), nil, &detail) != nil || detail.ID != channel.ID {
		t.Fatal("channel detail read failed")
	}
	var logs struct {
		Items []struct {
			Channel int64 `json:"channel"`
		} `json:"items"`
	}
	now := time.Now().Unix()
	path := fmt.Sprintf("/api/supplier/logs?p=1&page_size=2&channel_id=%d&start_timestamp=%d&end_timestamp=%d", channel.ID, now-30*86400, now)
	if err := upstream.call(ctx, http.MethodGet, path, nil, &logs); err != nil {
		t.Fatal("supplier log read failed")
	}
	for _, entry := range logs.Items {
		if entry.Channel != channel.ID {
			t.Fatal("supplier log channel scope mismatch")
		}
	}
	t.Log("login, capabilities, channel list/search/detail and scoped supplier logs verified; no channel writes performed")
}

func TestLocalBootstrapPasswordCannotReachProduction(t *testing.T) {
	shortPassword := randomToken()[:8]
	config := Config{Listen: "127.0.0.1:3000", Origin: "http://127.0.0.1:3000", AdminUsername: "admin", AdminPassword: shortPassword, AllowWeakBootstrapPassword: true}
	database, err := openDatabase(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	withoutException := config
	withoutException.AllowWeakBootstrapPassword = false
	if err := bootstrapAdmin(database, withoutException); err == nil {
		t.Fatal("short bootstrap password accepted without explicit local exception")
	}
	if err := bootstrapAdmin(database, config); err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err := database.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&encoded); err != nil || !verifyPassword(encoded, shortPassword) || encoded == shortPassword {
		t.Fatal("local bootstrap password was not hashed correctly")
	}
	production := config
	production.AllowWeakBootstrapPassword = false
	production.Listen = "0.0.0.0:3000"
	production.Origin = "https://portal.example"
	production.SecureCookies = true
	if err := bootstrapAdmin(database, production); err == nil {
		t.Fatal("development database with short password allowed in production")
	}
	app := newApp(config, database, log.New(io.Discard, "", 0))
	subject := &fixture{app: app, handler: app.handler()}
	login := subject.request("POST", "/api/login", fmt.Sprintf(`{"username":"admin","password":%q}`, shortPassword), testSession{}, nil)
	if login.Code != 200 {
		t.Fatal("local development login failed")
	}
	var bundle struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	session := testSession{cookie: login.Result().Cookies()[0], csrf: bundle.Data.CSRF}
	shortChange := subject.request("POST", "/api/password", fmt.Sprintf(`{"current_password":%q,"new_password":"short123"}`, shortPassword), session, nil)
	if shortChange.Code != 400 {
		t.Fatal("bootstrap exception weakened the normal password change policy")
	}
	changed := subject.request("POST", "/api/password", fmt.Sprintf(`{"current_password":%q,"new_password":"Production-Strong-Password-2026"}`, shortPassword), session, nil)
	if changed.Code != 200 {
		t.Fatal("upgrading the development password failed")
	}
	if err := bootstrapAdmin(database, production); err != nil {
		t.Fatal("production remained blocked after password upgrade")
	}
	if response := subject.request("GET", "/api/session", "", session, nil); response.Code != 401 {
		t.Fatal("password upgrade did not revoke development session")
	}
}

func TestLocalBootstrapExceptionRejectsPublicListeners(t *testing.T) {
	for _, name := range []string{"TEMPLATES_FILE", "UPSTREAM_USERNAME", "UPSTREAM_PASSWORD", "UPSTREAM_URL"} {
		t.Setenv(name, "")
	}
	t.Setenv("LOCAL_ALLOW_WEAK_BOOTSTRAP_PASSWORD", "true")
	for _, test := range []struct {
		name, listen, origin string
		allowed              bool
	}{
		{"local", "127.0.0.1:3000", "http://127.0.0.1:3000", true},
		{"public HTTP", "0.0.0.0:3000", "http://127.0.0.1:3000", false},
		{"public HTTPS", "0.0.0.0:3000", "https://portal.example", false},
		{"external origin", "127.0.0.1:3000", "https://portal.example", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("LISTEN_ADDR", test.listen)
			t.Setenv("PUBLIC_ORIGIN", test.origin)
			_, err := loadConfig()
			if (err == nil) != test.allowed {
				t.Fatalf("incorrect local password exception boundary: allowed=%v", test.allowed)
			}
		})
	}
}
