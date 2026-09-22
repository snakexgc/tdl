package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const redacted = "[REDACTED]"

var (
	credentialsURL   = regexp.MustCompile(`(?i)(https?|socks5h?)://[^\s/@]+@`)
	botToken         = regexp.MustCompile(`\b(?:bot)?[0-9]{5,}:[A-Za-z0-9_-]{20,}\b`)
	authorization    = regexp.MustCompile(`(?i)\b((?:proxy[-_ ]?)?authorization["\s:=]+)(?:bearer|basic)\s+[^\s,;"}]+`)
	secretAssignment = regexp.MustCompile(`(?i)\b((?:[a-z_]*token|password|passwd|api[_-]?hash|app[_-]?hash|secret|auth[_-]?key|authorization|session|phone|code)(?:["']?\s*[:=]\s*|\s+))(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&"'}]+)`)
	// Match quoted values separately so spaces inside JSON secrets are removed.
	quotedSecret = regexp.MustCompile(`(?i)("(?:[a-z_]*token|password|passwd|api[_-]?hash|app[_-]?hash|secret|auth[_-]?key|authorization|session|phone|code)"\s*:\s*)"(?:\\.|[^"\\])*"`)
)

func Redact(value string) string {
	value = credentialsURL.ReplaceAllString(value, "$1://[REDACTED]@")
	value = botToken.ReplaceAllString(value, redacted)
	value = quotedSecret.ReplaceAllString(value, `${1}"[REDACTED]"`)
	value = authorization.ReplaceAllString(value, "${1}[REDACTED]")
	value = secretAssignment.ReplaceAllString(value, "${1}[REDACTED]")
	return bounded(value, 8192)
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}

func sensitiveKey(key string) bool {
	// Check path segments, without hiding useful fields such as error_code,
	// request_id, response_status, token_count or bytes_written.
	for _, part := range strings.Split(strings.ToLower(key), ".") {
		part = strings.NewReplacer("_", "", "-", "").Replace(part)
		for _, secret := range []string{"password", "passwd", "token", "secret", "apikey", "apihash", "apphash", "authkey", "accesshash", "filereference", "authorization", "cookie", "headers", "body", "payload", "phone", "phonenumber", "session", "sessionid", "sessionstring", "sessiondata", "sessionkey", "phonecodehash", "logincode", "verificationcode"} {
			if strings.HasSuffix(part, secret) {
				return true
			}
		}
		switch part {
		case "session", "sessionstring", "sessiondata", "phone", "phonenumber", "code", "logincode", "verificationcode", "otp", "payload", "body", "text", "request", "response", "config", "configuration":
			return true
		}
	}
	return false
}

func safeValue(key string, value any) any {
	if sensitiveKey(key) {
		return redacted
	}
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		for name, v := range item {
			result[name] = safeValue(name, v)
		}
		return result
	case []any:
		result := make([]any, 0, len(item))
		for _, v := range item {
			result = append(result, safeValue("", v))
		}
		return result
	case string:
		return Redact(item)
	case error:
		return Redact(item.Error())
	case nil, bool, float64, json.Number:
		return item
	default:
		data, err := json.Marshal(value)
		var normalized any
		if err == nil && decodeJSON(data, &normalized) == nil {
			return safeValue("", normalized)
		}
		return Redact(fmt.Sprint(value))
	}
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func safeDetails(value any) string {
	data, err := json.Marshal(safeValue("", value))
	if err != nil {
		return ""
	}
	if len(data) <= 16384 {
		return string(data)
	}
	// Keep truncated details valid JSON for both the drawer and JSON export.
	preview := string(data)
	for limit := 8192; ; limit /= 2 {
		out, _ := json.Marshal(map[string]any{"truncated": true, "original_bytes": len(data), "preview": bounded(preview, limit)})
		if len(out) <= 16384 {
			return string(out)
		}
	}
}
