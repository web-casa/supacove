// Package redact removes known secret values from arbitrary error text.
//
// Error text can carry the secret in several escape compositions: raw,
// conninfo single-quote-escaped (\'), backslash-doubled (\\), and JSON
// encoded (\\ before a quote, doubling every backslash). Instead of
// enumerating forms (which terminates badly — round-8 review R8-P1-01) or
// bounding escape depth (round-10 review: three consecutive backslashes
// escaped a 4/35 matrix slice), every character of the secret matches with
// ANY number of preceding backslashes, and the whole span is removed in a
// single regex pass.
package redact

import (
	"regexp"
	"strings"
)

const marker = "[REDACTED]"

// Secrets redacts every occurrence of the given secret values from s.
func Secrets(secrets []string, s string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		var pattern strings.Builder
		pattern.WriteString(`\\*`) // any escaping before the first char
		for _, r := range sec {
			pattern.WriteString(`\\*`) // escaping accumulated between chars
			pattern.WriteString(regexp.QuoteMeta(string(r)))
		}
		re, err := regexp.Compile(pattern.String())
		if err != nil {
			continue
		}
		s = re.ReplaceAllLiteralString(s, marker)
	}
	return s
}
