// Package provider holds the adapter registry and the concrete adapters.
//
// An adapter registers itself from init with Register; the application builds
// every registered adapter once and asks the registry when it needs one. Adding
// a vendor is therefore one new package plus one Register call: neither
// internal/gateway nor internal/api changes.
package provider

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/logging"
)

// Deps is everything an adapter may use. It deliberately holds no store: an
// adapter that needs persistence uses State, and everything else arrives in
// gateway.InvokeRequest.
type Deps struct {
	Log   *logging.Logger
	HTTP  *http.Client
	State gateway.StateStore
	Clock func() time.Time
	Rand  io.Reader
}

// Factory builds one adapter.
type Factory func(Deps) (gateway.Adapter, error)

type registration struct {
	label string
	make  Factory
}

var (
	registryMu sync.RWMutex
	registry   = map[gateway.Kind]registration{}
)

// Register makes an adapter available under kind. It is meant to be called from
// init and panics on a duplicate kind, which is a programming error: two packages
// claiming one kind cannot be resolved at runtime.
func Register(kind gateway.Kind, label string, f Factory) {
	if kind == "" {
		panic("provider: Register with an empty kind")
	}
	if f == nil {
		panic("provider: Register with a nil factory")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[kind]; exists {
		panic(fmt.Sprintf("provider: kind %q registered twice", kind))
	}
	registry[kind] = registration{label: label, make: f}
}

// Registry is the set of adapters built for this process.
type Registry struct {
	adapters map[gateway.Kind]gateway.Adapter
	labels   map[gateway.Kind]string
	order    []gateway.Kind
}

// Build instantiates every registered adapter. A failing adapter is reported and
// skipped rather than failing startup: one broken vendor must not stop the
// gateway, and the operator sees why in the log.
func Build(d Deps) *Registry {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	if d.Rand == nil {
		d.Rand = defaultRand{}
	}

	registryMu.RLock()
	kinds := make([]gateway.Kind, 0, len(registry))
	for k := range registry {
		kinds = append(kinds, k)
	}
	registryMu.RUnlock()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	out := &Registry{
		adapters: make(map[gateway.Kind]gateway.Adapter, len(kinds)),
		labels:   make(map[gateway.Kind]string, len(kinds)),
		order:    make([]gateway.Kind, 0, len(kinds)),
	}
	for _, kind := range kinds {
		registryMu.RLock()
		reg := registry[kind]
		registryMu.RUnlock()

		adapter, err := reg.make(d)
		if err != nil {
			d.Log.Error("Adapter failed to initialise", map[string]any{"kind": string(kind), "error": err.Error()})
			continue
		}
		out.adapters[kind] = adapter
		out.labels[kind] = reg.label
		out.order = append(out.order, kind)
	}
	return out
}

// Get returns the adapter for kind.
func (r *Registry) Get(kind gateway.Kind) (gateway.Adapter, bool) {
	if r == nil {
		return nil, false
	}
	a, ok := r.adapters[kind]
	return a, ok
}

// Kinds returns the built adapter kinds in registration order.
func (r *Registry) Kinds() []gateway.Kind {
	if r == nil {
		return nil
	}
	return append([]gateway.Kind(nil), r.order...)
}

// Label returns the display label for a kind.
func (r *Registry) Label(kind gateway.Kind) string {
	if r == nil {
		return ""
	}
	if label, ok := r.labels[kind]; ok {
		return label
	}
	return string(kind)
}

// AccessMode returns the access mode for a kind: the adapter's own declaration
// when it implements gateway.AccessModeProvider, AccessAPI otherwise.
func (r *Registry) AccessMode(kind gateway.Kind) gateway.AccessMode {
	if r == nil {
		return gateway.AccessAPI
	}
	if adapter, ok := r.adapters[kind]; ok {
		if declared, ok := adapter.(gateway.AccessModeProvider); ok {
			return declared.AccessMode()
		}
	}
	return gateway.AccessAPI
}

// Descriptor is one entry of the control plane's adapter catalogue.
type Descriptor struct {
	Kind         gateway.Kind          `json:"kind"`
	Label        string                `json:"label"`
	AccessMode   gateway.AccessMode    `json:"accessMode"`
	Capabilities gateway.Capabilities  `json:"capabilities"`
	ConfigSchema []gateway.ConfigField `json:"configSchema"`
}

// Descriptors describes every built adapter, in a stable order. The control
// plane renders the "new provider" wizard and the adapter form from this.
func (r *Registry) Descriptors() []Descriptor {
	if r == nil {
		return nil
	}
	out := make([]Descriptor, 0, len(r.order))
	for _, kind := range r.order {
		adapter := r.adapters[kind]
		d := Descriptor{
			Kind:         kind,
			Label:        r.labels[kind],
			AccessMode:   r.AccessMode(kind),
			Capabilities: adapter.Capabilities(),
		}
		if schema, ok := adapter.(gateway.ConfigSchemaProvider); ok {
			d.ConfigSchema = schema.Schema()
		}
		if d.ConfigSchema == nil {
			d.ConfigSchema = []gateway.ConfigField{}
		}
		out = append(out, d)
	}
	return out
}

// defaultRand is a thin io.Reader over crypto/rand, so Deps.Rand is optional.
type defaultRand struct{}

func (defaultRand) Read(p []byte) (int, error) { return rand.Read(p) }
