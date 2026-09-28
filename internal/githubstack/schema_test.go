package githubstack

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// Every query and mutation this package sends is checked against GitHub's own
// schema, because a fake gh answers whatever it is asked. The stack comment's
// read asked for a field PullRequest has never had and every test passed; the
// first thing to notice was a real run.
//
// testdata/github.graphql is GitHub's published schema, kept verbatim so that
// refreshing it is one download:
//
//	curl -sSfL -o internal/githubstack/testdata/github.graphql \
//	  https://docs.github.com/public/fpt/schema.docs.graphql
//
// A new call to gh api graphql belongs in this table; nothing else will notice
// that it names a field GitHub does not have.
func TestEveryGraphQLDocumentIsValidAgainstGitHubsSchema(t *testing.T) {
	schema := loadGitHubSchema(t)
	for _, test := range []struct {
		name      string
		responses []string
		call      func(Client) error
	}{
		{name: "pull requests by head", call: func(c Client) error {
			_, err := c.Inspect(context.Background(), []string{"synthetic-lower", "synthetic-top"})
			return err
		}},
		{name: "mergeability", call: func(c Client) error {
			_, err := c.Mergeability(context.Background(), []int{11, 12})
			return err
		}},
		{
			name: "stack comments, and the page after",
			responses: []string{
				repositoryJSON(conversationJSON("c0", 11, "OPEN", true, "synthetic-cursor"), conversationJSON("c1", 12, "OPEN", false, "")),
				repositoryJSON(conversationJSON("c0", 11, "OPEN", false, "")),
			},
			call: func(c Client) error {
				_, err := c.Conversations(context.Background(), []int{11, 12}, syntheticMarker)
				return err
			},
		},
		{name: "add a comment", call: func(c Client) error {
			return c.AddComment(context.Background(), "PR_synthetic_11", "synthetic body")
		}},
		{name: "update a comment", call: func(c Client) error {
			return c.UpdateComment(context.Background(), "IC_synthetic_11", "synthetic body")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRunner{responses: test.responses}
			// Only what was sent is under test. A call with nothing scripted
			// fails once it has sent its document, which is all that is needed.
			_ = test.call(Client{Runner: runner})
			if len(runner.argv) == 0 {
				t.Fatal("nothing was sent to gh")
			}
			for _, argv := range runner.argv {
				checkDocument(t, schema, argv)
			}
		})
	}
}

func loadGitHubSchema(t *testing.T) *ast.Schema {
	t.Helper()
	input, err := os.ReadFile("testdata/github.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{Name: "github.graphql", Input: string(input)})
	if gqlErr != nil {
		t.Fatalf("load GitHub's schema: %v", gqlErr)
	}
	return schema
}

// checkDocument validates the one document an invocation carries, and that
// every variable it declares is one the invocation supplies: the schema cannot
// see the flags, and a variable nothing fills fails on GitHub just the same.
func checkDocument(t *testing.T, schema *ast.Schema, argv []string) {
	t.Helper()
	document, supplied := "", map[string]bool{}
	for index := 0; index+1 < len(argv); index++ {
		if argv[index] != "-f" && argv[index] != "-F" {
			continue
		}
		key, value, _ := strings.Cut(argv[index+1], "=")
		if key == "query" {
			document = value
			continue
		}
		supplied[key] = true
	}
	if document == "" {
		t.Fatalf("no query in %q", argv)
	}
	parsed, errs := gqlparser.LoadQuery(schema, document)
	if len(errs) != 0 {
		t.Fatalf("GitHub would refuse this document: %v\n%s", errs, document)
	}
	for _, operation := range parsed.Operations {
		for _, definition := range operation.VariableDefinitions {
			if !supplied[definition.Variable] {
				t.Errorf("$%s is declared and never supplied:\n%q", definition.Variable, argv)
			}
		}
	}
}
