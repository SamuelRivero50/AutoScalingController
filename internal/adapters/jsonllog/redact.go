package jsonllog

import "regexp"

// Redaction patterns, compiled once. An AWS account ID is 12 digits, alone
// or inside an ARN; access key IDs start with AKIA (long-term) or ASIA
// (temporary).
var (
	accountIDPattern = regexp.MustCompile(`(^|[^0-9])[0-9]{12}([^0-9]|$)`)
	accessKeyPattern = regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`)
)

const redacted = "REDACTED"

// redact removes AWS account IDs and access key IDs from s.
func redact(s string) string {
	s = accessKeyPattern.ReplaceAllString(s, redacted)
	return accountIDPattern.ReplaceAllString(s, "${1}"+redacted+"${2}")
}

// redactValue redacts every string inside a JSON-compatible value.
func redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return redact(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = redactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactValue(val)
		}
		return out
	case []string:
		out := make([]string, len(x))
		for i, val := range x {
			out[i] = redact(val)
		}
		return out
	default:
		return v
	}
}
