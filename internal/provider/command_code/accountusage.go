package command_code

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
)

// planTotalCredits maps a plan id to its monthly allowance. Unknown plans report
// null and the UI falls back to the remaining balance alone.
var planTotalCredits = map[string]float64{
	"individual-go": 10, "individual-goat": 70, "individual-pro": 30, "individual-pro-v1": 80,
	"individual-provider": 15, "individual-max": 150, "individual-ultra": 300, "teams-pro": 40,
}

var planDisplay = map[string]string{
	"individual-go": "Go", "individual-goat": "GOAT", "individual-pro": "Pro", "individual-pro-v1": "Pro",
	"individual-provider": "Provider", "individual-max": "Max", "individual-ultra": "Ultra",
	"teams-pro": "Teams Pro",
}

// Whoami is the account identity.
type Whoami struct {
	UserName *string `json:"userName"`
	Email    *string `json:"email"`
	OrgID    *string `json:"orgId"`
}

// Plan is the subscription.
type Plan struct {
	ID                 *string  `json:"id"`
	Name               *string  `json:"name"`
	Status             *string  `json:"status"`
	CancelAtPeriodEnd  bool     `json:"cancelAtPeriodEnd"`
	CurrentPeriodStart *string  `json:"currentPeriodStart"`
	CurrentPeriodEnd   *string  `json:"currentPeriodEnd"`
	DaysRemaining      *int     `json:"daysRemaining"`
	MonthlyTotal       *float64 `json:"monthlyTotal"`
}

// Credits is the balance across the three pools.
type Credits struct {
	Monthly        float64  `json:"monthly"`
	Purchased      float64  `json:"purchased"`
	Free           float64  `json:"free"`
	TotalRemaining float64  `json:"totalRemaining"`
	BelowThreshold bool     `json:"belowThreshold"`
	MonthlyTotal   *float64 `json:"monthlyTotal"`
	MonthlyUsed    *float64 `json:"monthlyUsed"`
}

// Period is the accounting for the current billing period.
type Period struct {
	TotalCount     float64 `json:"totalCount"`
	CompletedCount float64 `json:"completedCount"`
	FailedCount    float64 `json:"failedCount"`
	SuccessRate    float64 `json:"successRate"`
	TotalCost      float64 `json:"totalCost"`
	AverageCost    float64 `json:"averageCost"`
	TotalTokensIn  float64 `json:"totalTokensIn"`
	TotalTokensOut float64 `json:"totalTokensOut"`
	TotalTokens    float64 `json:"totalTokens"`
	TotalCredits   float64 `json:"totalCredits"`
}

// UsageData is one credential's account snapshot.
type UsageData struct {
	Whoami  *Whoami  `json:"whoami"`
	Plan    *Plan    `json:"plan"`
	Credits *Credits `json:"credits"`
	Period  *Period  `json:"period"`
	// FetchedAt is when this snapshot was taken upstream.
	FetchedAt int64 `json:"fetchedAt"`
	// Error is set when part of the snapshot could not be fetched.
	Error string `json:"error,omitempty"`
}

// usageCache holds snapshots so the panel does not call the upstream on every
// render.
type usageCache struct {
	mu   sync.Mutex
	byID map[int64]usageEntry
}

type usageEntry struct {
	data      *UsageData
	fetchedAt time.Time
	err       string
}

// usageTTL is how long a snapshot is served without re-fetching.
const usageTTL = 5 * time.Minute

// AccountUsage implements gateway.AccountUsageProvider.
func (a *Adapter) AccountUsage(ctx context.Context, in gateway.ProbeRequest) (any, error) {
	return a.accountUsage(ctx, in, false)
}

// RefreshAccountUsage forces a fresh snapshot.
func (a *Adapter) RefreshAccountUsage(ctx context.Context, in gateway.ProbeRequest) (any, error) {
	return a.accountUsage(ctx, in, true)
}

func (a *Adapter) accountUsage(ctx context.Context, in gateway.ProbeRequest, force bool) (*UsageData, error) {
	a.usage.mu.Lock()
	entry, cached := a.usage.byID[in.Credential.ID]
	a.usage.mu.Unlock()
	if !force && cached && a.clock().Sub(entry.fetchedAt) < usageTTL {
		return entry.data, nil
	}

	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return nil, err
	}
	if in.Credential.Secret == "" {
		return nil, fmt.Errorf("command_code: no credential to fetch usage for")
	}

	client := a.httpClient(cfg, in.Provider)
	base := apiBaseOf(cfg, in.Provider)

	whoami, err := a.usageGet(ctx, client, base, cfg, in.Credential.Secret, "/alpha/whoami",
		map[string]string{"limits": "1"})
	if err != nil {
		a.cacheUsage(in.Credential.ID, nil, err.Error())
		return nil, err
	}
	var identity Whoami
	_ = json.Unmarshal(whoami, &identity)

	orgID := ""
	if identity.OrgID != nil {
		orgID = *identity.OrgID
	}
	query := map[string]string{"orgId": orgID}

	// credits and subscriptions are independent; fetch them concurrently.
	var (
		creditsBody, subsBody []byte
		creditsErr, subsErr   error
		wg                    sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		creditsBody, creditsErr = a.usageGet(ctx, client, base, cfg, in.Credential.Secret, "/alpha/billing/credits", query)
	}()
	go func() {
		defer wg.Done()
		subsBody, subsErr = a.usageGet(ctx, client, base, cfg, in.Credential.Secret, "/alpha/billing/subscriptions", query)
	}()
	wg.Wait()

	data := &UsageData{Whoami: &identity, FetchedAt: a.clock().UnixMilli()}
	if creditsErr == nil {
		var credits Credits
		if err := json.Unmarshal(creditsBody, &credits); err == nil {
			data.Credits = &credits
		}
	}
	if subsErr == nil {
		data.Plan = parsePlan(subsBody)
	}
	if summary, err := a.usageGet(ctx, client, base, cfg, in.Credential.Secret, "/alpha/usage/summary", query); err == nil {
		var period Period
		if err := json.Unmarshal(summary, &period); err == nil {
			data.Period = &period
		}
	}
	if creditsErr != nil && subsErr != nil {
		data.Error = "credits and subscriptions are unavailable"
	}

	a.cacheUsage(in.Credential.ID, data, "")
	return data, nil
}

// parsePlan reads the first subscription out of the subscriptions payload.
func parsePlan(body []byte) *Plan {
	var wrapper struct {
		Subscriptions []Plan `json:"subscriptions"`
		Data          []Plan `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil
	}
	subs := wrapper.Subscriptions
	if len(subs) == 0 {
		subs = wrapper.Data
	}
	if len(subs) == 0 {
		return nil
	}
	plan := subs[0]
	if plan.ID != nil {
		if total, ok := planTotalCredits[*plan.ID]; ok {
			plan.MonthlyTotal = &total
		}
		if plan.Name == nil {
			if display, ok := planDisplay[*plan.ID]; ok {
				plan.Name = &display
			}
		}
	}
	if plan.DaysRemaining == nil {
		if days := daysUntil(plan.CurrentPeriodEnd); days != nil {
			plan.DaysRemaining = days
		}
	}
	return &plan
}

func daysUntil(iso *string) *int {
	if iso == nil || *iso == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *iso)
	if err != nil {
		return nil
	}
	days := int(time.Until(t).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return &days
}

// usageGet performs one authenticated read.
func (a *Adapter) usageGet(ctx context.Context, client *http.Client, base string, cfg Config,
	secret, path string, query map[string]string) ([]byte, error) {
	u := strings.TrimRight(base, "/") + path
	if len(query) > 0 {
		values := url.Values{}
		for k, v := range query {
			if v != "" {
				values.Set(k, v)
			}
		}
		if encoded := values.Encode(); encoded != "" {
			u += "?" + encoded
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, values := range usageHeaders(secret, cfg.ProjectDir, cfg.ProtocolVersion, nil) {
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
		return nil, fmt.Errorf("command_code: %s returned HTTP %d", path, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// cacheUsage stores a snapshot (or the error that prevented one).
func (a *Adapter) cacheUsage(credID int64, data *UsageData, errMsg string) {
	a.usage.mu.Lock()
	defer a.usage.mu.Unlock()
	if a.usage.byID == nil {
		a.usage.byID = map[int64]usageEntry{}
	}
	a.usage.byID[credID] = usageEntry{data: data, fetchedAt: a.clock(), err: errMsg}
}
