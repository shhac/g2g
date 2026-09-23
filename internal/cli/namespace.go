package cli

import "github.com/spf13/cobra"

// The command surface, arranged.
//
// Managing your own stack is the tool, so those commands are at the top level
// and read like git's: status, create, pull, push. What is specific to another
// tool lives under that tool's name — `g2g github …`, `g2g graphite …` — so a
// command says in its own words when it reaches past git, and someone who
// uses neither never has to read about them.

// Help groups, in the order a person meets them.
const (
	groupLook    = "look"
	groupShape   = "shape"
	groupMove    = "move"
	groupUpdate  = "update"
	groupPublish = "publish"
	groupTools   = "tools"
)

func commandGroups() []*cobra.Group {
	return []*cobra.Group{
		{ID: groupLook, Title: "See where you are:"},
		{ID: groupShape, Title: "Shape the stack:"},
		{ID: groupMove, Title: "Move around:"},
		{ID: groupUpdate, Title: "Keep it current:"},
		{ID: groupPublish, Title: "Publish and land:"},
		{ID: groupTools, Title: "Other tools:"},
	}
}

// namespace is a command that only holds others; run bare, it shows them.
func namespace(name, short, long string, children ...*cobra.Command) *cobra.Command {
	parent := &cobra.Command{
		Use:     name,
		GroupID: groupTools,
		Short:   short,
		Long:    long,
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	for _, child := range children {
		// A group names a heading in the root's help, and a nested command is
		// listed under its namespace instead.
		child.GroupID = ""
		parent.AddCommand(child)
	}
	return parent
}
