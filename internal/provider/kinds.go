package provider

import "github.com/learningdog1/ada_prune/internal/gateway"

// Adapter kinds. They live here rather than in internal/gateway so the gateway
// core never learns a vendor's name; the string values are what
// providers.kind stores and what the registry keys on.
const (
	// KindCommandCode is the Command Code subscription upstream.
	KindCommandCode gateway.Kind = "command_code"
	// KindOpenAICompatible is any OpenAI-shaped upstream.
	KindOpenAICompatible gateway.Kind = "openai_compatible"
	// KindAnthropic is the native Anthropic Messages API.
	KindAnthropic gateway.Kind = "anthropic"
	// KindCustom is reserved for user-supplied adapters.
	KindCustom gateway.Kind = "custom"
)
