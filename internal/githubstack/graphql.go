package githubstack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/shhac/g2g/internal/diagnostic"
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

// graphqlResponse is the envelope every batched read answers in: repository
// fields keyed by alias, and the errors that came with them.
//
// It is decoded here and nowhere else. The pull request lookup, the
// mergeability read and the comment read each used to decode it, check its
// errors and refuse a missing repository in words of their own, which is how a
// private copy of a bounded error path goes on to drift.
type graphqlResponse struct {
	Data struct {
		// Repository is keyed by field alias, and the fields are not all the
		// same shape: one names the repository and the rest are pull request
		// connections. Decoding each where it is read keeps one query able to
		// answer both.
		Repository map[string]json.RawMessage `json:"repository"`
	} `json:"data"`
	Errors []graphqlError `json:"errors"`
}

type graphqlError struct {
	Message string        `json:"message"`
	Type    string        `json:"type"`
	Path    []interface{} `json:"path"`
}

// repositoryFields decodes a batched response and returns its repository's
// fields. tolerate says which errors a read can live with; nil tolerates none.
//
// The response is read as the first JSON value in the output, because gh's
// output is combined with its stderr and a response carrying errors is
// followed by gh's own line about them.
func repositoryFields(output []byte, tolerate func([]graphqlError) bool) (map[string]json.RawMessage, error) {
	var response graphqlResponse
	if err := decodeFirstJSON(output, &response); err != nil {
		return nil, fmt.Errorf("parse gh api graphql JSON: %w", err)
	}
	if len(response.Errors) != 0 && (tolerate == nil || !tolerate(response.Errors)) {
		return nil, fmt.Errorf("gh api graphql returned errors: %s", diagnostic.BoundedOutput([]byte(response.Errors[0].Message)))
	}
	if response.Data.Repository == nil {
		return nil, fmt.Errorf("gh api graphql returned no repository; check that the GitHub CLI can read this repository")
	}
	return response.Data.Repository, nil
}

// aliasField decodes one field of the repository, naming what it was meant to
// be when it cannot.
func aliasField(repository map[string]json.RawMessage, alias, what string, into any) error {
	raw, exists := repository[alias]
	if !exists {
		return fmt.Errorf("gh api graphql response is missing %s", alias)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("gh api graphql response has invalid %s", strings.TrimSpace(alias+" "+what))
	}
	return nil
}

func decodeFirstJSON(output []byte, into any) error {
	start := bytes.IndexByte(output, '{')
	if start < 0 {
		return fmt.Errorf("no JSON in gh api graphql output")
	}
	return json.NewDecoder(bytes.NewReader(output[start:])).Decode(into)
}
