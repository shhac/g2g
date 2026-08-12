package cli

import "testing"

func TestColorEnabledHonorsTerminalEnvironment(t *testing.T) {
	for _, test := range []struct {
		name                        string
		noColor, ci, terminal, want bool
		term                        string
	}{
		{name: "color terminal", terminal: true, want: true},
		{name: "no color", noColor: true, terminal: true},
		{name: "continuous integration", ci: true, terminal: true},
		{name: "dumb terminal", term: "dumb", terminal: true},
		{name: "non terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := colorEnabled(test.noColor, test.term, test.ci, test.terminal); got != test.want {
				t.Errorf("colorEnabled() = %t, want %t", got, test.want)
			}
		})
	}
}
