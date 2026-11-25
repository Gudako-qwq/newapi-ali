package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"sync"
	"time"
)

var errUpstream = errors.New("upstream request failed")
var errRejected = errors.New("upstream rejected request")
var errUnconfigured = errors.New("upstream is not configured")

type upstreamEnvelope struct {
	Success *bool           `json:"success"`
	Data    json.RawMessage `json:"data"`
}

type upstreamRejection struct {
	status   int
	category string
}

func (rejection *upstreamRejection) Unwrap() error { return errRejected }

func (rejection *upstreamRejection) Error() string {
	return "HTTP " + strconv.Itoa(rejection.status) + " · " + rejection.category
}

func rejectionFromResponse(status int, body []byte) *upstreamRejection {
	var details struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &details)
	reason := "上游拒绝创建请求"
	switch status {
	case 401:
		reason = "上游会话失效"
	case 403:
		reason = "上游拒绝访问"
	case 429:
		reason = "上游请求限流"
	}
	for _, code := range []string{details.Code, details.Error.Code} {
		switch code {
		case "SECURITY_GATEWAY_REQUIRED", "SECURITY_GATEWAY_UPSTREAM_VERIFY_FAILED":
			return &upstreamRejection{status, code}
		}
	}
	return &upstreamRejection{status, reason}
}

type Upstream struct {
	baseURL          string
	username         string
	password         string
	client           *http.Client
	mutex            sync.Mutex
	loggedIn         bool
	lastLoginFailure time.Time
}

func newUpstream(config Config) *Upstream {
	jar, _ := cookiejar.New(nil)
	return &Upstream{baseURL: config.UpstreamURL, username: config.UpstreamUsername, password: config.UpstreamPassword, client: &http.Client{
		Jar: jar, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (upstream *Upstream) raw(ctx context.Context, method, path string, input any, output any) (int, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, errUpstream
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, upstream.baseURL+path, body)
	if err != nil {
		return 0, errUpstream
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", upstream.baseURL)
	response, err := upstream.client.Do(request)
	if err != nil {
		return 0, errUpstream
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(contents) > 4*1024*1024 {
		return response.StatusCode, errUpstream
	}
	if response.StatusCode >= 500 || response.StatusCode >= 300 && response.StatusCode < 400 {
		return response.StatusCode, errUpstream
	}
	var envelope upstreamEnvelope
	if json.Unmarshal(contents, &envelope) != nil {
		return response.StatusCode, errUpstream
	}
	if envelope.Success == nil {
		return response.StatusCode, errUpstream
	}
	if !*envelope.Success || response.StatusCode >= 400 {
		return response.StatusCode, rejectionFromResponse(response.StatusCode, contents)
	}
	if output != nil && json.Unmarshal(envelope.Data, output) != nil {
		return response.StatusCode, errUpstream
	}
	return response.StatusCode, nil
}

func (upstream *Upstream) ensureLogin(ctx context.Context) error {
	upstream.mutex.Lock()
	defer upstream.mutex.Unlock()
	if upstream.loggedIn {
		return nil
	}
	if upstream.username == "" || upstream.password == "" {
		return errUnconfigured
	}
	if time.Since(upstream.lastLoginFailure) < time.Minute {
		return errUpstream
	}
	var status struct {
		PasswordTransport struct {
			Mode string `json:"mode"`
		} `json:"password_transport"`
		Turnstile bool `json:"turnstile_check"`
	}
	if _, err := upstream.raw(ctx, http.MethodGet, "/api/status", nil, &status); err != nil {
		upstream.lastLoginFailure = time.Now()
		return errUpstream
	}
	if status.PasswordTransport.Mode != "disabled" || status.Turnstile {
		upstream.lastLoginFailure = time.Now()
		return errUpstream
	}
	var login struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	_, err := upstream.raw(ctx, http.MethodPost, "/api/user/login", map[string]string{"username": upstream.username, "password": upstream.password}, &login)
	if err != nil || login.User.ID <= 0 {
		upstream.lastLoginFailure = time.Now()
		return errUpstream
	}
	parsed, _ := url.Parse(upstream.baseURL)
	hasSession := false
	for _, cookie := range upstream.client.Jar.Cookies(parsed) {
		if cookie.Name == "new_api_session" && cookie.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		upstream.lastLoginFailure = time.Now()
		return errUpstream
	}
	upstream.loggedIn = true
	return nil
}

func (upstream *Upstream) call(ctx context.Context, method, path string, input, output any) error {
	if err := upstream.ensureLogin(ctx); err != nil {
		return err
	}
	status, err := upstream.raw(ctx, method, path, input, output)
	if status != http.StatusUnauthorized {
		return err
	}
	upstream.mutex.Lock()
	upstream.loggedIn = false
	upstream.mutex.Unlock()
	if method != http.MethodGet {
		return err
	}
	if err := upstream.ensureLogin(ctx); err != nil {
		return err
	}
	_, err = upstream.raw(ctx, method, path, input, output)
	return err
}

type upstreamChannel struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Type   int    `json:"type"`
	Status int    `json:"status"`
	Models string `json:"models"`
}

type createReceipt struct {
	ID      int64   `json:"id"`
	IDs     []int64 `json:"ids"`
	Results []struct {
		ChannelID int64 `json:"channel_id"`
	} `json:"results"`
}

func (receipt createReceipt) singleID() int64 {
	if receipt.ID < 0 || len(receipt.IDs) > 1 || len(receipt.Results) > 1 {
		return 0
	}
	id := receipt.ID
	for _, candidate := range receipt.IDs {
		if candidate <= 0 || id != 0 && candidate != id {
			return 0
		}
		id = candidate
	}
	for _, result := range receipt.Results {
		if result.ChannelID <= 0 || id != 0 && result.ChannelID != id {
			return 0
		}
		id = result.ChannelID
	}
	return id
}

func (upstream *Upstream) findChannel(ctx context.Context, name string) (upstreamChannel, error) {
	var page struct {
		Items []upstreamChannel `json:"items"`
	}
	err := upstream.call(ctx, http.MethodGet, "/api/channel/search?keyword="+url.QueryEscape(name)+"&p=1&page_size=100", nil, &page)
	if err != nil {
		return upstreamChannel{}, err
	}
	var found upstreamChannel
	for _, channel := range page.Items {
		if channel.Name != name {
			continue
		}
		if found.ID != 0 || channel.ID <= 0 {
			return upstreamChannel{}, errUpstream
		}
		found = channel
	}
	if found.ID == 0 {
		return found, errUpstream
	}
	return found, nil
}
