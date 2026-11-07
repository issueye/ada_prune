package command_code

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
)

// Model is one catalogue entry: the upstream name, a display label and the
// cheapest subscription tier that can use it.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Plan string `json:"plan"`
}

// BuiltinModels is the embedded catalogue. The upstream can also list models
// dynamically, but only for plans above the entry tier, so this list is what most
// operators actually see.
//
// It is a candidate list, not a set of routable models: nothing becomes callable
// until an operator imports it into the model table.
var BuiltinModels = []Model{
	{ID: "deepseek/deepseek-v4-pro", Name: "DeepSeek V4 Pro", Plan: "go"},
	{ID: "deepseek/deepseek-v4-flash", Name: "DeepSeek V4 Flash", Plan: "go"},
	{ID: "deepseek/deepseek-v4-flash-vision-exp", Name: "DeepSeek V4 Flash Vision", Plan: "go"},
	{ID: "deepseek/deepseek-v4-flash-fast", Name: "DeepSeek V4 Flash Fast", Plan: "go"},
	{ID: "deepseek/deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", Plan: "go"},
	{ID: "moonshotai/Kimi-K3", Name: "Kimi K3", Plan: "go"},
	{ID: "moonshotai/Kimi-K2.7-Code", Name: "Kimi K2.7 Code", Plan: "go"},
	{ID: "moonshotai/Kimi-K2.7-Code-Highspeed", Name: "Kimi K2.7 Code HighSpeed", Plan: "go"},
	{ID: "moonshotai/Kimi-K2.6", Name: "Kimi K2.6", Plan: "go"},
	{ID: "moonshotai/Kimi-K2.5", Name: "Kimi K2.5", Plan: "go"},
	{ID: "z-ai/glm-5.3-flash", Name: "GLM-5.3 Flash", Plan: "go"},
	{ID: "z-ai/glm-5.3-flashx", Name: "GLM-5.3 FlashX", Plan: "go"},
	{ID: "zai-org/GLM-5.3", Name: "GLM-5.3", Plan: "go"},
	{ID: "zai-org/GLM-5.2", Name: "GLM-5.2", Plan: "go"},
	{ID: "zai-org/GLM-5.2-Fast", Name: "GLM-5.2 Fast", Plan: "go"},
	{ID: "zai-org/GLM-5.1", Name: "GLM-5.1", Plan: "go"},
	{ID: "zai-org/GLM-5", Name: "GLM-5", Plan: "go"},
	{ID: "MiniMaxAI/MiniMax-M3", Name: "MiniMax M3", Plan: "go"},
	{ID: "MiniMaxAI/MiniMax-M2.7", Name: "MiniMax M2.7", Plan: "go"},
	{ID: "MiniMaxAI/MiniMax-M2.5", Name: "MiniMax M2.5", Plan: "go"},
	{ID: "xiaomi/mimo-v2.5-pro", Name: "MiMo V2.5 Pro", Plan: "go"},
	{ID: "xiaomi/mimo-v2.5", Name: "MiMo V2.5", Plan: "go"},
	{ID: "Qwen/Qwen3.8-Omni-Flash", Name: "Qwen 3.8 Omni Flash", Plan: "go"},
	{ID: "Qwen/Qwen3.8-Max-0902", Name: "Qwen 3.8 Max 0902", Plan: "go"},
	{ID: "Qwen/Qwen3.8-Max", Name: "Qwen 3.8 Max", Plan: "go"},
	{ID: "Qwen/Qwen3.8-27B", Name: "Qwen 3.8 27B", Plan: "go"},
	{ID: "Qwen/Qwen3.8-Flash", Name: "Qwen 3.8 Flash", Plan: "go"},
	{ID: "Qwen/Qwen3.7-Max", Name: "Qwen 3.7 Max", Plan: "go"},
	{ID: "Qwen/Qwen3.7-Plus", Name: "Qwen 3.7 Plus", Plan: "go"},
	{ID: "Qwen/Qwen3.7-Flash", Name: "Qwen 3.7 Flash", Plan: "go"},
	{ID: "Qwen/Qwen3.6-Max-Preview", Name: "Qwen 3.6 Max Preview", Plan: "go"},
	{ID: "Qwen/Qwen3.6-Plus", Name: "Qwen 3.6 Plus", Plan: "go"},
	{ID: "meituan/LongCat-2.0", Name: "LongCat 2.0", Plan: "go"},
	{ID: "stepfun/Step-3.7-Flash", Name: "Step 3.7 Flash", Plan: "go"},
	{ID: "stepfun/Step-3.5-Flash", Name: "Step 3.5 Flash", Plan: "go"},
	{ID: "tencent/hy3-paid", Name: "Tencent Hy3", Plan: "go"},
	{ID: "tencent/hy4-preview", Name: "Tencent Hy4 Preview", Plan: "go"},
	{ID: "nvidia/nemotron-3-ultra-550b-a55b", Name: "Nemotron 3 Ultra", Plan: "go"},
	{ID: "thinkingmachines/inkling", Name: "Inkling", Plan: "go"},
	{ID: "thinkingmachines/inkling-small", Name: "Inkling Small", Plan: "go"},
	{ID: "poolside/laguna-s-2.1-free", Name: "Laguna S 2.1", Plan: "go"},
	{ID: "inclusionai/ling-3.0-flash-sante:free", Name: "Ling 3.0 Flash Sante", Plan: "go"},
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5", Plan: "pro"},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", Plan: "pro"},
	{ID: "claude-fable-5-1", Name: "Claude Fable 5.1", Plan: "max"},
	{ID: "claude-fable-5", Name: "Claude Fable 5", Plan: "max"},
	{ID: "claude-opus-5", Name: "Claude Opus 5", Plan: "max"},
	{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Plan: "max"},
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7", Plan: "max"},
	{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", Plan: "pro"},
	{ID: "gpt-6-astra", Name: "GPT-6 Astra", Plan: "max"},
	{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Plan: "goat"},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Plan: "pro"},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Plan: "go"},
	{ID: "gpt-5.5", Name: "GPT-5.5", Plan: "pro"},
	{ID: "gpt-5.4", Name: "GPT-5.4", Plan: "pro"},
	{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex", Plan: "pro"},
	{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini", Plan: "pro"},
	{ID: "google/gemini-3.8-flash", Name: "Gemini 3.8 Flash", Plan: "goat"},
	{ID: "google/gemini-3.7-flash", Name: "Gemini 3.7 Flash", Plan: "goat"},
	{ID: "google/gemini-3.6-flash", Name: "Gemini 3.6 Flash", Plan: "pro"},
	{ID: "google/gemini-3.5-flash", Name: "Gemini 3.5 Flash", Plan: "pro"},
	{ID: "google/gemini-3.5-flash-lite", Name: "Gemini 3.5 Flash Lite", Plan: "pro"},
	{ID: "google/gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash Lite", Plan: "pro"},
	{ID: "sakana/fugu-ultra", Name: "Fugu Ultra", Plan: "max"},
	{ID: "meta/muse-spark-1.1", Name: "Muse Spark 1.1", Plan: "pro"},
	{ID: "meta/muse-spark-1.2", Name: "Muse Spark 1.2", Plan: "goat"},
	{ID: "meta/muse-spark-1.2-contributor", Name: "Muse Spark 1.2 Contributor", Plan: "go"},
	{ID: "meta/muse-spark-1.3", Name: "Muse Spark 1.3", Plan: "goat"},
	{ID: "meta/muse-spark-1.3-contributor", Name: "Muse Spark 1.3 Contributor", Plan: "go"},
	{ID: "xai/grok-4.5", Name: "Grok 4.5", Plan: "go"},
	{ID: "xai/grok-4.6", Name: "Grok 4.6", Plan: "goat"},
}

// PlanOf returns the cheapest plan a builtin model needs.
func PlanOf(id string) (string, bool) {
	for _, m := range BuiltinModels {
		if m.ID == id {
			return m.Plan, true
		}
	}
	return "", false
}

// catalogCache holds the dynamically fetched list between refreshes.
type catalogCache struct {
	mu        sync.Mutex
	models    []Model
	fetchedAt time.Time
	source    string
}

// catalogTTL is how long a fetched list is trusted.
const catalogTTL = 5 * time.Minute

// DiscoverModels implements gateway.ModelDiscovery.
func (a *Adapter) DiscoverModels(ctx context.Context, in gateway.ProbeRequest) ([]gateway.DiscoveredModel, error) {
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return nil, err
	}
	models, _, err := a.fetchModels(ctx, cfg, in.Provider, in.Credential)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.DiscoveredModel, 0, len(models))
	for _, m := range models {
		meta := map[string]string{}
		if m.Plan != "" {
			meta["plan"] = m.Plan
		}
		out = append(out, gateway.DiscoveredModel{
			UpstreamName: m.ID,
			DisplayName:  m.Name,
			Meta:         meta,
		})
	}
	return out, nil
}

// fetchModels returns the candidate model list and where it came from. The
// embedded list is the fallback in hybrid mode and the only source in builtin
// mode.
func (a *Adapter) fetchModels(ctx context.Context, cfg Config, p gateway.ProviderConfig,
	cred gateway.Credential) ([]Model, string, error) {
	a.catalog.mu.Lock()
	cached := a.catalog.models
	fetchedAt := a.catalog.fetchedAt
	source := a.catalog.source
	a.catalog.mu.Unlock()

	if len(cached) > 0 && a.clock().Sub(fetchedAt) < catalogTTL {
		return cached, source, nil
	}

	if cfg.Catalog == CatalogBuiltin {
		return BuiltinModels, CatalogBuiltin, nil
	}

	client := a.httpClient(cfg, p)
	models, err := fetchModelsHTTP(ctx, client, apiBaseOf(cfg, p), cred.Secret, cfg.ProjectDir, cfg.ProtocolVersion)
	if err != nil {
		if cfg.Catalog == CatalogDynamic {
			return nil, "", err
		}
		// Hybrid falls back to the embedded list rather than failing the page.
		return BuiltinModels, CatalogBuiltin, nil
	}

	a.catalog.mu.Lock()
	a.catalog.models = models
	a.catalog.fetchedAt = a.clock()
	a.catalog.source = CatalogDynamic
	a.catalog.mu.Unlock()
	return models, CatalogDynamic, nil
}

// fetchModelsHTTP performs the actual upstream call.
func fetchModelsHTTP(ctx context.Context, client *http.Client, apiBase, secret, projectDir, version string) ([]Model, error) {
	url := strings.TrimRight(apiBase, "/") + "/provider/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, values := range usageHeaders(secret, projectDir, version, nil) {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("command_code: model list returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("command_code: decode model list: %w", err)
	}
	models := make([]Model, 0, len(body.Data))
	for _, d := range body.Data {
		if d.ID == "" {
			continue
		}
		plan := ""
		if p, ok := PlanOf(d.ID); ok {
			plan = p
		}
		models = append(models, Model{ID: d.ID, Name: d.ID, Plan: plan})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("command_code: model list was empty")
	}
	return models, nil
}

// Catalogue returns the current candidate list and its source, for the control
// plane's import screen.
func (a *Adapter) Catalogue(ctx context.Context, cfg Config, p gateway.ProviderConfig,
	cred gateway.Credential) ([]Model, string) {
	models, source, err := a.fetchModels(ctx, cfg, p, cred)
	if err != nil {
		return BuiltinModels, CatalogBuiltin
	}
	return models, source
}
