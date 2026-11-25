package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type channelInput struct {
	Name       string   `json:"name"`
	TemplateID string   `json:"template_id"`
	Credential string   `json:"credential"`
	Models     []string `json:"models"`
	Region     string   `json:"region"`
	ProjectID  string   `json:"project_id"`
}

func (app *App) template(id string) (Template, bool) {
	for _, template := range app.config.Templates {
		if template.ID == id {
			return template, true
		}
	}
	return Template{}, false
}

func (app *App) catalog(writer http.ResponseWriter, request *http.Request, user User) {
	items := make([]map[string]any, 0, len(app.config.Templates))
	for _, template := range app.config.Templates {
		related := template.RelatedModels
		if related == nil {
			related = template.Models
		}
		items = append(items, map[string]any{"id": template.ID, "label": template.Label, "type": template.Type, "credential_kind": template.CredentialKind, "models": template.Models, "related_models": related, "regions": template.Regions, "default_region": template.DefaultRegion, "project_id_supported": template.Type == 41 && template.CredentialKind == "api_key"})
	}
	respond(writer, 200, items)
}

func vertexRegionMap(raw string, models, allowed []string) (string, error) {
	raw = strings.TrimSpace(raw)
	mapping := map[string]string{}
	if strings.HasPrefix(raw, "{") {
		if len(raw) > 8192 || json.Unmarshal([]byte(raw), &mapping) != nil || mapping["default"] == "" {
			return "", errors.New("部署区域映射必须包含 default，且所有区域必须是字符串")
		}
	} else {
		if !contains(allowed, raw) {
			return "", errors.New("部署区域不在允许范围内")
		}
		return raw, nil
	}
	for model, region := range mapping {
		if !contains(allowed, region) {
			return "", errors.New("部署区域不在允许范围内")
		}
		if model != "default" && !contains(models, model) {
			return "", errors.New("部署区域映射只能包含已选模型")
		}
	}
	encoded, err := json.Marshal(mapping)
	return string(encoded), err
}

var vertexModelIDPattern = regexp.MustCompile(`^[\pL\pN][\pL\pN._+@:/-]{0,255}$`)

func validVertexModelID(model string) bool {
	if !vertexModelIDPattern.MatchString(model) {
		return false
	}
	for part := range strings.SplitSeq(model, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validateChannelFields(name string, models []string, template Template) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 80 {
		return errors.New("请填写渠道名称（最多 80 字）")
	}
	if len(models) == 0 || template.Type == 41 && len(models) > 100 {
		return errors.New("请填写有效的模型列表；Vertex AI 每个渠道支持 1–100 个模型 ID")
	}
	seen := map[string]bool{}
	for _, model := range models {
		if seen[model] {
			return errors.New("模型 ID 不能重复")
		}
		if template.Type == 41 {
			if !validVertexModelID(model) {
				return errors.New("模型 ID 格式无效，请填写模型名称而非网址，且每项不超过 256 字符")
			}
		} else if !contains(template.Models, model) {
			return errors.New("模型不在允许范围内")
		}
		seen[model] = true
	}
	return nil
}

func channelPayload(input channelInput, template Template) (map[string]any, error) {
	if err := validateChannelFields(input.Name, input.Models, template); err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(input.Credential)) < 8 || len(input.Credential) > 128*1024 {
		return nil, errors.New("请填写有效凭据")
	}
	credential := strings.TrimSpace(input.Credential)
	if template.CredentialKind == "vertex_json" {
		var serviceAccount struct {
			Type       string `json:"type"`
			ProjectID  string `json:"project_id"`
			Email      string `json:"client_email"`
			PrivateKey string `json:"private_key"`
			TokenURI   string `json:"token_uri"`
		}
		if json.Unmarshal([]byte(credential), &serviceAccount) != nil || serviceAccount.Type != "service_account" || serviceAccount.ProjectID == "" || !strings.HasSuffix(serviceAccount.Email, ".iam.gserviceaccount.com") || !strings.HasPrefix(serviceAccount.PrivateKey, "-----BEGIN PRIVATE KEY-----") || serviceAccount.TokenURI != "https://oauth2.googleapis.com/token" {
			return nil, errors.New("请提供完整的 Google 服务账号 JSON，不支持外部身份配置或 JSON 数组")
		}
	} else if strings.ContainsAny(credential, "\r\n") {
		return nil, errors.New("一次只能提交一个 API Key")
	}
	settings := maps.Clone(template.Settings)
	if settings == nil {
		settings = map[string]any{}
	}
	other := ""
	setting := map[string]any{}
	if template.Type == 41 {
		setting = map[string]any{"force_format": false, "thinking_to_content": false, "proxy": "", "pass_through_body_enabled": false, "system_prompt": "", "system_prompt_override": false}
		if _, exists := settings["disable_task_polling_sleep"]; !exists {
			settings["disable_task_polling_sleep"] = false
		}
		delete(settings, "vertex_project_id")
		var err error
		other, err = vertexRegionMap(input.Region, input.Models, template.Regions)
		if err != nil {
			return nil, err
		}
		settings["vertex_key_type"] = "json"
		if template.CredentialKind == "api_key" {
			settings["vertex_key_type"] = "api_key"
			if input.ProjectID != "" && !projectPattern.MatchString(input.ProjectID) {
				return nil, errors.New("Google 项目 ID 格式不正确")
			}
			if input.ProjectID != "" {
				settings["vertex_project_id"] = input.ProjectID
			}
		} else if input.ProjectID != "" {
			return nil, errors.New("服务账号的项目由 JSON 决定")
		}
	} else if input.Region != "" || input.ProjectID != "" {
		return nil, errors.New("当前模板不支持区域或项目 ID")
	}
	maps.Copy(setting, template.Setting)
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, errors.New("渠道模板 settings 无效")
	}
	settingJSON, err := json.Marshal(setting)
	if err != nil {
		return nil, errors.New("渠道模板 setting 无效")
	}
	channel := map[string]any{
		"name": input.Name, "type": template.Type, "key": credential, "models": strings.Join(input.Models, ","), "group": template.Group,
		"base_url": template.BaseURL, "other": other, "settings": string(settingsJSON), "setting": string(settingJSON), "status": 1, "auto_ban": 1, "priority": 0, "weight": 0,
	}
	payload := map[string]any{"mode": "single", "channel": channel}
	if template.Type == 41 {
		if template.BaseURL == "" {
			channel["base_url"] = nil
		}
		for _, field := range []string{"openai_organization", "model_mapping", "test_model", "status_code_mapping", "tag", "remark", "param_override", "header_override"} {
			channel[field] = nil
		}
		payload["security_gateway"] = map[string]any{"mode": "random", "request_type": "distill", "request_region": "intl", "proxy_address": "", "static": false, "timeline": "SHORT"}
		requestID, err := uuid.NewRandom()
		if err != nil {
			return nil, errors.New("无法生成上游请求标识")
		}
		payload["request_id"] = requestID.String()
	}
	return payload, nil
}

func (app *App) listChannels(writer http.ResponseWriter, request *http.Request, user User) {
	page, err := boundedInt(request.URL.Query().Get("page"), 1, 1, 100000)
	if err != nil {
		fail(writer, 400, "分页参数不正确")
		return
	}
	query := request.URL.Query()
	pageSize, err := boundedInt(query.Get("page_size"), 50, 1, 100)
	if err != nil {
		fail(writer, 400, "每页数量不正确")
		return
	}
	where := " WHERE (c.user_id=? OR ?='admin')"
	args := []any{user.ID, user.Role}
	if keyword := strings.TrimSpace(query.Get("q")); keyword != "" {
		where += " AND (instr(lower(c.name),lower(?))>0 OR CAST(c.id AS TEXT)=?)"
		args = append(args, keyword, strings.TrimPrefix(keyword, "#"))
	}
	if model := strings.TrimSpace(query.Get("model")); model != "" {
		where += " AND instr(lower(c.models),lower(?))>0"
		args = append(args, model)
	}
	if kind := query.Get("template"); kind != "" {
		where += " AND c.template_id=?"
		args = append(args, kind)
	}
	switch query.Get("status") {
	case "":
		where += " AND c.state<>'archived'"
	case "archived":
		where += " AND c.state='archived'"
	case "enabled":
		where += " AND c.state='ready' AND c.status=1"
	case "disabled":
		where += " AND c.state='ready' AND c.status<>1"
	case "pending":
		where += " AND c.state IN ('creating','uncertain')"
	case "rejected":
		where += " AND c.state='rejected'"
	default:
		fail(writer, 400, "未知状态")
		return
	}
	order := map[string]string{"": "c.id DESC", "latest": "c.id DESC", "oldest": "c.id ASC", "name": "c.name COLLATE NOCASE,c.id", "updated": "c.updated_at DESC,c.id DESC"}[query.Get("sort")]
	if order == "" {
		fail(writer, 400, "未知排序")
		return
	}
	var total int
	if err := app.database.QueryRow("SELECT COUNT(*) FROM channels c"+where, args...).Scan(&total); err != nil {
		fail(writer, 500, "查询失败")
		return
	}
	rows, err := app.database.Query(channelSelect+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		fail(writer, 500, "查询失败")
		return
	}
	defer rows.Close()
	items := []Channel{}
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			fail(writer, 500, "查询失败")
			return
		}
		items = append(items, channel)
	}
	if rows.Err() != nil {
		fail(writer, 500, "查询失败")
		return
	}
	respond(writer, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (app *App) createChannel(writer http.ResponseWriter, request *http.Request, user User) {
	requestKey := request.Header.Get("Idempotency-Key")
	if len(requestKey) < 16 || !identifierPattern.MatchString(requestKey) {
		fail(writer, 400, "缺少有效的提交标识")
		return
	}
	var input channelInput
	if !readJSON(writer, request, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	template, exists := app.template(input.TemplateID)
	if !exists {
		fail(writer, 400, "渠道模板不存在")
		return
	}
	if input.Region == "" {
		input.Region = template.DefaultRegion
	}
	payload, err := channelPayload(input, template)
	if err != nil {
		fail(writer, 400, err.Error())
		return
	}
	fingerprint, _ := json.Marshal(input)
	requestHash := tokenHash(string(fingerprint))
	existing, err := scanChannel(app.database.QueryRow(channelSelect+" WHERE c.user_id=? AND c.request_key=?", user.ID, requestKey))
	if err == nil {
		if existing.RequestHash != requestHash {
			fail(writer, 409, "提交标识已用于其他内容")
			return
		}
		respond(writer, 200, existing)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(writer, 500, "提交失败")
		return
	}
	var count int
	if err := app.database.QueryRow("SELECT COUNT(*) FROM channels WHERE user_id=?", user.ID).Scan(&count); err != nil {
		fail(writer, 500, "提交失败")
		return
	}
	if count >= 1000 {
		fail(writer, 409, "已达到渠道数量限制，请联系管理员")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 70*time.Second)
	defer cancel()
	if err := app.upstream.ensureLogin(ctx); err != nil {
		fail(writer, 503, "渠道服务暂不可用，请联系管理员检查连接配置")
		return
	}
	now := time.Now().Unix()
	result, err := app.database.Exec(`INSERT INTO channels(user_id,name,template_id,models,upstream_name,request_key,request_hash,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'creating',?,?)`, user.ID, input.Name, input.TemplateID, strings.Join(input.Models, ","), input.Name, requestKey, requestHash, now, now)
	if err != nil {
		fail(writer, 409, "提交已受理或存储暂不可用，请刷新列表，不要重复提交")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		fail(writer, 500, "无法记录提交")
		return
	}
	app.audit(user.ID, "channel.create", id, "started")
	var receipt createReceipt
	err = app.upstream.call(ctx, http.MethodPost, "/api/channel", payload, &receipt)
	input.Credential = ""
	payload = nil
	state := "uncertain"
	if errors.Is(err, errRejected) {
		state = "rejected"
		var rejection *upstreamRejection
		if errors.As(err, &rejection) {
			app.audit(user.ID, "channel.create.upstream", id, rejection.Error())
		}
	}
	var resolved upstreamChannel
	createdID := receipt.singleID()
	if err == nil && createdID > 0 {
		err = app.upstream.call(ctx, http.MethodGet, fmt.Sprintf("/api/channel/%d", createdID), nil, &resolved)
	}
	if err == nil && createdID > 0 && resolved.ID == createdID && resolved.Name == input.Name && resolved.Type == template.Type {
		_, err = app.database.Exec("UPDATE channels SET upstream_id=?,state='ready',status=?,updated_at=? WHERE id=?", resolved.ID, resolved.Status, time.Now().Unix(), id)
		if err == nil {
			state = "ready"
		}
	}
	if state != "ready" {
		if _, err := app.database.Exec("UPDATE channels SET state=?,updated_at=? WHERE id=?", state, time.Now().Unix(), id); err != nil {
			fail(writer, 500, "提交结果待核对，请勿重复提交")
			return
		}
	}
	app.audit(user.ID, "channel.create", id, state)
	channel, err := app.ownedChannel(user, id)
	if err != nil {
		fail(writer, 500, "提交结果待核对，请刷新列表")
		return
	}
	status := http.StatusCreated
	if state != "ready" {
		status = http.StatusAccepted
	}
	respond(writer, status, channel)
}

func (app *App) archiveRejectedChannel(writer http.ResponseWriter, request *http.Request, user User, channel Channel) {
	if !readJSON(writer, request, &struct{}{}) {
		return
	}
	tx, err := app.database.BeginTx(request.Context(), nil)
	if err != nil {
		fail(writer, 500, "归档失败")
		return
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	result, err := tx.Exec(`UPDATE channels SET state='archived', updated_at=? WHERE id=? AND (user_id=? OR ?='admin') AND state='rejected' AND upstream_id IS NULL`, now, channel.ID, user.ID, user.Role)
	if err != nil {
		fail(writer, 500, "归档失败")
		return
	}
	changed, err := result.RowsAffected()
	if err != nil {
		fail(writer, 500, "归档失败")
		return
	}
	if changed == 0 {
		var state string
		err = tx.QueryRow("SELECT state FROM channels WHERE id=? AND (user_id=? OR ?='admin')", channel.ID, user.ID, user.Role).Scan(&state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			fail(writer, 404, "渠道不存在")
		case err != nil:
			fail(writer, 500, "归档失败")
		case state == "archived":
			respond(writer, 200, nil)
		default:
			fail(writer, 409, "仅可归档已被拒绝且未绑定上游渠道的记录")
		}
		return
	}
	if _, err := tx.Exec("INSERT INTO audit(user_id,action,channel_id,outcome,created_at) VALUES(?,'channel.archive_local',?,'ok',?)", user.ID, channel.ID, now); err != nil {
		fail(writer, 500, "归档失败")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(writer, 500, "归档结果待确认，请刷新列表")
		return
	}
	respond(writer, 200, nil)
}

func (app *App) syncChannel(writer http.ResponseWriter, request *http.Request, user User, channel Channel) {
	var upstream upstreamChannel
	if channel.UpstreamID == 0 {
		fail(writer, 409, "没有已核实的上游 ID，请管理员核对，不会按名称认领")
		return
	}
	err := app.upstream.call(request.Context(), http.MethodGet, fmt.Sprintf("/api/channel/%d", channel.UpstreamID), nil, &upstream)
	if err != nil || upstream.ID != channel.UpstreamID {
		fail(writer, 502, "暂时无法核对渠道；不会自动重复上号，请稍后同步或联系管理员")
		return
	}
	template, exists := app.template(channel.TemplateID)
	if !exists || upstream.Type != template.Type {
		fail(writer, 409, "渠道类型不匹配，请联系管理员")
		return
	}
	name := channel.Name
	if upstream.Name != "" && upstream.Name != channel.UpstreamName {
		name = upstream.Name
	}
	result, err := app.database.Exec("UPDATE channels SET upstream_id=?,upstream_name=?,name=?,models=?,state='ready',status=?,updated_at=? WHERE id=? AND state<>'archived' AND (upstream_id IS NULL OR upstream_id=?)", upstream.ID, upstream.Name, name, upstream.Models, upstream.Status, time.Now().Unix(), channel.ID, upstream.ID)
	if err != nil {
		fail(writer, 409, "渠道绑定失败，请联系管理员核对归属")
		return
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		fail(writer, 409, "记录状态已发生变化，请刷新列表")
		return
	}
	channel, err = app.ownedChannel(user, channel.ID)
	if err != nil {
		fail(writer, 500, "查询失败")
		return
	}
	app.audit(user.ID, "channel.sync", channel.ID, "ok")
	respond(writer, 200, channel)
}

func (app *App) reconcileChannel(writer http.ResponseWriter, request *http.Request, user User, channel Channel) {
	var input struct {
		UpstreamID int64 `json:"upstream_id"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if input.UpstreamID <= 0 {
		fail(writer, 400, "请填写待核实的上游 ID")
		return
	}
	if channel.State != "uncertain" || channel.UpstreamID != 0 {
		fail(writer, 409, "仅可核对未绑定的待确认记录")
		return
	}
	template, ok := app.template(channel.TemplateID)
	var detail upstreamChannel
	if !ok || app.upstream.call(request.Context(), http.MethodGet, fmt.Sprintf("/api/channel/%d", input.UpstreamID), nil, &detail) != nil || detail.ID != input.UpstreamID || detail.Type != template.Type || detail.Name != channel.UpstreamName {
		fail(writer, 409, "指定 ID 的渠道名称或类型无法核实，未绑定")
		return
	}
	tx, err := app.database.BeginTx(request.Context(), nil)
	if err != nil {
		fail(writer, 500, "绑定失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE channels SET state='ready',upstream_id=?,status=?,models=?,updated_at=? WHERE id=? AND state='uncertain' AND upstream_id IS NULL`, detail.ID, detail.Status, detail.Models, time.Now().Unix(), channel.ID)
	if err != nil {
		fail(writer, 409, "此上游 ID 已绑定或记录无法更新")
		return
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		fail(writer, 409, "记录状态已变化")
		return
	}
	if _, err = tx.Exec("INSERT INTO audit(user_id,action,channel_id,outcome,created_at) VALUES(?,'channel.reconcile',?,'ok',?)", user.ID, channel.ID, time.Now().Unix()); err != nil || tx.Commit() != nil {
		fail(writer, 500, "绑定失败，请刷新核对")
		return
	}
	respond(writer, 200, nil)
}

func (app *App) editChannel(writer http.ResponseWriter, request *http.Request, user User, channel Channel) {
	if channel.State != "ready" || channel.UpstreamID <= 0 {
		fail(writer, 409, "仅能编辑已绑定的渠道")
		return
	}
	var input struct {
		Name   string   `json:"name"`
		Models []string `json:"models"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	template, ok := app.template(channel.TemplateID)
	if !ok {
		fail(writer, 409, "渠道模板不可用")
		return
	}
	if err := validateChannelFields(input.Name, input.Models, template); err != nil {
		fail(writer, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 65*time.Second)
	defer cancel()
	path := fmt.Sprintf("/api/channel/%d", channel.UpstreamID)
	var current struct {
		upstreamChannel
		ModelMapping  string          `json:"model_mapping"`
		TestModel     string          `json:"test_model"`
		AutoBan       *int            `json:"auto_ban"`
		StatusMapping string          `json:"status_code_mapping"`
		Tag           string          `json:"tag"`
		Remark        string          `json:"remark"`
		Rules         json.RawMessage `json:"climbing_rules"`
	}
	if app.upstream.call(ctx, http.MethodGet, path, nil, &current) != nil || current.ID != channel.UpstreamID || current.Type != template.Type {
		fail(writer, 502, "无法核验当前渠道，未发送编辑请求")
		return
	}
	models := strings.Join(input.Models, ",")
	if current.Name != input.Name || current.Models != models {
		payload := map[string]any{"id": channel.UpstreamID, "name": input.Name, "models": models, "model_mapping": current.ModelMapping, "test_model": current.TestModel, "status_code_mapping": current.StatusMapping, "tag": current.Tag, "remark": current.Remark}
		if current.AutoBan != nil {
			payload["auto_ban"] = *current.AutoBan
		}
		if len(current.Rules) > 0 && string(current.Rules) != "null" {
			payload["climbing_rules"] = current.Rules
		}
		err := app.upstream.call(ctx, http.MethodPut, "/api/channel/", payload, nil)
		if errors.Is(err, errRejected) {
			app.audit(user.ID, "channel.edit", channel.ID, "rejected")
			fail(writer, 502, "上游拒绝修改，原本地记录已保留")
			return
		}
		if err != nil {
			app.audit(user.ID, "channel.edit", channel.ID, "unconfirmed")
			fail(writer, 502, "编辑结果待确认，请同步后再操作")
			return
		}
	}
	var verified upstreamChannel
	if app.upstream.call(ctx, http.MethodGet, path, nil, &verified) != nil || verified.ID != channel.UpstreamID || verified.Type != template.Type || verified.Name != input.Name || verified.Models != models {
		app.audit(user.ID, "channel.edit", channel.ID, "unconfirmed")
		fail(writer, 502, "上游变更尚未核实，请同步后再操作")
		return
	}
	result, err := app.database.Exec(`UPDATE channels SET name=?,upstream_name=?,models=?,status=?,updated_at=? WHERE id=? AND upstream_id=? AND state='ready'`, input.Name, input.Name, models, verified.Status, time.Now().Unix(), channel.ID, channel.UpstreamID)
	if err != nil {
		fail(writer, 500, "上游已更新，本地未同步，请刷新并同步")
		return
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		fail(writer, 409, "归属已变化，请刷新核对")
		return
	}
	app.audit(user.ID, "channel.edit", channel.ID, "ok")
	respond(writer, 200, nil)
}

func (app *App) channelAction(writer http.ResponseWriter, request *http.Request, user User, channel Channel) {
	var input struct {
		Action string `json:"action"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if input.Action != "enable" && input.Action != "disable" {
		fail(writer, 400, "仅支持启用或停用")
		return
	}
	if channel.UpstreamID == 0 || channel.State != "ready" {
		fail(writer, 409, "请先同步确认渠道")
		return
	}
	err := app.upstream.call(request.Context(), http.MethodPost, fmt.Sprintf("/api/channel/%d/lifecycle", channel.UpstreamID), map[string]string{"action": input.Action}, nil)
	if err != nil {
		app.audit(user.ID, "channel."+input.Action, channel.ID, "unconfirmed")
		fail(writer, 502, "操作结果未确认，请同步状态后再操作")
		return
	}
	app.audit(user.ID, "channel."+input.Action, channel.ID, "accepted")
	app.syncChannel(writer, request, user, channel)
}

type safeLog struct {
	CreatedAt        int64  `json:"created_at"`
	Model            string `json:"model_name"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Status           string `json:"attempt_status"`
	Stream           bool   `json:"is_stream"`
}

func (app *App) channelLogs(writer http.ResponseWriter, request *http.Request, user User, channel Channel) {
	if channel.UpstreamID <= 0 || channel.State != "ready" {
		fail(writer, 409, "请先同步确认渠道")
		return
	}
	allowed := map[string]bool{"page": true, "days": true}
	for key, values := range request.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			fail(writer, 400, "不支持的日志查询条件")
			return
		}
	}
	page, pageErr := boundedInt(request.URL.Query().Get("page"), 1, 1, 10000)
	days, daysErr := boundedInt(request.URL.Query().Get("days"), 1, 1, 30)
	if pageErr != nil || daysErr != nil {
		fail(writer, 400, "日志查询参数不正确")
		return
	}
	now := time.Now().Unix()
	query := url.Values{"p": {strconv.Itoa(page)}, "page_size": {"50"}, "channel_id": {strconv.FormatInt(channel.UpstreamID, 10)}, "start_timestamp": {strconv.FormatInt(now-int64(days)*86400, 10)}, "end_timestamp": {strconv.FormatInt(now, 10)}}
	var result struct {
		Total int64 `json:"total"`
		Items []struct {
			safeLog
			Channel int64 `json:"channel"`
		} `json:"items"`
	}
	if err := app.upstream.call(request.Context(), http.MethodGet, "/api/supplier/logs?"+query.Encode(), nil, &result); err != nil {
		fail(writer, 502, "日志服务暂不可用")
		return
	}
	if result.Total < 0 || len(result.Items) > 50 {
		fail(writer, 502, "日志响应不符合约定")
		return
	}
	items := []safeLog{}
	models := strings.Split(channel.Models, ",")
	statuses := []string{"success", "invalid_usage", "stream_interrupted", "downstream_dropped", "local_failure", "provider_rejected", "network_error", "protocol_error"}
	for _, row := range result.Items {
		if row.Channel != channel.UpstreamID {
			app.audit(user.ID, "logs.scope", channel.ID, "denied")
			fail(writer, 502, "日志归属核验失败，已拒绝返回数据")
			return
		}
		if !contains(models, row.Model) {
			row.Model = "其他模型"
		}
		if !contains(statuses, row.Status) {
			row.Status = "unknown"
		}
		items = append(items, row.safeLog)
	}
	respond(writer, 200, map[string]any{"items": items, "total": result.Total, "page": page, "page_size": 50})
}
