// Phrases every view shares that are about stacks rather than grammar; the
// grammar -- agreeing with a count, listing names -- is internal/words.
package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/graph"
)

// landingPhrase is where a declared trunk goes, as the tail of a sentence.
func landingPhrase(declaration graph.Declaration) string {
	if !declaration.Lands() {
		return ""
	}
	return fmt.Sprintf(" that lands into %s by %s", declaration.Into, declaration.By)
}
