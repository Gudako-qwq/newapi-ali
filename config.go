package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen                     string
	Origin                     string
	Database                   string
	AdminUsername              string
	AdminPassword              string
	UpstreamURL                string
	UpstreamUsername           string
	UpstreamPassword           string
	Templates                  []Template
	SecureCookies              bool
	AllowWeakBootstrapPassword bool
}

type Template struct {
	ID             string         `json:"id"`
	Label          string         `json:"label"`
	Type           int            `json:"type"`
	CredentialKind string         `json:"credential_kind"`
	Models         []string       `json:"models"`
	RelatedModels  []string       `json:"related_models,omitempty"`
	Group          string         `json:"group"`
	Regions        []string       `json:"regions"`
	DefaultRegion  string         `json:"default_region"`
	BaseURL        string         `json:"base_url"`
	Setting        map[string]any `json:"setting"`
	Settings       map[string]any `json:"settings"`
}

func loadConfig() (Config, error) {
	config := Config{
		Listen:           envOr("LISTEN_ADDR", "127.0.0.1:3000"),
		Origin:           envOr("PUBLIC_ORIGIN", "http://127.0.0.1:3000"),
		Database:         envOr("DATABASE_PATH", "data/ali.db"),
		AdminUsername:    envOr("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		AdminPassword:    os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		UpstreamURL:      envOr("UPSTREAM_URL", "https://mangoglow.org"),
		UpstreamUsername: os.Getenv("UPSTREAM_USERNAME"),
		UpstreamPassword: os.Getenv("UPSTREAM_PASSWORD"),
	}
	weakBootstrap, err := strconv.ParseBool(envOr("LOCAL_ALLOW_WEAK_BOOTSTRAP_PASSWORD", "false"))
	if err != nil {
		return config, errors.New("LOCAL_ALLOW_WEAK_BOOTSTRAP_PASSWORD must be true or false")
	}
	config.AllowWeakBootstrapPassword = weakBootstrap
	origin, err := url.Parse(config.Origin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return config, errors.New("PUBLIC_ORIGIN must be an origin without a path or credentials")
	}
	config.Origin = strings.TrimSuffix(config.Origin, "/")
	config.SecureCookies = origin.Scheme == "https"
	if !config.SecureCookies && (origin.Scheme != "http" || !loopbackHost(origin.Hostname())) {
		return config, errors.New("PUBLIC_ORIGIN requires HTTPS except on loopback")
	}
	if !config.SecureCookies {
		host, _, splitErr := net.SplitHostPort(config.Listen)
		if splitErr != nil || !loopbackHost(host) {
			return config, errors.New("HTTP development mode must listen on loopback")
		}
	}
	if config.AllowWeakBootstrapPassword && !localBootstrapAllowed(config) {
		return config, errors.New("weak bootstrap passwords are allowed only with a loopback HTTP origin and loopback listener")
	}
	upstream, err := url.Parse(config.UpstreamURL)
	if err != nil || upstream.Scheme != "https" || upstream.Host == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" || (upstream.Path != "" && upstream.Path != "/") {
		return config, errors.New("UPSTREAM_URL must be an HTTPS origin without credentials")
	}
	config.UpstreamURL = strings.TrimSuffix(config.UpstreamURL, "/")
	if (config.UpstreamUsername == "") != (config.UpstreamPassword == "") {
		return config, errors.New("both upstream credentials must be configured together")
	}
	config.Templates = []Template{
		{ID: "vertex-json", Label: "Vertex AI · 服务账号 JSON", Type: 41, CredentialKind: "vertex_json", Models: []string{"gemini-3.8-flash", "gemini-4-argon"}, Group: "vertex", Regions: []string{"us-central1", "asia-southeast1", "global"}, DefaultRegion: "us-central1"},
		{ID: "vertex-key", Label: "Vertex AI · API Key", Type: 41, CredentialKind: "api_key", Models: []string{"gemini-3.8-flash", "gemini-4-argon"}, Group: "vertex", Regions: []string{"us-central1", "asia-southeast1", "global"}, DefaultRegion: "asia-southeast1"},
	}
	if path := os.Getenv("TEMPLATES_FILE"); path != "" {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return config, errors.New("cannot read TEMPLATES_FILE")
		}
		if err := json.Unmarshal(contents, &config.Templates); err != nil {
			return config, errors.New("TEMPLATES_FILE is not valid JSON")
		}
	}
	if len(config.Templates) == 0 {
		return config, errors.New("at least one channel template is required")
	}
	seen := map[string]bool{}
	for _, template := range config.Templates {
		if !identifierPattern.MatchString(template.ID) || seen[template.ID] || template.Label == "" || template.Type < 1 || len(template.Models) == 0 || template.Group == "" {
			return config, errors.New("invalid or duplicate channel template")
		}
		seen[template.ID] = true
		for _, model := range template.RelatedModels {
			if !contains(template.Models, model) {
				return config, errors.New("related_models must be a subset of models")
			}
		}
		if template.CredentialKind != "api_key" && template.CredentialKind != "vertex_json" {
			return config, errors.New("unsupported credential_kind")
		}
		if template.CredentialKind == "vertex_json" && template.Type != 41 {
			return config, errors.New("vertex_json requires channel type 41")
		}
		if template.Type == 41 && (len(template.Regions) == 0 || !contains(template.Regions, template.DefaultRegion)) {
			return config, errors.New("Vertex templates require a valid default region")
		}
	}
	return config, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func loopbackHost(host string) bool { return host == "localhost" || net.ParseIP(host).IsLoopback() }

func localBootstrapAllowed(config Config) bool {
	if !config.AllowWeakBootstrapPassword || config.SecureCookies {
		return false
	}
	origin, originErr := url.Parse(config.Origin)
	host, _, listenErr := net.SplitHostPort(config.Listen)
	return originErr == nil && listenErr == nil && origin.Scheme == "http" && loopbackHost(origin.Hostname()) && loopbackHost(host)
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func boundedInt(value string, fallback, minimum, maximum int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New("invalid integer")
	}
	return parsed, nil
}
