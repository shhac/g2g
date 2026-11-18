package git

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shhac/g2g/internal/atomicfile"
	"github.com/shhac/g2g/internal/landed"
)

// absorbedRules is the version of the rule absorbedEarlier applies. An answer
// remembered under another version is not this rule's answer and is thrown
// away. Any change to how absorption is decided -- the candidates, the
// witness files, what a merge has to give back -- must change it:
// TestRememberedVerdictsAreTiedToTheRuleThatMadeThem fails until it does.
const absorbedRules = 1

// verdictLimit is how many branches' answers are kept, and verdictChecks how
// many trunks a branch's "not absorbed" is remembered against. A branch is
// asked of its trunk and of the remote's copies of it, rarely of more.
const (
	verdictLimit  = 4000
	verdictChecks = 4
)

// verdict is what the earlier look found for one commit of a branch.
//
// Both halves are about commits, never names, which is what makes them safe
// to keep. A branch that moved has another commit and no answer. Absorbed is
// the base commit that held the branch's work: the answer is yes for any base
// that contains it. Checked are base commits the look reached without
// finding one: for a base that contains one of them, only the commits since
// are left to look at, which is what turns a search of everything the trunk
// gained since the branch left it into a search of what it gained today.
type verdict struct {
	Absorbed string   `json:"absorbed,omitempty"`
	Checked  []string `json:"checked,omitempty"`
	At       int64    `json:"at"`
}

type verdictFile struct {
	Rules    int                `json:"rules"`
	Verdicts map[string]verdict `json:"verdicts"`
}

// verdictWrites serialises this process's writes. Another process writing at
// the same moment can still lose one of the two an answer, and the cost is
// asking again.
var verdictWrites sync.Mutex

// asking is one question of the earlier look, as commits.
type asking struct {
	file, base, branch string
}

// asked resolves what an earlier look is about, in one process: where the
// answers are kept, and the two commits. It answers false when answers must
// not be remembered or cannot be placed.
func (c Client) asked(ctx context.Context, base, branch string) (asking, bool) {
	if landed.IsFresh(ctx) {
		return asking{}, false
	}
	output, err := c.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir", base+"^{commit}", branch+"^{commit}")
	lines := outputLines(output)
	if err != nil || len(lines) != 3 || !allObjectIDs(lines[1:]) {
		return asking{}, false
	}
	return asking{file: filepath.Join(lines[0], "g2g", "landed.json"), base: lines[1], branch: lines[2]}, true
}

// remembered is what was found before for this commit of the branch. A file
// that is missing, unreadable or made under another rule holds nothing.
func (a asking) remembered() verdict {
	return readVerdicts(a.file).Verdicts[a.branch]
}

func readVerdicts(path string) verdictFile {
	empty := verdictFile{Rules: absorbedRules, Verdicts: map[string]verdict{}}
	content, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var file verdictFile
	if json.Unmarshal(content, &file) != nil || file.Rules != absorbedRules || file.Verdicts == nil {
		return empty
	}
	return file
}

// remember keeps an answer. absorbed is the base commit that held the
// branch's work, empty when the look found none up to the base asked about;
// replaced is the checked commit that look started from, which the new one
// supersedes. A failure to write is not reported: the answer was given, and
// the cost of not keeping it is asking again.
func (a asking) remember(absorbed, replaced string) {
	verdictWrites.Lock()
	defer verdictWrites.Unlock()
	file := readVerdicts(a.file)
	kept := file.Verdicts[a.branch]
	kept.At = time.Now().Unix()
	if absorbed != "" {
		kept.Absorbed, kept.Checked = absorbed, nil
	} else {
		kept.Checked = slices.DeleteFunc(kept.Checked, func(commit string) bool { return commit == replaced || commit == a.base })
		kept.Checked = append(kept.Checked, a.base)
		if len(kept.Checked) > verdictChecks {
			kept.Checked = kept.Checked[len(kept.Checked)-verdictChecks:]
		}
	}
	file.Verdicts[a.branch] = kept
	forgetOldest(file.Verdicts)
	content, err := json.Marshal(file)
	if err != nil {
		return
	}
	_ = atomicfile.Write(a.file, content)
}

// forgetOldest drops the least recently answered half once there are more
// than verdictLimit, so branches long gone do not accumulate.
func forgetOldest(verdicts map[string]verdict) {
	if len(verdicts) <= verdictLimit {
		return
	}
	ages := make([]int64, 0, len(verdicts))
	for _, each := range verdicts {
		ages = append(ages, each.At)
	}
	slices.Sort(ages)
	cutoff := ages[len(ages)/2]
	for commit, each := range verdicts {
		if each.At < cutoff {
			delete(verdicts, commit)
		}
	}
}

// rememberedAbsorption answers from what was found before, where that still
// holds: yes when the base contains the commit that held the branch's work,
// no when the base is one the look already reached. Otherwise it says where
// the look can start from, which is a commit already reached that the base
// contains, or nowhere.
func (c Client) rememberedAbsorption(ctx context.Context, a asking) (answer, known bool, from string) {
	kept := a.remembered()
	if kept.Absorbed != "" {
		if contains, err := c.IsAncestor(ctx, kept.Absorbed, a.base); err == nil && contains {
			return true, true, ""
		}
	}
	if slices.Contains(kept.Checked, a.base) {
		return false, true, ""
	}
	for _, commit := range slices.Backward(kept.Checked) {
		if contains, err := c.IsAncestor(ctx, commit, a.base); err == nil && contains {
			return false, false, strings.TrimSpace(commit)
		}
	}
	return false, false, ""
}
