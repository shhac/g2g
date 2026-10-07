package repair

import "strings"

// Command renders an argv as a copyable POSIX-shell command, keeping argument
// boundaries intact and preventing shell expansion of names or messages.
func Command(args []string) string {
	parts := make([]string, len(args))
	for index, argument := range args {
		if argument != "" && !strings.ContainsFunc(argument, func(r rune) bool { return !shellSafe(r) }) {
			parts[index] = argument
		} else {
			parts[index] = "'" + strings.ReplaceAll(argument, "'", "'\\''") + "'"
		}
	}
	return strings.Join(parts, " ")
}

// Quote is one argument as Command renders it, for a name spliced into a
// command that also holds something the shell must see as written, such as a
// <branch> placeholder the reader fills in.
func Quote(argument string) string {
	return Command([]string{argument})
}

func shellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune("_+-./:=@", r)
	}
}
