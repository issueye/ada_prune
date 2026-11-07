package gateway

import (
	"errors"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

// ErrModelNotFound means the requested model name is unknown or disabled.
var ErrModelNotFound = errors.New("gateway: model not found")

// ErrRouteModelNotFound means a route model name is unknown or disabled.
var ErrRouteModelNotFound = errors.New("gateway: route model not found")

// Candidate is one routable model together with its provider.
type Candidate struct {
	Model    ModelRef
	Provider ProviderConfig
}

// Catalog is the narrow view of stored configuration the data plane needs.
//
// It exists so internal/gateway never imports the persistence layer: the
// application provides a store-backed implementation, tests provide a map.
type Catalog interface {
	// Resolve returns the candidate chain for a public model name. A plain model
	// yields one candidate; a route model yields its members in priority order.
	Resolve(name string) ([]Candidate, error)

	// Credentials returns the usable credentials of a provider, best first.
	Credentials(providerID int64) ([]Credential, error)

	// MarkExhausted records a durable credit exhaustion.
	MarkExhausted(credentialID int64) error

	// Touch records that a credential served a request.
	Touch(credentialID int64)

	// PinnedCredential returns the operator's routing pin, or 0.
	PinnedCredential() int64
}

// AdapterResolver looks an adapter up by kind.
type AdapterResolver interface {
	Get(kind Kind) (Adapter, bool)
}

// Record is one finished data-plane call.
type Record struct {
	TS                 int64
	Endpoint           string
	InProtocol         ir.Protocol
	ProviderID         int64
	ProviderKind       string
	Model              string
	ClientKeyID        int64
	CredentialID       int64
	StatusCode         int
	Stream             bool
	DurationMs         int64
	TTFTMs             int64
	Usage              ir.Usage
	Cost               float64
	FinishReason       string
	ErrorType          string
	ClientDisconnected bool
}

// Recorder receives finished calls. Implementations must not block the request
// path: persistence happens off the hot path.
type Recorder interface {
	Record(rec Record)
}
