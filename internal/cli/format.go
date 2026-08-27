package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/shhac/g2g/internal/repair"
)

// outputFormat selects which renderer consumes a stackView. Pretty output is
// the default; the machine formats exist so callers stop scraping decorated
// terminal text, which is why the renderer has always kept plan data separate
// from its ANSI decoration.
type outputFormat string

const (
	formatPretty    outputFormat = ""
	formatJSON      outputFormat = "json"
	formatPorcelain outputFormat = "porcelain"
)

// schemaVersion is bumped when a field changes meaning or disappears. Adding a
// field is not a breaking change and does not bump it.
//
// 2 narrowed "blocked" to the reason alone. It used to carry the label a person
// is shown in front of it — "Apply blocked: ", or "Safe next action: " from
// status, which is the same field saying two different things — and a consumer
// was left trimming a presentation decision out of its data. The label is now
// the renderer's. Read "repair" for the ways out, which this version added and
// which is why the sentence no longer has to be parsed to find the command.
const schemaVersion = 2

type jsonDocument struct {
	SchemaVersion int          `json:"schemaVersion"`
	Operation     string       `json:"operation"`
	Target        string       `json:"target"`
	TargetSource  string       `json:"targetSource,omitempty"`
	Trunk         string       `json:"trunk"`
	Branches      []jsonBranch `json:"branches"`
	Command       []string     `json:"command,omitempty"`
	// Blocked is the reason apply would refuse. It is reported alongside
	// Command rather than instead of it, so a consumer can see the plan's
	// destination and decide for itself; check this before acting on Command.
	Blocked string `json:"blocked,omitempty"`
	// Repair is what to do about Blocked, with the commands separate from the
	// prose. It is also set where nothing is blocked and there is still
	// something to do — a branch no source describes is a state, not a refusal.
	Repair *jsonRepair `json:"repair,omitempty"`
	Notes  []jsonNote  `json:"notes,omitempty"`
}

// jsonRepair is repair.Note as the document carries it. The domain type is not
// serialized directly: its shape is the renderer's business, and a JSON tag on
// it would make every package that builds one answerable for this schema.
type jsonRepair struct {
	// Reason is why, without advice. It is empty when the ways say all there
	// is to say.
	Reason string    `json:"reason,omitempty"`
	Ways   []jsonWay `json:"ways"`
}

// jsonWay is one way out. Command is absent when the way out is not something
// to run — "fetch and reconcile first" — which is a whole answer rather than a
// step with a missing field.
type jsonWay struct {
	Command string `json:"command,omitempty"`
	Effect  string `json:"effect"`
}

type jsonBranch struct {
	Branch string `json:"branch"`
	// Parent carries the structure that order alone cannot express once a
	// graph forks. A linear plan leaves it empty and its order still holds.
	Parent      string `json:"parent,omitempty"`
	Target      bool   `json:"target,omitempty"`
	PullRequest int    `json:"pullRequest,omitempty"`
	URL         string `json:"url,omitempty"`
	State       string `json:"state,omitempty"`
	Severity    string `json:"severity,omitempty"`
}

type jsonNote struct {
	Text     string `json:"text"`
	Severity string `json:"severity"`
}

func (v stackView) document() jsonDocument {
	doc := jsonDocument{
		SchemaVersion: schemaVersion,
		Operation:     v.Operation,
		Target:        v.Target,
		TargetSource:  v.TargetSource,
		Command:       v.Action,
		Blocked:       plainCommands(v.Blocked),
		Repair:        repairDocument(v.Repair),
		Branches:      []jsonBranch{},
	}
	for _, node := range v.Nodes {
		if node.Trunk {
			doc.Trunk = node.Branch
			continue
		}
		doc.Branches = append(doc.Branches, jsonBranch{
			Branch:      node.Branch,
			Parent:      node.Parent,
			Target:      node.Target,
			PullRequest: node.PRNumber,
			URL:         node.PRURL,
			State:       node.State,
			Severity:    string(node.Severity),
		})
	}
	for _, note := range v.Notes {
		doc.Notes = append(doc.Notes, jsonNote{Text: plainCommands(note.Text), Severity: string(note.Severity)})
	}
	return doc
}

// repairDocument carries a repair only where there is one. A note with no ways
// out is a refusal nothing here fixes, and an empty array would read as a
// promise that there is something to run.
func repairDocument(note repair.Note) *jsonRepair {
	if len(note.Ways) == 0 {
		return nil
	}
	ways := make([]jsonWay, 0, len(note.Ways))
	for _, way := range note.Ways {
		ways = append(ways, jsonWay{Command: way.Command, Effect: way.Effect})
	}
	return &jsonRepair{Reason: note.Reason, Ways: ways}
}

func writeJSON(writer io.Writer, view stackView) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(view.document())
}

// writePorcelain emits one tab-separated record per line, leading with a
// record type so a reader can switch on it and ignore fields added later.
func writePorcelain(writer io.Writer, view stackView) error {
	doc := view.document()
	records := [][]string{{"target", doc.Target, doc.TargetSource}, {"trunk", doc.Trunk}}
	for _, branch := range doc.Branches {
		records = append(records, []string{
			// parent is appended rather than inserted: a reader switches on
			// the record type and ignores fields added after the ones it knows.
			"branch", branch.Branch, porcelainNumber(branch.PullRequest), branch.State, branch.Severity, branch.URL, porcelainBool(branch.Target), branch.Parent,
		})
	}
	if doc.Blocked != "" {
		records = append(records, []string{"blocked", doc.Blocked})
	}
	if doc.Repair != nil {
		records = append(records, []string{"repair", doc.Repair.Reason})
		for _, way := range doc.Repair.Ways {
			records = append(records, []string{"way", way.Command, way.Effect})
		}
	}
	if len(doc.Command) != 0 {
		records = append(records, append([]string{"command"}, doc.Command...))
	}
	for _, note := range doc.Notes {
		records = append(records, []string{"note", note.Severity, note.Text})
	}

	var out strings.Builder
	for _, record := range records {
		out.WriteString(strings.Join(record, "\t"))
		out.WriteString("\n")
	}
	_, err := io.WriteString(writer, out.String())
	return err
}

func porcelainNumber(number int) string {
	if number == 0 {
		return ""
	}
	return fmt.Sprint(number)
}

func porcelainBool(value bool) string {
	if value {
		return "target"
	}
	return ""
}

// prose writes a human-facing line. Machine formats emit the document and
// nothing else, so their output can be piped without post-processing.
//
// The line is drawn a second time here because a caller may name a command
// outside anything it styles. Drawing is idempotent — a line whose commands
// were already resolved carries no marks left to find — so this covers the
// unstyled remainder without disturbing the rest.
func prose(writer io.Writer, p Presentation, line string) error {
	if p.machine() {
		return nil
	}
	_, err := fmt.Fprintln(writer, p.drawCommands(line, ""))
	return err
}
