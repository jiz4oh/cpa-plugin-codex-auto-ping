package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct cliproxy_buffer {
    uint8_t* ptr;
    size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void* host_ctx, const char* method,
    const uint8_t* request, size_t request_len, cliproxy_buffer* response);
typedef void (*cliproxy_host_free_buffer_fn)(void* ptr, size_t len);

typedef struct cliproxy_host_api {
    uint32_t abi_version;
    void* host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_buffer_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char* method, uint8_t* request,
    size_t request_len, cliproxy_buffer* response);
typedef void (*cliproxy_plugin_free_buffer_fn)(void* ptr, size_t len);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct cliproxy_plugin_api {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;
    cliproxy_plugin_free_buffer_fn free_buffer;
    cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

#ifdef _WIN32
#define CPA_PLUGIN_EXPORT __declspec(dllexport)
#else
#define CPA_PLUGIN_EXPORT
#endif

extern CPA_PLUGIN_EXPORT int autoPingPluginCall(char* method, uint8_t* request,
    size_t request_len, cliproxy_buffer* response);
extern CPA_PLUGIN_EXPORT void autoPingPluginFreeBuffer(void* ptr, size_t len);
extern CPA_PLUGIN_EXPORT void autoPingPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static inline void store_host_api(const cliproxy_host_api* host) {
    stored_host = host;
}

static inline void set_plugin_api(cliproxy_plugin_api* plugin) {
    plugin->abi_version = 1;
    plugin->call = autoPingPluginCall;
    plugin->free_buffer = autoPingPluginFreeBuffer;
    plugin->shutdown = autoPingPluginShutdown;
}

static inline int call_host_api(const char* method, const uint8_t* request,
    size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) return 1;
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static inline void free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
        stored_host->free_buffer(ptr, len);
    }
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	pluginName    = "auto-ping"
	version       = "0.2.1"
	codexURL      = "https://chatgpt.com/backend-api/codex/responses"
	modelName     = "gpt-5.6-luna"
	defaultPrompt = "ping"
	defaultTZ     = "Asia/Shanghai"
)

var defaultTimes = []string{"06:00", "11:00", "16:00", "21:00"}

var (
	runtimeMu       sync.Mutex
	schedulerCancel context.CancelFunc
	currentConfig   = defaultConfig()
)

type config struct {
	Enabled  bool     `json:"enabled"`
	Timezone string   `json:"timezone"`
	Times    []string `json:"times"`
}

type registerRequest struct {
	ConfigYAML string `json:"config_yaml"`
}

type authFile struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	AuthIndex   string `json:"auth_index"`
	Account     string `json:"account,omitempty"`
	Email       string `json:"email,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Type        string `json:"type,omitempty"`
	Status      string `json:"status,omitempty"`
	Disabled    bool   `json:"disabled"`
	Unavailable bool   `json:"unavailable,omitempty"`
}

type httpRequest struct {
	Method  string              `json:"Method"`
	URL     string              `json:"URL"`
	Headers map[string][]string `json:"Headers,omitempty"`
	Body    []byte              `json:"Body,omitempty"`
}

type httpResponse struct {
	StatusCode int `json:"StatusCode"`
}

type authMaterial struct {
	AccessToken string
	AccountID   string
}

type codexBody struct {
	Model        string         `json:"model"`
	Instructions string         `json:"instructions"`
	Input        []codexMessage `json:"input"`
	Store        bool           `json:"store"`
	Stream       bool           `json:"stream"`
}

type codexMessage struct {
	Type    string      `json:"type"`
	Role    string      `json:"role"`
	Content []codexPart `json:"content"`
}

type codexPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type registerResult struct {
	SchemaVersion int                  `json:"schema_version"`
	Metadata      metadata             `json:"metadata"`
	Capabilities  registerCapabilities `json:"capabilities"`
}

type registerCapabilities struct {
	ManagementAPI bool `json:"management_api"`
}

type managementRegistration struct {
	Resources []any `json:"resources"`
}

type metadata struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository,omitempty"`
	Description      string        `json:"Description"`
	ConfigFields     []configField `json:"ConfigFields,omitempty"`
}

type configField struct {
	Name         string `json:"Name"`
	Type         string `json:"Type"`
	Description  string `json:"Description"`
	DefaultValue any    `json:"DefaultValue"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil {
		return -1
	}
	C.store_host_api(host)
	C.set_plugin_api(plugin)
	return 0
}

//export autoPingPluginCall
func autoPingPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response == nil {
		return -1
	}
	name := ""
	if method != nil {
		name = C.GoString(method)
	}

	requestBytes, ok := copyRequestBytes(request, requestLen)
	if !ok {
		return writeJSON(response, failEnvelope("invalid_request", "invalid request length"))
	}

	switch name {
	case "plugin.register", "plugin.reconfigure":
		var req registerRequest
		if len(requestBytes) > 0 {
			if err := json.Unmarshal(requestBytes, &req); err != nil {
				return writeJSON(response, failEnvelope("invalid_request", "invalid plugin configuration envelope"))
			}
		}
		cfg, err := parseConfig(req.ConfigYAML)
		if err != nil {
			return writeJSON(response, failEnvelope("invalid_config", err.Error()))
		}
		applyConfig(cfg)
		return writeJSON(response, okEnvelope(registrationResult()))
	case "management.register":
		return writeJSON(response, okEnvelope(managementRegistration{Resources: []any{}}))
	case "plugin.shutdown":
		stopScheduler()
		return writeJSON(response, okEnvelope(map[string]any{"status": "stopped"}))
	default:
		return writeJSON(response, failEnvelope("unsupported_method", "unsupported plugin method"))
	}
}

//export autoPingPluginFreeBuffer
func autoPingPluginFreeBuffer(ptr unsafe.Pointer, length C.size_t) {
	_ = length
	C.free(ptr)
}

//export autoPingPluginShutdown
func autoPingPluginShutdown() { stopScheduler() }

func registrationResult() registerResult {
	cfg := defaultConfig()
	return registerResult{
		SchemaVersion: 5,
		Metadata: metadata{
			Name:             pluginName,
			Version:          version,
			Author:           "jiz4oh",
			GitHubRepository: "https://github.com/jiz4oh/cpa-plugin-codex-auto-ping",
			Description:      "Ping every enabled Codex OAuth account at configured times using gpt-5.6-luna.",
			ConfigFields: []configField{
				{Name: "timezone", Type: "string", Description: "IANA timezone used by the scheduler, e.g. Asia/Shanghai.", DefaultValue: cfg.Timezone},
				{Name: "times", Type: "string", Description: "Daily HH:MM times. YAML list is recommended, e.g. [06:00, 11:00, 16:00, 21:00].", DefaultValue: strings.Join(cfg.Times, ",")},
			},
		},
		Capabilities: registerCapabilities{ManagementAPI: true},
	}
}

func defaultConfig() config {
	return config{Enabled: true, Timezone: defaultTZ, Times: append([]string(nil), defaultTimes...)}
}

func applyConfig(cfg config) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	if schedulerCancel != nil {
		schedulerCancel()
		schedulerCancel = nil
	}
	currentConfig = cfg
	if !cfg.Enabled {
		logf("disabled by CPA config")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	schedulerCancel = cancel
	go schedulerLoop(ctx, cfg)
}

func stopScheduler() {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if schedulerCancel != nil {
		schedulerCancel()
		schedulerCancel = nil
	}
}

func schedulerLoop(ctx context.Context, cfg config) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		logf("invalid timezone %q: %v", cfg.Timezone, err)
		return
	}

	for {
		next := nextRun(time.Now().In(loc), loc, cfg.Times)
		logf("next run at %s model=%s", next.Format(time.RFC3339), modelName)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
			runAll(ctx)
		}
	}
}

func nextRun(now time.Time, loc *time.Location, times []string) time.Time {
	best := time.Time{}
	for _, value := range times {
		hour, minute, _ := parseClock(value)
		candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		if best.IsZero() || candidate.Before(best) {
			best = candidate
		}
	}
	return best
}

func runAll(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()

	files, err := listAuth(ctx)
	if err != nil {
		logf("auth list failed: %v", err)
		return
	}
	attempted, succeeded := 0, 0
	for _, a := range files {
		if !isCodex(a) || a.AuthIndex == "" || a.Disabled || a.Unavailable {
			continue
		}
		attempted++
		if err := pingAuth(ctx, a); err != nil {
			logf("ping failed auth=%s email=%s: %v", safeName(a), a.Email, err)
			continue
		}
		succeeded++
		logf("ping ok auth=%s email=%s", safeName(a), a.Email)
	}
	logf("run complete attempted=%d succeeded=%d failed=%d", attempted, succeeded, attempted-succeeded)
}

func isCodex(a authFile) bool {
	p := strings.ToLower(strings.TrimSpace(a.Provider))
	t := strings.ToLower(strings.TrimSpace(a.Type))
	n := strings.ToLower(strings.TrimSpace(a.Name))
	return p == "codex" || t == "codex" || strings.Contains(p, "codex") || strings.Contains(t, "codex") || strings.Contains(n, "codex")
}

func pingAuth(ctx context.Context, a authFile) error {
	raw, err := getAuth(ctx, a.AuthIndex)
	if err != nil {
		return err
	}
	m, err := parseAuthMaterial(raw)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(codexBody{
		Model:        modelName,
		Instructions: "You are a helpful assistant.",
		Input: []codexMessage{{
			Type: "message", Role: "user",
			Content: []codexPart{{Type: "input_text", Text: defaultPrompt}},
		}},
		Store: false, Stream: true,
	})
	headers := map[string][]string{
		"Accept":        {"text/event-stream"},
		"Authorization": {"Bearer " + m.AccessToken},
		"Content-Type":  {"application/json"},
		"OpenAI-Beta":   {"responses=v1"},
		"originator":    {"codex_cli_rs"},
		"User-Agent":    {"codex_cli_rs/0.76.0"},
	}
	if m.AccountID != "" {
		headers["Chatgpt-Account-Id"] = []string{m.AccountID}
	}

	var resp httpResponse
	if err := callHost(ctx, "host.http.do", httpRequest{Method: "POST", URL: codexURL, Headers: headers, Body: body}, &resp); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	return nil
}

func listAuth(ctx context.Context) ([]authFile, error) {
	var resp struct {
		Files []authFile `json:"files"`
	}
	if err := callHost(ctx, "host.auth.list", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Files, nil
}

func getAuth(ctx context.Context, authIndex string) ([]byte, error) {
	var resp struct {
		AuthIndex string          `json:"auth_index"`
		Name      string          `json:"name"`
		JSON      json.RawMessage `json:"json"`
	}
	if err := callHost(ctx, "host.auth.get", map[string]any{"auth_index": authIndex}, &resp); err != nil {
		return nil, err
	}
	if len(resp.JSON) == 0 {
		return nil, fmt.Errorf("empty auth JSON")
	}
	return resp.JSON, nil
}

func parseAuthMaterial(raw []byte) (authMaterial, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return authMaterial{}, fmt.Errorf("invalid auth JSON")
	}
	token := firstString(root, "access_token", "accessToken", "oauth_access_token", "oauthAccessToken", "token", "id_token", "idToken")
	accountID := firstString(root, "account_id", "chatgpt_account_id", "accountId", "chatgptAccountId")
	for _, key := range []string{"tokens", "credentials", "auth", "oauth", "session"} {
		var nested map[string]json.RawMessage
		if b, ok := root[key]; ok && json.Unmarshal(b, &nested) == nil {
			if token == "" {
				token = firstString(nested, "access_token", "accessToken", "oauth_access_token", "oauthAccessToken", "token", "id_token", "idToken")
			}
			if accountID == "" {
				accountID = firstString(nested, "account_id", "chatgpt_account_id", "accountId", "chatgptAccountId")
			}
		}
	}
	if token == "" {
		return authMaterial{}, fmt.Errorf("missing access token")
	}
	return authMaterial{AccessToken: token, AccountID: accountID}, nil
}

func firstString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func parseConfig(raw string) (config, error) {
	cfg := defaultConfig()
	text := strings.TrimSpace(raw)
	if text == "" {
		return validateConfig(cfg)
	}

	if strings.HasPrefix(text, "{") {
		var partial struct {
			Enabled  *bool    `json:"enabled"`
			Timezone string   `json:"timezone"`
			Times    []string `json:"times"`
		}
		if err := json.Unmarshal([]byte(text), &partial); err != nil {
			return config{}, fmt.Errorf("invalid JSON config: %w", err)
		}
		if partial.Enabled != nil {
			cfg.Enabled = *partial.Enabled
		}
		if strings.TrimSpace(partial.Timezone) != "" {
			cfg.Timezone = strings.TrimSpace(partial.Timezone)
		}
		if len(partial.Times) > 0 {
			cfg.Times = partial.Times
		}
		return validateConfig(cfg)
	}

	lines := strings.Split(text, "\n")
	inTimes := false
	var parsedTimes []string
	for _, rawLine := range lines {
		line := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[0])
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "-") && inTimes {
			parsedTimes = append(parsedTimes, unquote(strings.TrimSpace(strings.TrimPrefix(line, "-"))))
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		inTimes = false
		switch key {
		case "enabled":
			if value != "" {
				b, err := strconv.ParseBool(unquote(value))
				if err != nil {
					return config{}, fmt.Errorf("enabled must be true or false")
				}
				cfg.Enabled = b
			}
		case "timezone":
			if value != "" {
				cfg.Timezone = unquote(value)
			}
		case "times":
			inTimes = true
			if value != "" {
				parsedTimes = parseInlineList(value)
				inTimes = false
			}
		}
	}
	if len(parsedTimes) > 0 {
		cfg.Times = parsedTimes
	}
	return validateConfig(cfg)
}

func validateConfig(cfg config) (config, error) {
	cfg.Timezone = strings.TrimSpace(cfg.Timezone)
	if cfg.Timezone == "" {
		cfg.Timezone = defaultTZ
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return config{}, fmt.Errorf("invalid timezone %q", cfg.Timezone)
	}
	if len(cfg.Times) == 0 {
		return config{}, fmt.Errorf("times must contain at least one HH:MM value")
	}

	seen := map[string]bool{}
	normalized := make([]string, 0, len(cfg.Times))
	for _, value := range cfg.Times {
		value = strings.TrimSpace(unquote(value))
		hour, minute, err := parseClock(value)
		if err != nil {
			return config{}, err
		}
		norm := fmt.Sprintf("%02d:%02d", hour, minute)
		if !seen[norm] {
			seen[norm] = true
			normalized = append(normalized, norm)
		}
	}
	cfg.Times = normalized
	return cfg, nil
}

func parseClock(value string) (int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time %q: expected HH:MM", value)
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid time %q: expected 00:00-23:59", value)
	}
	return hour, minute, nil
}

func parseInlineList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, unquote(strings.TrimSpace(p)))
	}
	return out
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func copyRequestBytes(request *C.uint8_t, requestLen C.size_t) ([]byte, bool) {
	length := int(requestLen)
	if length < 0 || C.size_t(length) != requestLen {
		return nil, false
	}
	if length == 0 {
		return nil, true
	}
	if request == nil {
		return nil, false
	}
	requestSlice := unsafe.Slice((*byte)(unsafe.Pointer(request)), length)
	return append([]byte(nil), requestSlice...), true
}

func callHost(ctx context.Context, method string, payload any, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cm := C.CString(method)
	defer C.free(unsafe.Pointer(cm))
	var req *C.uint8_t
	var cPayload unsafe.Pointer
	if len(raw) > 0 {
		cPayload = C.CBytes(raw)
		if cPayload == nil {
			return fmt.Errorf("allocation failure")
		}
		defer C.free(cPayload)
		req = (*C.uint8_t)(cPayload)
	}
	var response C.cliproxy_buffer
	code := C.call_host_api(cm, req, C.size_t(len(raw)), &response)
	data := copyHostResponse(response)
	if response.ptr != nil {
		C.free_host_buffer(unsafe.Pointer(response.ptr), response.len)
	}
	if len(data) == 0 {
		return fmt.Errorf("host callback %s empty response code=%d", method, int(code))
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode host response: %w", err)
	}
	if !env.OK {
		if env.Error != nil {
			return fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("host callback failed")
	}
	if code != 0 {
		return fmt.Errorf("host callback code=%d", int(code))
	}
	if target != nil && len(env.Result) != 0 {
		return json.Unmarshal(env.Result, target)
	}
	return nil
}

func copyHostResponse(r C.cliproxy_buffer) []byte {
	if r.ptr == nil || r.len == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(r.ptr), C.int(r.len))
}

func okEnvelope(result any) []byte {
	b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	return b
}

func failEnvelope(code, message string) []byte {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message, "retryable": false}})
	return b
}

func writeJSON(response *C.cliproxy_buffer, data []byte) C.int {
	if len(data) == 0 {
		response.ptr = nil
		response.len = 0
		return 0
	}
	ptr := C.malloc(C.size_t(len(data)))
	if ptr == nil {
		return -1
	}
	C.memcpy(ptr, unsafe.Pointer(&data[0]), C.size_t(len(data)))
	response.ptr = (*C.uint8_t)(ptr)
	response.len = C.size_t(len(data))
	return 0
}

func safeName(a authFile) string {
	if a.Name != "" {
		return a.Name
	}
	if a.ID != "" {
		return a.ID
	}
	return a.AuthIndex
}

func logf(format string, args ...any) { fmt.Printf("[auto-ping] "+format+"\n", args...) }
