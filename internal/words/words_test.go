package words

import "testing"

func TestCountAndList(t *testing.T) {
	for _, test := range []struct{ got, want string }{
		{Count(0, "commit", "commits"), "0 commits"},
		{Count(1, "commit", "commits"), "1 commit"},
		{Count(3, "commit", "commits"), "3 commits"},
		{List(nil), ""},
		{List([]string{"synthetic-a"}), "synthetic-a"},
		{List([]string{"synthetic-a", "synthetic-b"}), "synthetic-a and synthetic-b"},
		{List([]string{"synthetic-a", "synthetic-b", "synthetic-c"}), "synthetic-a, synthetic-b and synthetic-c"},
	} {
		if test.got != test.want {
			t.Errorf("got %q, want %q", test.got, test.want)
		}
	}
}
