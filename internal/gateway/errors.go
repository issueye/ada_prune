package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

// ClientError is a failure rendered for the calling protocol.
type ClientError struct {
	// Status is the HTTP status to send.
	Status int
	// Type is the protocol's error type string.
	Type string
	// Message is the human-readable text. When the upstream supplied one, it is
	// preserved: hiding it makes debugging impossible.
	Message string
	// Code is an optional machine-readable code.
	Code string
	// Param names the offending request field, when known.
	Param string
	// RetryAfter is a hint in seconds for 429/503 responses.
	RetryAfter int
}

func (e *ClientError) Error() string {
	if e.Code != "" {
		return "gateway: " + e.Type + " (" + e.Code + "): " + e.Message
	}
	return "gateway: " + e.Type + ": " + e.Message
}

// NewClientError builds an error with a protocol-appropriate type.
func NewClientError(status int, message string) *ClientError {
	return &ClientError{Status: status, Type: typeForStatus(status), Message: message}
}

// InvalidRequest reports a malformed or unsupported request body.
func InvalidRequest(param, message string) *ClientError {
	return &ClientError{
		Status:  http.StatusBadRequest,
		Type:    "invalid_request_error",
		Message: message,
		Param:   param,
	}
}

// typeForStatus picks the OpenAI-family error type for a status.
func typeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable:
		return "overloaded_error"
	default:
		return "upstream_error"
	}
}

// anthropicType maps an OpenAI-family type onto Anthropic's vocabulary.
func anthropicType(t string) string {
	switch t {
	case "authentication_error", "permission_error", "invalid_request_error",
		"not_found_error", "rate_limit_error", "overloaded_error":
		return t
	case "server_busy", "temporarily_unavailable":
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// EncodeClientError renders an error in the calling protocol's shape.
func EncodeClientError(in ir.Protocol, err *ClientError) []byte {
	if err == nil {
		return nil
	}
	var body any
	if in == ir.ProtoAnthropic {
		body = map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    anthropicType(err.Type),
				"message": err.Message,
			},
		}
	} else {
		inner := map[string]any{
			"message": err.Message,
			"type":    err.Type,
		}
		if err.Code != "" {
			inner["code"] = err.Code
		}
		if err.Param != "" {
			inner["param"] = err.Param
		}
		body = map[string]any{"error": inner}
	}
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return []byte(`{"error":{"message":"internal error","type":"upstream_error"}}`)
	}
	return raw
}

// MapUpstreamStatus converts a non-2xx upstream answer into a client error.
//
// The status is deliberately collapsed: a downstream client cannot act
// differently on 401 versus 403 from the gateway's upstream, and echoing the
// vendor's exact status leaks which vendor is behind the model.
func MapUpstreamStatus(status int, body []byte) *ClientError {
	message := upstreamMessage(body)
	switch status {
	case http.StatusPaymentRequired:
		// Credit exhaustion is not a transient limit; say so, or clients back off
		// and retry against a key that cannot work until it is reloaded.
		if message == "" {
			message = "upstream account has no credit"
		}
		return &ClientError{
			Status:     http.StatusTooManyRequests,
			Type:       "rate_limit_error",
			Message:    message + " (upstream quota exhausted; not a transient rate limit)",
			RetryAfter: 30,
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		if message == "" {
			message = "upstream rejected the credential"
		}
		return &ClientError{Status: http.StatusUnauthorized, Type: "authentication_error", Message: message}
	case http.StatusNotFound:
		if message == "" {
			message = "upstream model not found"
		}
		return &ClientError{Status: http.StatusNotFound, Type: "not_found_error", Message: message}
	case http.StatusTooManyRequests:
		if message == "" {
			message = "upstream rate limited the request"
		}
		return &ClientError{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Message: message, RetryAfter: 30}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		if message == "" {
			message = "upstream rejected the request"
		}
		return &ClientError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: message}
	case http.StatusServiceUnavailable:
		if message == "" {
			message = "upstream is temporarily unavailable"
		}
		return &ClientError{Status: http.StatusServiceUnavailable, Type: "overloaded_error", Message: message, RetryAfter: 10}
	default:
		if message == "" {
			message = "upstream failed"
		}
		return &ClientError{Status: http.StatusBadGateway, Type: "upstream_error", Message: message, RetryAfter: 10}
	}
}

// upstreamMessage extracts a readable message from a vendor error body.
func upstreamMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		switch {
		case parsed.Error.Message != "":
			return truncate(parsed.Error.Message, 300)
		case parsed.Message != "":
			return truncate(parsed.Message, 300)
		case parsed.Detail != "":
			return truncate(parsed.Detail, 300)
		}
	}
	return truncate(trimmed, 200)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// TruncatedStreamError is the retryable failure for a stream that ended without a
// completion signal. Reporting "stop" instead would silently corrupt the answer.
func TruncatedStreamError(detail string) *ClientError {
	return &ClientError{
		Status:     http.StatusBadGateway,
		Type:       "upstream_error",
		Message:    "upstream stream ended without a completion signal (" + detail + ")",
		RetryAfter: 10,
	}
}
