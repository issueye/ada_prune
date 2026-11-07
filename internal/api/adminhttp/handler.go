package adminhttp

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/httpkit"
	"github.com/learningdog1/ada_prune/internal/store"
)

// maxAdminBody bounds management request bodies. They carry key lists and
// settings, never model payloads.
const maxAdminBody = 2 << 20

// HTTPHandler serves /admin/api/*. It only parses, authorises and serialises;
// every decision lives in Service so the desktop UI behaves identically.
type HTTPHandler struct {
	svc   *admin.Service
	token string
}

// NewHTTPHandler returns the admin HTTP adapter. An empty token disables
// authentication, which is the loopback default.
func NewHTTPHandler(svc *admin.Service, token string) *HTTPHandler {
	return &HTTPHandler{svc: svc, token: token}
}

// ServeHTTP dispatches one management call.
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Token authentication comes first: it is CSRF-immune on its own, so a
	// request without a valid token never reaches the origin check.
	if !h.tokenOK(r) {
		writeError(w, http.StatusUnauthorized, "admin token required", "")
		return
	}
	// Cross-site writes are rejected by comparing Origin with this server's own
	// Host. curl, the desktop page and same-origin LAN pages send none or a
	// matching one.
	if origin := r.Header.Get("Origin"); origin != "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
		if !sameOrigin(origin, r.Host) {
			writeError(w, http.StatusForbidden, "cross-origin write rejected", "")
			return
		}
	}

	path := strings.TrimPrefix(r.URL.Path, "/admin/api")
	segments := splitPath(path)
	if len(segments) == 0 {
		writeError(w, http.StatusNotFound, "not found", "")
		return
	}

	// Singletons first: they have no id segment.
	switch segments[0] {
	case "overview":
		h.do(w, func() (any, error) { return h.svc.Overview(r.Context()) })
		return
	case "status":
		h.do(w, func() (any, error) { return h.svc.Status(r.Context()) })
		return
	case "adapters":
		h.do(w, func() (any, error) {
			rows, err := h.svc.Adapters(r.Context())
			if err != nil {
				return nil, err
			}
			return map[string]any{"rows": rows}, nil
		})
		return
	case "settings":
		h.settings(w, r)
		return
	case "logs":
		h.logs(w, r)
		return
	case "usage":
		h.usage(w, r)
		return
	case "export":
		h.do(w, func() (any, error) { return h.svc.Export(r.Context()) })
		return
	}

	// Collections: /<collection> and /<collection>/<id>[/<action>].
	if err := h.collection(w, r, segments); err != nil {
		writeServiceError(w, err)
	}
}

// logs serves GET /admin/api/logs.
func (h *HTTPHandler) logs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	q := admin.LogsQuery{
		Limit:       int(queryInt(r, "limit")),
		Offset:      int(queryInt(r, "offset")),
		Endpoint:    r.URL.Query().Get("endpoint"),
		Status:      r.URL.Query().Get("status"),
		ProviderID:  queryInt(r, "providerId"),
		Model:       r.URL.Query().Get("model"),
		ClientKeyID: queryInt(r, "clientKeyId"),
		From:        queryInt(r, "from"),
		To:          queryInt(r, "to"),
	}
	h.do(w, func() (any, error) { return h.svc.Logs(r.Context(), q) })
}

// usage serves GET /admin/api/usage.
func (h *HTTPHandler) usage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	q := admin.UsageQuery{
		From:    r.URL.Query().Get("from"),
		To:      r.URL.Query().Get("to"),
		GroupBy: store.UsageGroup(r.URL.Query().Get("groupBy")),
	}
	h.do(w, func() (any, error) { return h.svc.Usage(r.Context(), q) })
}

func (h *HTTPHandler) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.do(w, func() (any, error) { return h.svc.Settings(r.Context()) })
	case http.MethodPut:
		var patch map[string]any
		if err := httpkit.DecodeJSON(r, maxAdminBody, &patch); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body", "")
			return
		}
		h.do(w, func() (any, error) { return h.svc.SaveSettings(r.Context(), patch) })
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

// collection routes the per-resource endpoints. It returns an error only when a
// handler could not write its own response.
func (h *HTTPHandler) collection(w http.ResponseWriter, r *http.Request, segments []string) error {
	name := segments[0]
	var id int64
	var action string

	if len(segments) >= 2 {
		// /credentials/usage/refresh and /credentials/import-cli have a literal
		// second segment, so only parse an id when the segment is numeric.
		if parsed, err := strconv.ParseInt(segments[1], 10, 64); err == nil {
			id = parsed
			if len(segments) >= 3 {
				action = segments[2]
			}
		} else {
			action = segments[1]
			if len(segments) >= 3 {
				action = segments[1] + "/" + segments[2]
			}
		}
	}

	switch name {
	case "providers":
		return h.providers(w, r, id, action)
	case "credentials":
		return h.credentials(w, r, id, action)
	case "models":
		return h.models(w, r, id, action)
	case "route-models":
		return h.routeModels(w, r, id, action)
	case "client-keys":
		return h.clientKeys(w, r, id, action)
	case "endpoints":
		return h.endpoints(w, r, id, action)
	default:
		writeError(w, http.StatusNotFound, "not found", "")
		return nil
	}
}

func (h *HTTPHandler) providers(w http.ResponseWriter, r *http.Request, id int64, action string) error {
	ctx := r.Context()

	if id == 0 {
		switch r.Method {
		case http.MethodGet:
			rows, err := h.svc.ListProviders(ctx)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
			return nil
		case http.MethodPost:
			var in admin.ProviderInput
			if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
				return badBody(err)
			}
			row, err := h.svc.CreateProvider(ctx, in)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusCreated, row)
			return nil
		}
		return methodNotAllowed()
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		row, err := h.svc.GetProvider(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodPut:
		var in admin.ProviderInput
		if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
			return badBody(err)
		}
		row, err := h.svc.UpdateProvider(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodDelete:
		if err := h.svc.DeleteProvider(ctx, id); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	case action == "probe" && r.Method == http.MethodPost:
		result, err := h.svc.ProbeProvider(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, result)
		return nil
	case action == "status" && r.Method == http.MethodGet:
		status, err := h.svc.ProviderStatus(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, status)
		return nil
	case action == "discover-models" && r.Method == http.MethodPost:
		result, err := h.svc.DiscoverModels(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, result)
		return nil
	case action == "enabled" && r.Method == http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil || body.Enabled == nil {
			return badBody(err)
		}
		if err := h.svc.SetProviderEnabled(ctx, id, *body.Enabled); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	return methodNotAllowed()
}

func (h *HTTPHandler) credentials(w http.ResponseWriter, r *http.Request, id int64, action string) error {
	ctx := r.Context()

	if id == 0 {
		switch {
		case action == "" && r.Method == http.MethodGet:
			providerID := queryInt(r, "providerId")
			rows, err := h.svc.ListCredentials(ctx, providerID)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
			return nil
		case action == "" && r.Method == http.MethodPost:
			var in admin.CredentialInput
			if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
				return badBody(err)
			}
			row, err := h.svc.CreateCredential(ctx, in)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusCreated, row)
			return nil
		case action == "batch" && r.Method == http.MethodPost:
			var body struct {
				ProviderID int64  `json:"providerId"`
				Text       string `json:"text"`
			}
			if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil {
				return badBody(err)
			}
			result, err := h.svc.BatchImport(ctx, body.ProviderID, body.Text)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, result)
			return nil
		case action == "import-cli" && r.Method == http.MethodPost:
			var body struct {
				ProviderID int64 `json:"providerId"`
			}
			if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil {
				return badBody(err)
			}
			result, err := h.svc.ImportCLI(ctx, body.ProviderID)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, result)
			return nil
		case action == "pin" && r.Method == http.MethodPost:
			var body struct {
				ID *int64 `json:"id"`
			}
			if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil {
				return badBody(err)
			}
			var pin int64
			if body.ID != nil {
				pin = *body.ID
			}
			if err := h.svc.PinCredential(ctx, pin); err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pinned": pin})
			return nil
		case action == "usage/refresh" && r.Method == http.MethodPost:
			result, err := h.svc.RefreshAllUsage(ctx)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, result)
			return nil
		}
		return methodNotAllowed()
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		row, err := h.svc.GetCredential(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodPut:
		var in admin.CredentialInput
		if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
			return badBody(err)
		}
		row, err := h.svc.UpdateCredential(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodDelete:
		if err := h.svc.DeleteCredential(ctx, id); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	case action == "status" && r.Method == http.MethodPost:
		var body struct {
			Status string `json:"status"`
		}
		if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil {
			return badBody(err)
		}
		if err := h.svc.SetCredentialStatus(ctx, id, body.Status); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	case action == "test" && r.Method == http.MethodPost:
		result, err := h.svc.TestCredential(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, result)
		return nil
	case action == "usage" && r.Method == http.MethodGet:
		data, err := h.svc.CredentialUsage(ctx, id, queryBool(r, "refresh"))
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, data)
		return nil
	case action == "usage/refresh" && r.Method == http.MethodPost:
		data, err := h.svc.CredentialUsage(ctx, id, true)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, data)
		return nil
	}
	return methodNotAllowed()
}

func (h *HTTPHandler) models(w http.ResponseWriter, r *http.Request, id int64, action string) error {
	ctx := r.Context()

	if id == 0 {
		switch {
		case action == "" && r.Method == http.MethodGet:
			rows, err := h.svc.ListModels(ctx, admin.ModelFilterInput{
				ProviderID: queryInt(r, "providerId"),
				Protocol:   r.URL.Query().Get("protocol"),
				Query:      r.URL.Query().Get("q"),
				Enabled:    queryBoolPtr(r, "enabled"),
			})
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
			return nil
		case action == "" && r.Method == http.MethodPost:
			var in admin.ModelInput
			if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
				return badBody(err)
			}
			row, err := h.svc.CreateModel(ctx, in)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusCreated, row)
			return nil
		case action == "import" && r.Method == http.MethodPost:
			var body struct {
				ProviderID int64                  `json:"providerId"`
				Rows       []admin.ModelImportRow `json:"rows"`
			}
			if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil {
				return badBody(err)
			}
			result, err := h.svc.ImportModels(ctx, body.ProviderID, body.Rows)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, result)
			return nil
		}
		return methodNotAllowed()
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		row, err := h.svc.GetModel(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodPut:
		var in admin.ModelInput
		if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
			return badBody(err)
		}
		row, err := h.svc.UpdateModel(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodDelete:
		if err := h.svc.DeleteModel(ctx, id); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	case action == "enabled" && r.Method == http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil || body.Enabled == nil {
			return badBody(err)
		}
		if err := h.svc.SetModelEnabled(ctx, id, *body.Enabled); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	return methodNotAllowed()
}

func (h *HTTPHandler) routeModels(w http.ResponseWriter, r *http.Request, id int64, action string) error {
	ctx := r.Context()

	if id == 0 {
		switch r.Method {
		case http.MethodGet:
			rows, err := h.svc.ListRouteModels(ctx)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
			return nil
		case http.MethodPost:
			var in admin.RouteModelInput
			if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
				return badBody(err)
			}
			row, err := h.svc.CreateRouteModel(ctx, in)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusCreated, row)
			return nil
		}
		return methodNotAllowed()
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		row, err := h.svc.GetRouteModel(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodPut:
		var in admin.RouteModelInput
		if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
			return badBody(err)
		}
		row, err := h.svc.UpdateRouteModel(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodDelete:
		if err := h.svc.DeleteRouteModel(ctx, id); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	return methodNotAllowed()
}

func (h *HTTPHandler) clientKeys(w http.ResponseWriter, r *http.Request, id int64, action string) error {
	ctx := r.Context()

	if id == 0 {
		switch r.Method {
		case http.MethodGet:
			rows, err := h.svc.ListClientKeys(ctx)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
			return nil
		case http.MethodPost:
			var in admin.ClientKeyInput
			if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
				return badBody(err)
			}
			created, err := h.svc.CreateClientKey(ctx, in)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusCreated, created)
			return nil
		}
		return methodNotAllowed()
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		row, err := h.svc.GetClientKey(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodPut:
		var in admin.ClientKeyInput
		if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
			return badBody(err)
		}
		row, err := h.svc.UpdateClientKey(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodDelete:
		if err := h.svc.DeleteClientKey(ctx, id); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	case action == "rotate" && r.Method == http.MethodPost:
		token, err := h.svc.RotateClientKey(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": token})
		return nil
	case action == "reset-usage" && r.Method == http.MethodPost:
		if err := h.svc.ResetClientKeyUsage(ctx, id); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	case action == "enabled" && r.Method == http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := httpkit.DecodeJSON(r, maxAdminBody, &body); err != nil || body.Enabled == nil {
			return badBody(err)
		}
		if err := h.svc.SetClientKeyEnabled(ctx, id, *body.Enabled); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	return methodNotAllowed()
}

func (h *HTTPHandler) endpoints(w http.ResponseWriter, r *http.Request, id int64, action string) error {
	ctx := r.Context()

	if id == 0 && action == "" {
		switch r.Method {
		case http.MethodGet:
			rows, err := h.svc.ListEndpoints(ctx)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
			return nil
		case http.MethodPost:
			var in admin.EndpointInput
			if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
				return badBody(err)
			}
			row, err := h.svc.CreateEndpoint(ctx, in)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusCreated, row)
			return nil
		}
		return methodNotAllowed()
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		row, err := h.svc.GetEndpoint(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodPut:
		var in admin.EndpointInput
		if err := httpkit.DecodeJSON(r, maxAdminBody, &in); err != nil {
			return badBody(err)
		}
		row, err := h.svc.UpdateEndpoint(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, row)
		return nil
	case action == "" && r.Method == http.MethodDelete:
		if err := h.svc.DeleteEndpoint(ctx, id); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	return methodNotAllowed()
}

// ---- helpers ----

func (h *HTTPHandler) do(w http.ResponseWriter, fn func() (any, error)) {
	v, err := fn()
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func queryInt(r *http.Request, name string) int64 {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// queryBoolPtr returns nil when the parameter is absent, so "no filter" stays
// distinguishable from "filter for false".
func queryBoolPtr(r *http.Request, name string) *bool {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	value := raw == "1" || strings.EqualFold(raw, "true")
	return &value
}

func queryBool(r *http.Request, name string) bool {
	raw := r.URL.Query().Get(name)
	return raw == "1" || strings.EqualFold(raw, "true")
}

func badBody(err error) error {
	if err == nil {
		return admin.FieldError(400, "", "invalid JSON body")
	}
	return admin.FieldError(400, "", "%s", err.Error())
}

func methodNotAllowed() error {
	return &admin.Error{Status: http.StatusMethodNotAllowed, Message: "method not allowed"}
}

func (h *HTTPHandler) tokenOK(r *http.Request) bool {
	if h.token == "" {
		return true
	}
	given := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		given = strings.TrimPrefix(auth, "Bearer ")
	}
	if given == "" {
		given = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(h.token)) == 1
}

func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// writeJSON writes the control-plane envelope.
func writeJSON(w http.ResponseWriter, status int, v any) {
	httpkit.SendJSON(w, status, v)
}

func writeError(w http.ResponseWriter, status int, msg, field string) {
	body := map[string]any{"error": msg}
	switch status {
	case http.StatusBadRequest:
		body["code"] = "bad_request"
	case http.StatusUnauthorized:
		body["code"] = "unauthorized"
	case http.StatusForbidden:
		body["code"] = "forbidden"
	case http.StatusNotFound:
		body["code"] = "not_found"
	case http.StatusConflict:
		body["code"] = "conflict"
	case http.StatusMethodNotAllowed:
		body["code"] = "method_not_allowed"
	default:
		body["code"] = "internal"
	}
	if field != "" {
		body["field"] = field
	}
	httpkit.SendJSON(w, status, body)
}

func writeServiceError(w http.ResponseWriter, err error) {
	var adminErr *admin.Error
	if errors.As(err, &adminErr) {
		field := adminErr.Field
		writeError(w, admin.StatusOf(err), adminErr.Message, field)
		return
	}
	var inUse *store.ErrInUse
	if errors.As(err, &inUse) {
		body := map[string]any{"error": inUse.Reason, "code": "conflict"}
		if inUse.Count > 0 {
			body["count"] = inUse.Count
		}
		httpkit.SendJSON(w, http.StatusConflict, body)
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found", "")
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicate value", "")
	default:
		writeError(w, http.StatusInternalServerError, err.Error(), "")
	}
}
