package shape

// How a selection's target was chosen: named with --branch, or the branch
// checked out. The g2g graph and the stack resolver both report one, and a
// suggestion names the branch only when it was named, so a wording changed in
// one place would quietly aim suggestions at wherever the reader stands.
const (
	TargetNamed   = "--branch"
	TargetCurrent = "current Git branch"
)
