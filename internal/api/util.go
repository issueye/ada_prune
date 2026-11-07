package api

import (
	"encoding/json"
	"errors"

	"github.com/learningdog1/ada_proxy/internal/httpkit"
)

// asTooLarge is errors.As with a concrete target, kept local so dataplane.go does
// not import errors for one call.
func asTooLarge(err error, target **httpkit.BodyTooLarge) bool {
	return errors.As(err, target)
}

// jsonUnmarshal is a thin alias so callers do not import encoding/json for one
// probe.
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// itoa renders a small int without the fmt dependency.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
