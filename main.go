package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct cliproxy_buffer { uint8_t* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void* host_ctx, const char* method,
    const uint8_t* request, size_t request_len, cliproxy_buffer* response);
typedef void (*cliproxy_host_free_buffer_fn)(void* ptr, size_t len);
typedef struct cliproxy_host_api {
    uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_buffer_fn free_buffer;
} cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char* method, uint8_t* request, size_t request_len, cliproxy_buffer* response);
typedef void (*cliproxy_plugin_free_buffer_fn)(void* ptr, size_t len);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct cliproxy_plugin_api {
    uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_buffer_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;
#ifdef _WIN32
#define CPA_PLUGIN_EXPORT __declspec(dllexport)
#else
#define CPA_PLUGIN_EXPORT
#endif
extern CPA_PLUGIN_EXPORT int autoPingPluginCall(char* method, uint8_t* request, size_t request_len, cliproxy_buffer* response);
extern CPA_PLUGIN_EXPORT void autoPingPluginFreeBuffer(void* ptr, size_t len);
extern CPA_PLUGIN_EXPORT void autoPingPluginShutdown(void);
static const cliproxy_host_api* stored_host;
static inline void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static inline void set_plugin_api(cliproxy_plugin_api* plugin) {
    plugin->abi_version = 1; plugin->call = autoPingPluginCall; plugin->free_buffer = autoPingPluginFreeBuffer; plugin->shutdown = autoPingPluginShutdown;
}
static inline int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) return 1;
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}
static inline void free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) stored_host->free_buffer(ptr, len);
}
*/
import "C"

import (
    "context"
    "encoding/json"
    "fmt"
    "html"
    "sort"
    "strconv"
    "strings"
    "sync"
    "time"
    "unsafe"
)

const (
    pluginName = "codex-auto-ping"
    version = "0.2.8"
    codexURL = "https://chatgpt.com/backend-api/codex/responses"
    modelName = "gpt-5.6-luna"
    defaultPrompt = "ping"
    defaultTZ = "Asia/Shanghai"
    windowInterval = 5 * time.Hour
    windowGuard = 1 * time.Second
    runTimeout = 20 * time.Minute
    attemptTimeout = 45 * time.Second
    maxAttempts = 3
    retryBaseDelay = 5 * time.Second
)

var (
    defaultTimes = []string{"06:00", "11:00", "16:00", "21:00"}
    runtimeMu sync.Mutex
    schedulerCancel context.CancelFunc
    currentConfig = defaultConfig()
    stateMu sync.RWMutex
    running bool
    nextRunAt time.Time
    lastRun *runSummary
    accountStates = map[string]accountState{}
)

type config struct { Enabled bool `json:"enabled"`; Timezone string `json:"timezone"`; Times []string `json:"times"` }
type registerRequest struct { ConfigYAML string `json:"config_yaml"` }
type authFile struct {
    ID string `json:"id,omitempty"`; Name string `json:"name"`; AuthIndex string `json:"auth_index"`; Account string `json:"account,omitempty"`;
    Email string `json:"email,omitempty"`; Provider string `json:"provider,omitempty"`; Type string `json:"type,omitempty"`; Status string `json:"status,omitempty"`;
    Disabled bool `json:"disabled"`; Unavailable bool `json:"unavailable,omitempty"`
}
type httpRequest struct { Method string `json:"Method"`; URL string `json:"URL"`; Headers map[string][]string `json:"Headers,omitempty"`; Body []byte `json:"Body,omitempty"` }
type httpResponse struct { StatusCode int `json:"StatusCode"`; Headers map[string][]string `json:"Headers,omitempty"`; Body []byte `json:"Body,omitempty"` }
type authMaterial struct { AccessToken, AccountID string }
type codexBody struct { Model string `json:"model"`; Instructions string `json:"instructions"`; Input []codexMessage `json:"input"`; Store bool `json:"store"`; Stream bool `json:"stream"` }
type codexMessage struct { Type string `json:"type"`; Role string `json:"role"`; Content []codexPart `json:"content"` }
type codexPart struct { Type string `json:"type"`; Text string `json:"text"` }
type registerResult struct { SchemaVersion int `json:"schema_version"`; Metadata metadata `json:"metadata"`; Capabilities registerCapabilities `json:"capabilities"` }
type registerCapabilities struct { ManagementAPI bool `json:"management_api"` }
type managementRoute struct { Method, Path, Menu, Description string }
type resourceRoute struct { Path, Menu, Description string }
type managementRegistration struct { Routes []managementRoute `json:"routes,omitempty"`; Resources []resourceRoute `json:"resources,omitempty"` }
type managementRequest struct { Method, Path string; Headers map[string][]string; Query map[string][]string; Body []byte }
type managementResponse struct { StatusCode int; Headers map[string][]string; Body []byte }
type accountRunResult struct {
    AuthIndex string `json:"auth_index,omitempty"`; Name string `json:"name"`; Email string `json:"email,omitempty"`; Unavailable bool `json:"unavailable,omitempty"`;
    Status string `json:"status"`; Attempts int `json:"attempts"`; HTTPStatus int `json:"http_status,omitempty"`; Error string `json:"error,omitempty"`;
    EligibleAt *time.Time `json:"eligible_at,omitempty"`; ResetsAt *time.Time `json:"resets_at,omitempty"`
}
type runSummary struct {
    At time.Time `json:"at"`; Mode string `json:"mode"`; Total int `json:"total"`; Attempted int `json:"attempted"`; Succeeded int `json:"succeeded"`;
    Failed int `json:"failed"`; Limited int `json:"limited"`; Skipped int `json:"skipped"`; Error string `json:"error,omitempty"`; Accounts []accountRunResult `json:"accounts,omitempty"`
}
type accountState struct {
    AuthIndex string `json:"auth_index"`; Name string `json:"name"`; Email string `json:"email,omitempty"`; Unavailable bool `json:"unavailable,omitempty"`;
    Status string `json:"status"`; Attempts int `json:"attempts"`; LastAttempt time.Time `json:"last_attempt,omitempty"`; LastSuccess time.Time `json:"last_success,omitempty"`;
    EligibleAt time.Time `json:"eligible_at,omitempty"`; ResetsAt time.Time `json:"resets_at,omitempty"`; Error string `json:"error,omitempty"`
}
type statusResponse struct {
    Enabled bool `json:"enabled"`; Version, Model, Timezone string; Times []string `json:"times"`; WindowSeconds int64 `json:"window_seconds"`; GuardSeconds int64 `json:"guard_seconds"`;
    MaxAttempts int `json:"max_attempts"`; RetryBaseSeconds int64 `json:"retry_base_seconds"`; NextRun *time.Time `json:"next_run,omitempty"`; Running bool `json:"running"`;
    LastRun *runSummary `json:"last_run,omitempty"`; Accounts []accountState `json:"accounts,omitempty"`
}
type pingOutcome struct { Status string; HTTPStatus int; Retryable bool; Error string; ResetsAt time.Time }
type upstreamErrorEnvelope struct { Error struct { Type, Message, PlanType string; ResetsAt int64 `json:"resets_at"`; ResetsInSeconds int64 `json:"resets_in_seconds"` } `json:"error"` }
type metadata struct { Name string `json:"Name"`; Version string `json:"Version"`; Author string `json:"Author"`; GitHubRepository string `json:"GitHubRepository,omitempty"`; Description string `json:"Description"`; ConfigFields []configField `json:"ConfigFields,omitempty"` }
type configField struct { Name string `json:"Name"`; Type string `json:"Type"`; Description string `json:"Description"`; DefaultValue any `json:"DefaultValue"` }

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int { if host == nil || plugin == nil { return -1 }; C.store_host_api(host); C.set_plugin_api(plugin); return 0 }
//export autoPingPluginCall
func autoPingPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
    if response == nil { return -1 }
    name := ""; if method != nil { name = C.GoString(method) }
    requestBytes, ok := copyRequestBytes(request, requestLen); if !ok { return writeJSON(response, failEnvelope("invalid_request", "invalid request length")) }
    switch name {
    case "plugin.register", "plugin.reconfigure":
        var req registerRequest; if len(requestBytes) > 0 { if err := json.Unmarshal(requestBytes, &req); err != nil { return writeJSON(response, failEnvelope("invalid_request", "invalid plugin configuration envelope")) } }
        cfg, err := parseConfig(req.ConfigYAML); if err != nil { return writeJSON(response, failEnvelope("invalid_config", err.Error())) }; applyConfig(cfg); return writeJSON(response, okEnvelope(registrationResult()))
    case "management.register": return writeJSON(response, okEnvelope(managementRegistrationResult()))
    case "management.handle": return writeJSON(response, handleManagement(requestBytes))
    case "plugin.shutdown": stopScheduler(); return writeJSON(response, okEnvelope(map[string]any{"status":"stopped"}))
    default: return writeJSON(response, failEnvelope("unsupported_method", "unsupported plugin method"))
    }
}
//export autoPingPluginFreeBuffer
func autoPingPluginFreeBuffer(ptr unsafe.Pointer, length C.size_t) { _ = length; C.free(ptr) }
//export autoPingPluginShutdown
func autoPingPluginShutdown() { stopScheduler() }

func registrationResult() registerResult {
    cfg := defaultConfig(); return registerResult{SchemaVersion:5, Metadata:metadata{Name:pluginName, Version:version, Author:"jiz4oh", GitHubRepository:"https://github.com/jiz4oh/cpa-plugin-codex-auto-ping", Description:"Reliably ping every enabled Codex OAuth account at configured times using gpt-5.6-luna.", ConfigFields:[]configField{{Name:"timezone",Type:"string",Description:"IANA timezone used by the scheduler.",DefaultValue:cfg.Timezone},{Name:"times",Type:"string",Description:"Daily HH:MM times. YAML list is recommended.",DefaultValue:strings.Join(cfg.Times,",")}}}, Capabilities:registerCapabilities{ManagementAPI:true}}
}
func managementRegistrationResult() managementRegistration { return managementRegistration{Routes:[]managementRoute{{Method:"GET",Path:"/plugins/codex-auto-ping/status",Description:"Return scheduler, account, and last-run status as JSON."},{Method:"POST",Path:"/plugins/codex-auto-ping/run",Description:"Force-run Codex Auto Ping immediately."}}, Resources:[]resourceRoute{{Path:"/status",Menu:"Codex Auto Ping",Description:"Show scheduler, per-account status, and Run Now."}}} }
func handleManagement(raw []byte) []byte {
    var req managementRequest; if json.Unmarshal(raw,&req)!=nil { return failEnvelope("invalid_request","invalid management request") }; method:=strings.ToUpper(strings.TrimSpace(req.Method)); path:=strings.TrimSpace(req.Path)
    switch {
    case method=="GET" && strings.Contains(path,"/v0/resource/plugins/") && strings.HasSuffix(path,"/status"):
        return okEnvelope(managementResponse{StatusCode:200,Headers:map[string][]string{"content-type":{"text/html; charset=utf-8"},"cache-control":{"no-store"}},Body:[]byte(renderStatusPage(statusSnapshot()))})
    case method=="GET" && strings.HasSuffix(path,"/plugins/codex-auto-ping/status"):
        body,_:=json.Marshal(statusSnapshot()); return okEnvelope(managementResponse{StatusCode:200,Headers:map[string][]string{"content-type":{"application/json; charset=utf-8"},"cache-control":{"no-store"}},Body:body})
    case method=="POST" && strings.HasSuffix(path,"/plugins/codex-auto-ping/run"):
        if !startManualRun() { body,_:=json.Marshal(map[string]any{"accepted":false,"running":true,"message":"a run is already in progress"}); return okEnvelope(managementResponse{StatusCode:409,Headers:map[string][]string{"content-type":{"application/json; charset=utf-8"}},Body:body}) }
        body,_:=json.Marshal(map[string]any{"accepted":true,"running":true,"force":true}); return okEnvelope(managementResponse{StatusCode:202,Headers:map[string][]string{"content-type":{"application/json; charset=utf-8"}},Body:body})
    default: body,_:=json.Marshal(map[string]string{"error":"not found"}); return okEnvelope(managementResponse{StatusCode:404,Headers:map[string][]string{"content-type":{"application/json; charset=utf-8"}},Body:body})
    }
}

func renderStatusPage(s statusResponse) string {
    next:="-"; if s.NextRun!=nil { next=s.NextRun.Format(time.RFC3339) }; lastAt,mode,totals,lastErr:="-","-","-","-"; if s.LastRun!=nil { lastAt=s.LastRun.At.Format(time.RFC3339); mode=s.LastRun.Mode; totals=fmt.Sprintf("total=%d attempted=%d ok=%d limited=%d failed=%d skipped=%d",s.LastRun.Total,s.LastRun.Attempted,s.LastRun.Succeeded,s.LastRun.Limited,s.LastRun.Failed,s.LastRun.Skipped); if s.LastRun.Error!="" { lastErr=s.LastRun.Error } }
    var rows strings.Builder; for _,a:=range s.Accounts { eligible,reset,success:="-","-","-"; if !a.EligibleAt.IsZero(){eligible=a.EligibleAt.Format(time.RFC3339)}; if !a.ResetsAt.IsZero(){reset=a.ResetsAt.Format(time.RFC3339)}; if !a.LastSuccess.IsZero(){success=a.LastSuccess.Format(time.RFC3339)}; fmt.Fprintf(&rows,"<tr><td>%s</td><td>%s</td><td>%t</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td></tr>",html.EscapeString(a.Name),html.EscapeString(a.Status),a.Unavailable,a.Attempts,html.EscapeString(success),html.EscapeString(eligible),html.EscapeString(reset)) }
    return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Codex Auto Ping</title><style>body{font-family:system-ui,-apple-system,sans-serif;max-width:980px;margin:40px auto;padding:0 20px;color:#222}h1{font-size:24px}table{border-collapse:collapse;width:100%%;margin:20px 0;font-size:14px}td,th{padding:8px 10px;border-bottom:1px solid #ddd;text-align:left}.actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:16px}input{padding:9px 10px;min-width:280px}button{padding:9px 14px;cursor:pointer}.hint{font-size:13px;color:#666;margin-top:8px}pre{white-space:pre-wrap;background:#f6f6f6;padding:12px;border-radius:6px}</style></head><body><h1>Codex Auto Ping</h1><table><tr><td>Enabled</td><td>%t</td></tr><tr><td>Version</td><td>%s</td></tr><tr><td>Model</td><td>%s</td></tr><tr><td>Timezone</td><td>%s</td></tr><tr><td>Schedule</td><td>%s</td></tr><tr><td>Window guard</td><td>%ds after 5h</td></tr><tr><td>Retry</td><td>max %d attempts, exponential backoff from %ds</td></tr><tr><td>Next run</td><td>%s</td></tr><tr><td>Running</td><td>%t</td></tr><tr><td>Last run</td><td>%s</td></tr><tr><td>Mode</td><td>%s</td></tr><tr><td>Result</td><td>%s</td></tr><tr><td>Error</td><td>%s</td></tr></table><h2>Accounts</h2><table><thead><tr><th>Account</th><th>Status</th><th>CPA unavailable</th><th>Attempts</th><th>Last success</th><th>Eligible at</th><th>Reset at (informational)</th></tr></thead><tbody>%s</tbody></table><div class="actions"><input id="management-key" type="password" autocomplete="off" placeholder="Management Key"><button id="run" onclick="runNow()">Run Now (Force)</button></div><div class="hint">Run Now ignores last_success and cached reset times. Scheduled runs respect the 5h window; resets_at is informational only.</div><pre id="result"></pre><script>async function runNow(){const b=document.getElementById('run'),o=document.getElementById('result'),k=document.getElementById('management-key').value.trim();if(!k){o.textContent='Management Key is required.';return}b.disabled=true;o.textContent='Starting forced run...';try{const r=await fetch('/v0/management/plugins/codex-auto-ping/run',{method:'POST',headers:{'Authorization':'Bearer '+k}});const t=await r.text();o.textContent=t;if(r.ok)setTimeout(()=>location.reload(),2000)}catch(e){o.textContent=String(e)}finally{b.disabled=false}}</script></body></html>`,s.Enabled,html.EscapeString(s.Version),html.EscapeString(s.Model),html.EscapeString(s.Timezone),html.EscapeString(strings.Join(s.Times," / ")),s.GuardSeconds,s.MaxAttempts,s.RetryBaseSeconds,html.EscapeString(next),s.Running,html.EscapeString(lastAt),html.EscapeString(mode),html.EscapeString(totals),html.EscapeString(lastErr),rows.String())
}
func statusSnapshot() statusResponse {
    runtimeMu.Lock(); cfg:=currentConfig; cfg.Times=append([]string(nil),currentConfig.Times...); runtimeMu.Unlock(); stateMu.RLock(); defer stateMu.RUnlock(); var next *time.Time; if !nextRunAt.IsZero(){t:=nextRunAt;next=&t}; var last *runSummary; if lastRun!=nil{c:=*lastRun;c.Accounts=append([]accountRunResult(nil),lastRun.Accounts...);last=&c}; accounts:=make([]accountState,0,len(accountStates)); for _,a:=range accountStates{accounts=append(accounts,a)}; sort.Slice(accounts,func(i,j int)bool{return accounts[i].Name<accounts[j].Name}); return statusResponse{Enabled:cfg.Enabled,Version:version,Model:modelName,Timezone:cfg.Timezone,Times:cfg.Times,WindowSeconds:int64(windowInterval/time.Second),GuardSeconds:int64(windowGuard/time.Second),MaxAttempts:maxAttempts,RetryBaseSeconds:int64(retryBaseDelay/time.Second),NextRun:next,Running:running,LastRun:last,Accounts:accounts}
}

func defaultConfig() config { return config{Enabled:true,Timezone:defaultTZ,Times:append([]string(nil),defaultTimes...)} }
func applyConfig(cfg config) { runtimeMu.Lock(); defer runtimeMu.Unlock(); if schedulerCancel!=nil{schedulerCancel();schedulerCancel=nil}; currentConfig=cfg; if !cfg.Enabled{setNextRun(time.Time{});logf("disabled by CPA config");return}; ctx,cancel:=context.WithCancel(context.Background()); schedulerCancel=cancel; go schedulerLoop(ctx,cfg) }
func stopScheduler(){ runtimeMu.Lock(); defer runtimeMu.Unlock(); if schedulerCancel!=nil{schedulerCancel();schedulerCancel=nil}; setNextRun(time.Time{}) }
func schedulerLoop(ctx context.Context,cfg config){loc,err:=time.LoadLocation(cfg.Timezone);if err!=nil{logf("invalid timezone %q: %v",cfg.Timezone,err);return};for{next:=nextRun(time.Now().In(loc),loc,cfg.Times);setNextRun(next);logf("next scheduled run at %s model=%s",next.Format(time.RFC3339),modelName);timer:=time.NewTimer(time.Until(next));select{case<-ctx.Done():if !timer.Stop(){select{case<-timer.C:default:}};return;case<-timer.C:runScheduled(ctx)}}}
func nextRun(now time.Time,loc *time.Location,times []string)time.Time{best:=time.Time{};for _,v:=range times{h,m,_:=parseClock(v);c:=time.Date(now.Year(),now.Month(),now.Day(),h,m,0,0,loc);if !c.After(now){c=c.AddDate(0,0,1)};if best.IsZero()||c.Before(best){best=c}};return best}
func setNextRun(t time.Time){stateMu.Lock();nextRunAt=t;stateMu.Unlock()}
func claimRun()bool{stateMu.Lock();defer stateMu.Unlock();if running{return false};running=true;return true}
func startManualRun()bool{if !claimRun(){return false};go runClaimed(context.Background(),true);return true}
func runScheduled(parent context.Context){if !claimRun(){logf("scheduled run skipped: another run is already in progress");return};runClaimed(parent,false)}

func runClaimed(parent context.Context, force bool) {
    mode:="scheduled"; if force{mode="force"}; summary:=runSummary{At:time.Now(),Mode:mode}; defer func(){stateMu.Lock();running=false;lastRun=&summary;stateMu.Unlock()}(); ctx,cancel:=context.WithTimeout(parent,runTimeout);defer cancel(); files,err:=listAuth(ctx);if err!=nil{summary.Error="auth list failed: "+err.Error();logf("%s",summary.Error);return}
    var targets []authFile; for _,a:=range files{if !isCodex(a){continue};summary.Total++;if a.Disabled{r:=accountRunResult{AuthIndex:a.AuthIndex,Name:safeName(a),Email:a.Email,Unavailable:a.Unavailable,Status:"skipped_disabled"};summary.Skipped++;summary.Accounts=append(summary.Accounts,r);updateAccountState(a,r,time.Time{});continue};if a.AuthIndex==""{r:=accountRunResult{Name:safeName(a),Email:a.Email,Unavailable:a.Unavailable,Status:"skipped_missing_auth_index",Error:"missing auth_index"};summary.Skipped++;summary.Accounts=append(summary.Accounts,r);continue};targets=append(targets,a)};logf("auth scan mode=%s codex=%d targets=%d",mode,summary.Total,len(targets))
    results:=make(chan accountRunResult,len(targets));var wg sync.WaitGroup;for _,a:=range targets{wg.Add(1);go func(auth authFile){defer wg.Done();results<-runAccount(ctx,auth,force)}(a)};go func(){wg.Wait();close(results)}();firstError:="";for r:=range results{summary.Accounts=append(summary.Accounts,r);switch r.Status{case"success":summary.Attempted++;summary.Succeeded++;case"limited":if r.Attempts>0{summary.Attempted++};summary.Limited++;case"deferred":summary.Skipped++;default:if r.Attempts>0{summary.Attempted++};summary.Failed++;if firstError==""{firstError=r.Error}}};sort.Slice(summary.Accounts,func(i,j int)bool{return summary.Accounts[i].Name<summary.Accounts[j].Name});if summary.Failed>0{summary.Error=firstError};logf("run complete mode=%s total=%d attempted=%d succeeded=%d limited=%d failed=%d skipped=%d",mode,summary.Total,summary.Attempted,summary.Succeeded,summary.Limited,summary.Failed,summary.Skipped)
}

func runAccount(ctx context.Context,a authFile,force bool)accountRunResult{
    base:=accountRunResult{AuthIndex:a.AuthIndex,Name:safeName(a),Email:a.Email,Unavailable:a.Unavailable};state:=getAccountState(a.AuthIndex);eligible:=time.Time{}
    if !force && !state.LastSuccess.IsZero(){eligible=state.LastSuccess.Add(windowInterval+windowGuard);now:=time.Now();if now.Before(eligible){base.EligibleAt=timePtr(eligible);wait:=time.Until(eligible);if deadline,ok:=ctx.Deadline();ok&&time.Now().Add(wait).After(deadline){base.Status="deferred";base.Error="next window is outside this run timeout";updateAccountState(a,base,eligible);logf("defer auth=%s eligible_at=%s",safeName(a),eligible.Format(time.RFC3339));return base};logf("wait auth=%s duration=%s eligible_at=%s",safeName(a),wait.Round(time.Second),eligible.Format(time.RFC3339));timer:=time.NewTimer(wait);select{case<-ctx.Done():timer.Stop();base.Status="failed";base.Error=ctx.Err().Error();updateAccountState(a,base,eligible);return base;case<-timer.C:}}}
    if force{logf("force ping auth=%s cached_reset=%s last_success=%s",safeName(a),formatTime(state.ResetsAt),formatTime(state.LastSuccess))}
    for attempt:=1;attempt<=maxAttempts;attempt++{base.Attempts=attempt;attemptCtx,cancel:=context.WithTimeout(ctx,attemptTimeout);outcome:=pingAuthOnce(attemptCtx,a);cancel();base.HTTPStatus=outcome.HTTPStatus;base.Error=outcome.Error;if !outcome.ResetsAt.IsZero(){base.ResetsAt=timePtr(outcome.ResetsAt)};if outcome.Status=="success"{base.Status="success";successAt:=time.Now();base.EligibleAt=timePtr(successAt.Add(windowInterval+windowGuard));updateAccountStateSuccess(a,base,successAt);logf("ping ok auth=%s mode=%s attempt=%d next_eligible=%s",safeName(a),map[bool]string{true:"force",false:"scheduled"}[force],attempt,base.EligibleAt.Format(time.RFC3339));return base};if outcome.Status=="limited"{base.Status="limited";updateAccountState(a,base,eligible);logf("ping limited auth=%s mode=%s attempt=%d reset_at=%s",safeName(a),map[bool]string{true:"force",false:"scheduled"}[force],attempt,formatTime(outcome.ResetsAt));return base};if !outcome.Retryable||attempt==maxAttempts{base.Status="failed";updateAccountState(a,base,eligible);logf("ping failed auth=%s attempt=%d/%d http=%d retryable=%t: %s",safeName(a),attempt,maxAttempts,outcome.HTTPStatus,outcome.Retryable,outcome.Error);return base};delay:=retryDelay(attempt);logf("ping retry auth=%s attempt=%d/%d in=%s http=%d: %s",safeName(a),attempt,maxAttempts,delay,outcome.HTTPStatus,outcome.Error);timer:=time.NewTimer(delay);select{case<-ctx.Done():timer.Stop();base.Status="failed";base.Error=ctx.Err().Error();updateAccountState(a,base,eligible);return base;case<-timer.C:}}
    base.Status="failed";base.Error="retry loop exhausted";updateAccountState(a,base,eligible);return base
}
func retryDelay(failedAttempt int)time.Duration{if failedAttempt<1{failedAttempt=1};return retryBaseDelay*time.Duration(1<<uint(failedAttempt-1))}

func pingAuthOnce(ctx context.Context,a authFile)pingOutcome{raw,err:=getAuth(ctx,a.AuthIndex);if err!=nil{return pingOutcome{Status:"failed",Retryable:true,Error:"auth get: "+err.Error()}};m,err:=parseAuthMaterial(raw);if err!=nil{return pingOutcome{Status:"failed",Retryable:false,Error:err.Error()}};body,_:=json.Marshal(codexBody{Model:modelName,Instructions:"You are a helpful assistant.",Input:[]codexMessage{{Type:"message",Role:"user",Content:[]codexPart{{Type:"input_text",Text:defaultPrompt}}}},Store:false,Stream:true});headers:=map[string][]string{"Accept":{"text/event-stream"},"Authorization":{"Bearer "+m.AccessToken},"Content-Type":{"application/json"},"OpenAI-Beta":{"responses=v1"},"originator":{"codex_cli_rs"},"User-Agent":{"codex_cli_rs/0.76.0"}};if m.AccountID!=""{headers["Chatgpt-Account-Id"]=[]string{m.AccountID}};var resp httpResponse;if err:=callHost(ctx,"host.http.do",httpRequest{Method:"POST",URL:codexURL,Headers:headers,Body:body},&resp);err!=nil{return pingOutcome{Status:"failed",Retryable:true,Error:err.Error()}};if resp.StatusCode>=200&&resp.StatusCode<=299{return pingOutcome{Status:"success",HTTPStatus:resp.StatusCode}};typ,msg,reset:=parseUpstreamError(resp.Body);if typ=="usage_limit_reached"{if msg==""{msg="usage limit reached"};return pingOutcome{Status:"limited",HTTPStatus:resp.StatusCode,Retryable:false,Error:msg,ResetsAt:reset}};if msg==""{msg=fmt.Sprintf("upstream HTTP %d",resp.StatusCode)};if typ!=""{msg=typ+": "+msg};return pingOutcome{Status:"failed",HTTPStatus:resp.StatusCode,Retryable:isRetryableHTTP(resp.StatusCode),Error:msg,ResetsAt:reset}}
func parseUpstreamError(body []byte)(string,string,time.Time){if len(body)==0{return"","",time.Time{}};var p upstreamErrorEnvelope;if json.Unmarshal(body,&p)!=nil{return"","",time.Time{}};reset:=time.Time{};if p.Error.ResetsAt>0{reset=time.Unix(p.Error.ResetsAt,0)}else if p.Error.ResetsInSeconds>0{reset=time.Now().Add(time.Duration(p.Error.ResetsInSeconds)*time.Second)};return strings.TrimSpace(p.Error.Type),strings.TrimSpace(p.Error.Message),reset}
func isRetryableHTTP(code int)bool{switch code{case 408,425,429,500,502,503,504:return true;default:return false}}
func getAccountState(authIndex string)accountState{stateMu.RLock();defer stateMu.RUnlock();return accountStates[authIndex]}
func updateAccountState(a authFile,r accountRunResult,eligible time.Time){stateMu.Lock();defer stateMu.Unlock();s:=accountStates[a.AuthIndex];s.AuthIndex,s.Name,s.Email,s.Unavailable=a.AuthIndex,safeName(a),a.Email,a.Unavailable;s.Status,s.Attempts,s.Error,s.LastAttempt=r.Status,r.Attempts,r.Error,time.Now();if !eligible.IsZero(){s.EligibleAt=eligible};if r.EligibleAt!=nil{s.EligibleAt=*r.EligibleAt};if r.ResetsAt!=nil{s.ResetsAt=*r.ResetsAt}else if r.Status!="limited"{s.ResetsAt=time.Time{}};accountStates[a.AuthIndex]=s}
func updateAccountStateSuccess(a authFile,r accountRunResult,successAt time.Time){updateAccountState(a,r,successAt.Add(windowInterval+windowGuard));stateMu.Lock();s:=accountStates[a.AuthIndex];s.LastSuccess=successAt;s.ResetsAt=time.Time{};accountStates[a.AuthIndex]=s;stateMu.Unlock()}
func timePtr(t time.Time)*time.Time{return &t};func formatTime(t time.Time)string{if t.IsZero(){return"-"};return t.Format(time.RFC3339)}
func isCodex(a authFile)bool{p:=strings.ToLower(strings.TrimSpace(a.Provider));t:=strings.ToLower(strings.TrimSpace(a.Type));n:=strings.ToLower(strings.TrimSpace(a.Name));return p=="codex"||t=="codex"||strings.Contains(p,"codex")||strings.Contains(t,"codex")||strings.Contains(n,"codex")}
func listAuth(ctx context.Context)([]authFile,error){var resp struct{Files []authFile `json:"files"`};if err:=callHost(ctx,"host.auth.list",map[string]any{},&resp);err!=nil{return nil,err};return resp.Files,nil}
func getAuth(ctx context.Context,authIndex string)([]byte,error){var resp struct{AuthIndex string `json:"auth_index"`;Name string `json:"name"`;JSON json.RawMessage `json:"json"`};if err:=callHost(ctx,"host.auth.get",map[string]any{"auth_index":authIndex},&resp);err!=nil{return nil,err};if len(resp.JSON)==0{return nil,fmt.Errorf("empty auth JSON")};return resp.JSON,nil}
func parseAuthMaterial(raw []byte)(authMaterial,error){var root map[string]json.RawMessage;if err:=json.Unmarshal(raw,&root);err!=nil{return authMaterial{},fmt.Errorf("invalid auth JSON")};token:=firstString(root,"access_token","accessToken","oauth_access_token","oauthAccessToken","token","id_token","idToken");accountID:=firstString(root,"account_id","chatgpt_account_id","accountId","chatgptAccountId");for _,key:=range[]string{"tokens","credentials","auth","oauth","session"}{var nested map[string]json.RawMessage;if b,ok:=root[key];ok&&json.Unmarshal(b,&nested)==nil{if token==""{token=firstString(nested,"access_token","accessToken","oauth_access_token","oauthAccessToken","token","id_token","idToken")};if accountID==""{accountID=firstString(nested,"account_id","chatgpt_account_id","accountId","chatgptAccountId")}}};if token==""{return authMaterial{},fmt.Errorf("missing access token")};return authMaterial{AccessToken:token,AccountID:accountID},nil}
func firstString(m map[string]json.RawMessage,keys ...string)string{for _,k:=range keys{raw,ok:=m[k];if !ok{continue};var s string;if json.Unmarshal(raw,&s)==nil&&strings.TrimSpace(s)!=""{return strings.TrimSpace(s)}};return""}

func parseConfig(raw string)(config,error){cfg:=defaultConfig();text:=strings.TrimSpace(raw);if text==""{return validateConfig(cfg)};if strings.HasPrefix(text,"{"){var p struct{Enabled *bool `json:"enabled"`;Timezone string `json:"timezone"`;Times []string `json:"times"`};if err:=json.Unmarshal([]byte(text),&p);err!=nil{return config{},fmt.Errorf("invalid JSON config: %w",err)};if p.Enabled!=nil{cfg.Enabled=*p.Enabled};if strings.TrimSpace(p.Timezone)!=""{cfg.Timezone=strings.TrimSpace(p.Timezone)};if len(p.Times)>0{cfg.Times=p.Times};return validateConfig(cfg)};lines:=strings.Split(text,"\n");inTimes:=false;var parsed []string;for _,rawLine:=range lines{line:=strings.TrimSpace(strings.SplitN(rawLine,"#",2)[0]);if line==""{continue};if strings.HasPrefix(line,"-")&&inTimes{parsed=append(parsed,unquote(strings.TrimSpace(strings.TrimPrefix(line,"-"))));continue};parts:=strings.SplitN(line,":",2);if len(parts)!=2{continue};key,value:=strings.TrimSpace(parts[0]),strings.TrimSpace(parts[1]);inTimes=false;switch key{case"enabled":if value!=""{b,err:=strconv.ParseBool(unquote(value));if err!=nil{return config{},fmt.Errorf("enabled must be true or false")};cfg.Enabled=b};case"timezone":if value!=""{cfg.Timezone=unquote(value)};case"times":inTimes=true;if value!=""{parsed=parseInlineList(value);inTimes=false}}};if len(parsed)>0{cfg.Times=parsed};return validateConfig(cfg)}
func validateConfig(cfg config)(config,error){cfg.Timezone=strings.TrimSpace(cfg.Timezone);if cfg.Timezone==""{cfg.Timezone=defaultTZ};if _,err:=time.LoadLocation(cfg.Timezone);err!=nil{return config{},fmt.Errorf("invalid timezone %q",cfg.Timezone)};if len(cfg.Times)==0{return config{},fmt.Errorf("times must contain at least one HH:MM value")};seen:=map[string]bool{};norm:=make([]string,0,len(cfg.Times));for _,v:=range cfg.Times{v=strings.TrimSpace(unquote(v));h,m,err:=parseClock(v);if err!=nil{return config{},err};n:=fmt.Sprintf("%02d:%02d",h,m);if !seen[n]{seen[n]=true;norm=append(norm,n)}};cfg.Times=norm;return cfg,nil}
func parseClock(value string)(int,int,error){parts:=strings.Split(value,":");if len(parts)!=2{return 0,0,fmt.Errorf("invalid time %q: expected HH:MM",value)};h,e1:=strconv.Atoi(parts[0]);m,e2:=strconv.Atoi(parts[1]);if e1!=nil||e2!=nil||h<0||h>23||m<0||m>59{return 0,0,fmt.Errorf("invalid time %q: expected 00:00-23:59",value)};return h,m,nil}
func parseInlineList(value string)[]string{value=strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(value),"["),"]"));if value==""{return nil};parts:=strings.Split(value,",");out:=make([]string,0,len(parts));for _,p:=range parts{out=append(out,unquote(strings.TrimSpace(p)))};return out}
func unquote(value string)string{value=strings.TrimSpace(value);if len(value)>=2&&((value[0]=='"'&&value[len(value)-1]=='"')||(value[0]=='\''&&value[len(value)-1]=='\'')){return value[1:len(value)-1]};return value}

func copyRequestBytes(request *C.uint8_t,requestLen C.size_t)([]byte,bool){length:=int(requestLen);if length<0||C.size_t(length)!=requestLen{return nil,false};if length==0{return nil,true};if request==nil{return nil,false};s:=unsafe.Slice((*byte)(unsafe.Pointer(request)),length);return append([]byte(nil),s...),true}
func callHost(ctx context.Context,method string,payload any,target any)error{if err:=ctx.Err();err!=nil{return err};raw,err:=json.Marshal(payload);if err!=nil{return err};cm:=C.CString(method);defer C.free(unsafe.Pointer(cm));var req *C.uint8_t;var cPayload unsafe.Pointer;if len(raw)>0{cPayload=C.CBytes(raw);if cPayload==nil{return fmt.Errorf("allocation failure")};defer C.free(cPayload);req=(*C.uint8_t)(cPayload)};var response C.cliproxy_buffer;code:=C.call_host_api(cm,req,C.size_t(len(raw)),&response);data:=copyHostResponse(response);if response.ptr!=nil{C.free_host_buffer(unsafe.Pointer(response.ptr),response.len)};if len(data)==0{return fmt.Errorf("host callback %s empty response code=%d",method,int(code))};var env struct{OK bool `json:"ok"`;Result json.RawMessage `json:"result"`;Error *struct{Code string `json:"code"`;Message string `json:"message"`} `json:"error"`};if err:=json.Unmarshal(data,&env);err!=nil{return fmt.Errorf("decode host response: %w",err)};if !env.OK{if env.Error!=nil{return fmt.Errorf("%s: %s",env.Error.Code,env.Error.Message)};return fmt.Errorf("host callback failed")};if code!=0{return fmt.Errorf("host callback code=%d",int(code))};if target!=nil&&len(env.Result)!=0{return json.Unmarshal(env.Result,target)};return nil}
func copyHostResponse(r C.cliproxy_buffer)[]byte{if r.ptr==nil||r.len==0{return nil};return C.GoBytes(unsafe.Pointer(r.ptr),C.int(r.len))}
func okEnvelope(result any)[]byte{b,_:=json.Marshal(map[string]any{"ok":true,"result":result});return b};func failEnvelope(code,message string)[]byte{b,_:=json.Marshal(map[string]any{"ok":false,"error":map[string]any{"code":code,"message":message,"retryable":false}});return b}
func writeJSON(response *C.cliproxy_buffer,data []byte)C.int{if len(data)==0{response.ptr=nil;response.len=0;return 0};ptr:=C.malloc(C.size_t(len(data)));if ptr==nil{return -1};C.memcpy(ptr,unsafe.Pointer(&data[0]),C.size_t(len(data)));response.ptr=(*C.uint8_t)(ptr);response.len=C.size_t(len(data));return 0}
func safeName(a authFile)string{if a.Name!=""{return a.Name};if a.ID!=""{return a.ID};return a.AuthIndex};func logf(format string,args ...any){fmt.Printf("[codex-auto-ping] "+format+"\n",args...)}