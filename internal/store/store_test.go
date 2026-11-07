package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/learningdog1/ada_prune/internal/secretbox"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	box, err := secretbox.FromKey(make([]byte, secretbox.KeySize))
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	s := OpenMemory(box)
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestProviderCRUD(t *testing.T) {
	s := newTestStore(t)

	id, err := s.CreateProvider(Provider{
		Name:          "Command Code 主号",
		Kind:          "command_code",
		BaseURL:       "https://api.example.com",
		Enabled:       true,
		Weight:        1,
		ExtraHeaders:  map[string]string{"x-custom": "1"},
		AdapterConfig: map[string]any{"cliMode": "agent"},
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	got, err := s.GetProvider(id)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.Name != "Command Code 主号" || got.Kind != "command_code" {
		t.Fatalf("provider = %+v", got)
	}
	if got.ExtraHeaders["x-custom"] != "1" {
		t.Fatalf("extra headers = %v", got.ExtraHeaders)
	}
	if got.AdapterConfig["cliMode"] != "agent" {
		t.Fatalf("adapter config = %v", got.AdapterConfig)
	}

	got.Weight = 5
	got.AdapterConfig = map[string]any{"cliMode": "chat"}
	if err := s.UpdateProvider(*got); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	again, err := s.GetProvider(id)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if again.Weight != 5 || again.AdapterConfig["cliMode"] != "chat" {
		t.Fatalf("update did not persist: %+v", again)
	}

	if err := s.DeleteProvider(id); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if _, err := s.GetProvider(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProvider after delete = %v, want ErrNotFound", err)
	}
}

func TestProviderNameIsUnique(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateProvider(Provider{Name: "dup", Kind: "openai_compatible", Enabled: true, Weight: 1}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.CreateProvider(Provider{Name: "dup", Kind: "anthropic", Enabled: true, Weight: 1})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second create = %v, want ErrDuplicate", err)
	}
}

func TestDeleteProviderWithEnabledModelsIsBlocked(t *testing.T) {
	s := newTestStore(t)
	providerID := mustProvider(t, s, "p1")
	mustModel(t, s, providerID, "m1")

	err := s.DeleteProvider(providerID)
	var inUse *ErrInUse
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteProvider = %v, want *ErrInUse", err)
	}
	if inUse.Count != 1 {
		t.Fatalf("in-use count = %d, want 1", inUse.Count)
	}

	// Disabling the model unblocks the delete.
	if err := s.SetModelEnabled(mustModelID(t, s, "m1"), false); err != nil {
		t.Fatalf("SetModelEnabled: %v", err)
	}
	if err := s.DeleteProvider(providerID); err != nil {
		t.Fatalf("DeleteProvider after disabling models: %v", err)
	}
}

func TestCredentialSecretIsEncrypted(t *testing.T) {
	s := newTestStore(t)
	providerID := mustProvider(t, s, "p1")

	const secret = "user_secret_value_0123456789"
	id, err := s.CreateCredential(Credential{
		ProviderID: providerID,
		Name:       "主号",
		Secret:     secret,
		Enabled:    true,
		Status:     CredentialActive,
	})
	if err != nil {
		t.Fatalf("CreateCredential: %v", err)
	}

	// The list view never decrypts.
	rows, err := s.ListCredentials(providerID)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Secret != "" {
		t.Fatal("list view decrypted the secret")
	}
	if rows[0].SecretMask != secretbox.Mask(secret) {
		t.Fatalf("mask = %q, want %q", rows[0].SecretMask, secretbox.Mask(secret))
	}

	// The storage column holds ciphertext, not the secret.
	var stored string
	if err := s.DB().QueryRow(`SELECT secret_cipher FROM credentials WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatalf("read cipher: %v", err)
	}
	if strings.Contains(stored, secret) {
		t.Fatal("stored ciphertext contains the plaintext secret")
	}

	// The routing view does decrypt.
	routable, err := s.ListRoutableCredentials(providerID)
	if err != nil {
		t.Fatalf("ListRoutableCredentials: %v", err)
	}
	if len(routable) != 1 || routable[0].Secret != secret {
		t.Fatalf("routable secret mismatch: %+v", routable)
	}
}

func TestCredentialUpdateWithoutSecretKeepsIt(t *testing.T) {
	s := newTestStore(t)
	providerID := mustProvider(t, s, "p1")
	const secret = "user_keep_me_0123456789"
	id, err := s.CreateCredential(Credential{ProviderID: providerID, Name: "c", Secret: secret, Enabled: true})
	if err != nil {
		t.Fatalf("CreateCredential: %v", err)
	}

	got, err := s.GetCredential(id, false)
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	got.Name = "renamed"
	if err := s.UpdateCredential(*got, ""); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}

	withSecret, err := s.GetCredential(id, true)
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	if withSecret.Secret != secret {
		t.Fatalf("secret changed on an update that omitted it: %q", withSecret.Secret)
	}
	if withSecret.Name != "renamed" {
		t.Fatalf("name was not updated: %q", withSecret.Name)
	}
}

func TestCredentialStatusAndPin(t *testing.T) {
	s := newTestStore(t)
	providerID := mustProvider(t, s, "p1")
	id, err := s.CreateCredential(Credential{ProviderID: providerID, Name: "c", Secret: "user_x0123456789abcdef", Enabled: true})
	if err != nil {
		t.Fatalf("CreateCredential: %v", err)
	}

	if err := s.SetCredentialStatus(id, CredentialExhausted); err != nil {
		t.Fatalf("SetCredentialStatus: %v", err)
	}
	if err := s.SetCredentialStatus(id, "bogus"); err == nil {
		t.Fatal("accepted an invalid status")
	}
	// An exhausted credential is not routable.
	if rows, _ := s.ListRoutableCredentials(providerID); len(rows) != 0 {
		t.Fatalf("routable = %d, want 0 after exhaustion", len(rows))
	}

	if err := s.PinCredential(id); err != nil {
		t.Fatalf("PinCredential: %v", err)
	}
	pinned, err := s.PinnedCredentialID()
	if err != nil {
		t.Fatalf("PinnedCredentialID: %v", err)
	}
	if pinned != id {
		t.Fatalf("pinned = %d, want %d", pinned, id)
	}

	// Deleting the pinned credential clears the pin.
	if err := s.DeleteCredential(id); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if pinned, _ := s.PinnedCredentialID(); pinned != 0 {
		t.Fatalf("pin survived the delete: %d", pinned)
	}
}

func TestModelUniquenessAndRouteReferences(t *testing.T) {
	s := newTestStore(t)
	providerID := mustProvider(t, s, "p1")
	// mustModel seeds public-a with upstream name public-a.
	modelID := mustModel(t, s, providerID, "public-a")

	// A second model with the same upstream name under the same provider is a
	// conflict even though its public name differs.
	if _, err := s.CreateModel(Model{
		ProviderID: providerID, DisplayName: "Other", PublicName: "public-b",
		UpstreamName: "public-a", UpstreamProtocol: "vendor_native",
	}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate (provider, upstream) = %v, want ErrDuplicate", err)
	}

	// A public name is globally unique, so reusing it with a new upstream name
	// is also a conflict.
	if _, err := s.CreateModel(Model{
		ProviderID: providerID, DisplayName: "Other", PublicName: "public-a",
		UpstreamName: "upstream-c", UpstreamProtocol: "vendor_native",
	}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate public name = %v, want ErrDuplicate", err)
	}

	if _, err := s.CreateRouteModel(RouteModel{
		Name: "fast", Strategy: StrategyPriority, Enabled: true,
		Members: []RouteModelMember{{ModelID: modelID, Priority: 1, Weight: 1, Enabled: true}},
	}); err != nil {
		t.Fatalf("CreateRouteModel: %v", err)
	}

	err := s.DeleteModel(modelID)
	var inUse *ErrInUse
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteModel = %v, want *ErrInUse", err)
	}
}

func TestClientKeyLifecycle(t *testing.T) {
	s := newTestStore(t)

	created, err := s.CreateClientKey(ClientKey{Name: "codex-local", Enabled: true})
	if err != nil {
		t.Fatalf("CreateClientKey: %v", err)
	}
	if !strings.HasPrefix(created.Token, ClientKeyPrefix) {
		t.Fatalf("token = %q, want the %s prefix", created.Token, ClientKeyPrefix)
	}

	key, err := s.VerifyClientKey(created.Token)
	if err != nil {
		t.Fatalf("VerifyClientKey: %v", err)
	}
	if key.Name != "codex-local" {
		t.Fatalf("verified key = %+v", key)
	}

	// The plaintext is not stored anywhere.
	var stored string
	if err := s.DB().QueryRow(`SELECT key_hash FROM client_keys WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if strings.Contains(stored, created.Token) {
		t.Fatal("key_hash contains the plaintext token")
	}

	// Disabling makes verification fail.
	if err := s.SetClientKeyEnabled(created.ID, false); err != nil {
		t.Fatalf("SetClientKeyEnabled: %v", err)
	}
	if _, err := s.VerifyClientKey(created.Token); !errors.Is(err, ErrClientKeyInvalid) {
		t.Fatalf("verify of a disabled key = %v, want ErrClientKeyInvalid", err)
	}

	// Rotation invalidates the old token.
	if err := s.SetClientKeyEnabled(created.ID, true); err != nil {
		t.Fatalf("SetClientKeyEnabled: %v", err)
	}
	token, err := s.RotateClientKey(created.ID)
	if err != nil {
		t.Fatalf("RotateClientKey: %v", err)
	}
	if token == created.Token {
		t.Fatal("rotation returned the same token")
	}
	if _, err := s.VerifyClientKey(created.Token); !errors.Is(err, ErrClientKeyInvalid) {
		t.Fatalf("old token still works: %v", err)
	}
	if _, err := s.VerifyClientKey(token); err != nil {
		t.Fatalf("new token rejected: %v", err)
	}

	if _, err := s.VerifyClientKey("sk-prune-not-a-real-key"); !errors.Is(err, ErrClientKeyInvalid) {
		t.Fatalf("unknown token = %v, want ErrClientKeyInvalid", err)
	}
	if _, err := s.VerifyClientKey("sk-ccp-legacy"); !errors.Is(err, ErrClientKeyInvalid) {
		t.Fatalf("foreign prefix = %v, want ErrClientKeyInvalid", err)
	}
}

func TestRequestLogsAndUsage(t *testing.T) {
	s := newTestStore(t)
	providerID := mustProvider(t, s, "p1")

	const now = int64(1_793_500_000_000)
	for i := 0; i < 3; i++ {
		status := int64(200)
		errType := ""
		if i == 2 {
			status, errType = 502, "upstream_error"
		}
		if err := s.InsertRequest(RequestRecord{
			TS: now + int64(i), Endpoint: "/v1/chat/completions", InboundProtocol: "openai_chat",
			ProviderID: &providerID, ProviderKind: "command_code", Model: "m1",
			StatusCode: status, Stream: true, DurationMs: 100, TTFTMs: 40,
			InputTokens: 10, OutputTokens: 5, CachedTokens: 2, Cost: 0.01,
			FinishReason: "stop", ErrorType: errType,
		}); err != nil {
			t.Fatalf("InsertRequest: %v", err)
		}
		if err := s.RecordUsage(now, providerID, "m1", 0, 10, 5, 2, 0.01); err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}
	}

	page, err := s.ListRequests(LogsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if page.Total != 3 || len(page.Rows) != 3 {
		t.Fatalf("page = %+v", page)
	}
	if page.Rows[0].TS < page.Rows[1].TS {
		t.Fatal("rows are not newest first")
	}

	summary, err := s.UsageSummary("", "")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.Requests != 3 || summary.Errors != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.InputTokens != 30 || summary.OutputTokens != 15 || summary.CachedTokens != 6 {
		t.Fatalf("token totals = %+v", summary)
	}

	usage, err := s.UsageAggregate(UsageQuery{GroupBy: GroupByProvider})
	if err != nil {
		t.Fatalf("UsageAggregate: %v", err)
	}
	if len(usage.Rows) != 1 || usage.Rows[0].Requests != 3 {
		t.Fatalf("usage rows = %+v", usage.Rows)
	}

	chart, err := s.Last24Hours(now+1000, 24)
	if err != nil {
		t.Fatalf("Last24Hours: %v", err)
	}
	if len(chart.Points) != 24 {
		t.Fatalf("points = %d, want 24", len(chart.Points))
	}
}

func TestSettingsAndProviderState(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetSetting("k", "v"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if got, _ := s.GetSetting("k"); got != "v" {
		t.Fatalf("GetSetting = %q", got)
	}
	if err := s.DeleteSetting("k"); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	if got, _ := s.GetSetting("k"); got != "" {
		t.Fatalf("GetSetting after delete = %q", got)
	}

	providerID := mustProvider(t, s, "p1")
	type state struct {
		Reported bool `json:"reported"`
	}
	if err := s.SetProviderState(providerID, "fingerprint", state{Reported: true}); err != nil {
		t.Fatalf("SetProviderState: %v", err)
	}
	var got state
	found, err := s.GetProviderState(providerID, "fingerprint", &got)
	if err != nil || !found {
		t.Fatalf("GetProviderState: found=%v err=%v", found, err)
	}
	if !got.Reported {
		t.Fatal("state did not round trip")
	}
	if err := s.DeleteProviderState(providerID, "fingerprint"); err != nil {
		t.Fatalf("DeleteProviderState: %v", err)
	}
	if found, _ := s.GetProviderState(providerID, "fingerprint", &got); found {
		t.Fatal("state survived the delete")
	}
}

func TestEndpointCRUD(t *testing.T) {
	s := newTestStore(t)
	providerID := mustProvider(t, s, "p1")
	modelID := mustModel(t, s, providerID, "m1")

	id, err := s.CreateEndpoint(Endpoint{
		Name: "Claude 入口", Path: "/cc/v1/messages", InboundProtocol: "anthropic",
		TargetType: TargetModel, TargetID: modelID, AuthRequired: true, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	byPath, err := s.GetEndpointByPath("/cc/v1/messages")
	if err != nil {
		t.Fatalf("GetEndpointByPath: %v", err)
	}
	if byPath.ID != id {
		t.Fatalf("lookup by path returned id %d, want %d", byPath.ID, id)
	}
	if err := s.DeleteEndpoint(id); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	if _, err := s.GetEndpoint(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetEndpoint after delete = %v", err)
	}
}

func TestInMemoryStoreWorks(t *testing.T) {
	box, _ := secretbox.FromKey(make([]byte, secretbox.KeySize))
	s := OpenMemory(box)
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer s.Close()
	if s.Degraded() {
		t.Fatal("an in-memory store opened on purpose is not degraded")
	}
	if _, err := s.CreateProvider(Provider{Name: "p", Kind: "custom", Enabled: true, Weight: 1}); err != nil {
		t.Fatalf("create in memory: %v", err)
	}
}

// ---- helpers ----

func mustProvider(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	id, err := s.CreateProvider(Provider{Name: name, Kind: "command_code", Enabled: true, Weight: 1})
	if err != nil {
		t.Fatalf("CreateProvider(%s): %v", name, err)
	}
	return id
}

func mustModel(t *testing.T, s *Store, providerID int64, publicName string) int64 {
	t.Helper()
	id, err := s.CreateModel(Model{
		ProviderID:       providerID,
		DisplayName:      publicName,
		PublicName:       publicName,
		UpstreamName:     publicName,
		UpstreamProtocol: "vendor_native",
		SupportsStream:   true,
		SupportsTools:    true,
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("CreateModel(%s): %v", publicName, err)
	}
	return id
}

func mustModelID(t *testing.T, s *Store, publicName string) int64 {
	t.Helper()
	m, err := s.GetModelByPublicName(publicName)
	if err != nil {
		t.Fatalf("GetModelByPublicName(%s): %v", publicName, err)
	}
	return m.ID
}
