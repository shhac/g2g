package githubstack

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// graphqlString encodes s as a GraphQL string literal.
//
// strconv.Quote looks close enough and is not: Go's escapes include \xNN,
// \UXXXXXXXX and \a, none of which GraphQL has, so one unusual but legal
// branch name failed the whole batched query it rode in. Everything outside
// printable ASCII is written as \uXXXX — astral characters as a surrogate pair
// — because that form is valid under every published revision of the grammar,
// whereas raw astral characters and the braced \u{…} form are not.
//
// Prefer a variable where the query can declare one: then the value never
// enters the query text at all. This is for the places a literal is the
// simpler shape, such as an opaque pagination cursor.
func graphqlString(s string) string {
	var literal strings.Builder
	literal.Grow(len(s) + 2)
	literal.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			literal.WriteString(`\"`)
		case r == '\\':
			literal.WriteString(`\\`)
		case r >= 0x20 && r < 0x7f:
			literal.WriteRune(r)
		case r > 0xffff:
			high, low := utf16.EncodeRune(r)
			fmt.Fprintf(&literal, `\u%04X\u%04X`, high, low)
		default:
			fmt.Fprintf(&literal, `\u%04X`, r)
		}
	}
	literal.WriteByte('"')
	return literal.String()
}
