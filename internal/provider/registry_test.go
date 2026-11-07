package provider

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
	"github.com/learningdog1/ada_proxy/internal/logging"
)

// fakeAdapter is a minimal Adapter used to exercise the registry.
type fakeAdapter struct {
	kind  gateway.Kind
	fails bool
}

func (f *fakeAdapter) Kind() gateway.Kind                          { return f.kind }
func (f *fakeAdapter) Capabilities() gateway.Capabilities          { return gateway.Capabilities{Stream: true} }
func (f *fakeAdapter) ValidateConfig(gateway.ProviderConfig) error { return nil }
func (f *fakeAdapter) Do(context.Context, *gateway.InvokeRequest) (*http.Response, error) {
	return nil, gateway.ErrUnsupported
}
func (f *fakeAdapter) DecodeResponse([]byte, *gateway.InvokeRequest) (*ir.ChatResponse, error) {
	return &ir.ChatResponse{}, nil
}
func (f *fakeAdapter) DecodeStream(io.Reader, *gateway.InvokeRequest) gateway.StreamDecoder {
	return nil
}

// fakeSchemaAdapter also implements ConfigSchemaProvider.
type fakeSchemaAdapter struct{ fakeAdapter }

func (f *fakeSchemaAdapter) Schema() []gateway.ConfigField {
	return []gateway.ConfigField{{Key: "x", Label: "X", Type: "string"}}
}

// fakeReverseAdapter declares a non-default access mode.
type fakeReverseAdapter struct{ fakeAdapter }

func (f *fakeReverseAdapter) AccessMode() gateway.AccessMode { return gateway.AccessReverseProxy }

func TestAccessModeClassification(t *testing.T) {
	plain := gateway.Kind("test_plain_api")
	Register(plain, "Plain", func(Deps) (gateway.Adapter, error) {
		return &fakeAdapter{kind: plain}, nil
	})
	reverse := gateway.Kind("test_reverse")
	Register(reverse, "Reverse", func(Deps) (gateway.Adapter, error) {
		return &fakeReverseAdapter{fakeAdapter{kind: reverse}}, nil
	})

	reg := Build(Deps{Log: logging.Discard()})
	if got := reg.AccessMode(plain); got != gateway.AccessAPI {
		t.Fatalf("plain adapter access mode = %q, want the api default", got)
	}
	if got := reg.AccessMode(reverse); got != gateway.AccessReverseProxy {
		t.Fatalf("reverse adapter access mode = %q, want reverse_proxy", got)
	}
	if got := reg.AccessMode("test_unknown"); got != gateway.AccessAPI {
		t.Fatalf("unknown kind access mode = %q, want the api default", got)
	}
	for _, d := range reg.Descriptors() {
		switch d.Kind {
		case plain:
			if d.AccessMode != gateway.AccessAPI {
				t.Fatalf("descriptor for %q = %q, want api", plain, d.AccessMode)
			}
		case reverse:
			if d.AccessMode != gateway.AccessReverseProxy {
				t.Fatalf("descriptor for %q = %q, want reverse_proxy", reverse, d.AccessMode)
			}
		}
	}
}

func TestBuildSkipsFailingAdapters(t *testing.T) {
	kind := gateway.Kind("test_failing")
	Register(kind, "Failing", func(Deps) (gateway.Adapter, error) {
		return nil, context.Canceled
	})

	reg := Build(Deps{Log: logging.Discard()})
	if _, ok := reg.Get(kind); ok {
		t.Fatal("a failing adapter was registered")
	}
	// The build must succeed overall: one broken vendor cannot stop the gateway.
	if reg == nil {
		t.Fatal("Build returned nil")
	}
}

func TestBuildAndDescribe(t *testing.T) {
	kind := gateway.Kind("test_described")
	Register(kind, "Described", func(Deps) (gateway.Adapter, error) {
		return &fakeSchemaAdapter{fakeAdapter{kind: kind}}, nil
	})

	reg := Build(Deps{Log: logging.Discard()})
	adapter, ok := reg.Get(kind)
	if !ok {
		t.Fatal("registered adapter was not built")
	}
	if adapter.Kind() != kind {
		t.Fatalf("kind = %q", adapter.Kind())
	}
	if reg.Label(kind) != "Described" {
		t.Fatalf("label = %q", reg.Label(kind))
	}

	var found *Descriptor
	for i := range reg.Descriptors() {
		if reg.Descriptors()[i].Kind == kind {
			found = &reg.Descriptors()[i]
		}
	}
	if found == nil {
		t.Fatal("adapter missing from Descriptors")
	}
	if len(found.ConfigSchema) != 1 || found.ConfigSchema[0].Key != "x" {
		t.Fatalf("schema = %+v", found.ConfigSchema)
	}
	if !found.Capabilities.Stream {
		t.Fatal("capabilities were not copied")
	}
}

func TestAdapterWithoutSchemaGetsEmptyList(t *testing.T) {
	kind := gateway.Kind("test_no_schema")
	Register(kind, "No Schema", func(Deps) (gateway.Adapter, error) {
		return &fakeAdapter{kind: kind}, nil
	})

	reg := Build(Deps{Log: logging.Discard()})
	for _, d := range reg.Descriptors() {
		if d.Kind != kind {
			continue
		}
		if d.ConfigSchema == nil {
			t.Fatal("ConfigSchema is nil; the JSON would be null instead of []")
		}
		if len(d.ConfigSchema) != 0 {
			t.Fatalf("schema = %+v, want empty", d.ConfigSchema)
		}
		return
	}
	t.Fatal("adapter missing from Descriptors")
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	kind := gateway.Kind("test_duplicate")
	Register(kind, "First", func(Deps) (gateway.Adapter, error) { return &fakeAdapter{kind: kind}, nil })

	defer func() {
		if recover() == nil {
			t.Fatal("registering the same kind twice did not panic")
		}
	}()
	Register(kind, "Second", func(Deps) (gateway.Adapter, error) { return &fakeAdapter{kind: kind}, nil })
}

func TestNilRegistryIsSafe(t *testing.T) {
	var reg *Registry
	if _, ok := reg.Get(KindCommandCode); ok {
		t.Fatal("nil registry returned an adapter")
	}
	if reg.Kinds() != nil {
		t.Fatal("nil registry returned kinds")
	}
	if reg.Descriptors() != nil {
		t.Fatal("nil registry returned descriptors")
	}
	if reg.Label(KindCommandCode) != "" {
		t.Fatal("nil registry returned a label")
	}
	if reg.AccessMode(KindCommandCode) != gateway.AccessAPI {
		t.Fatal("nil registry returned a non-default access mode")
	}
}
