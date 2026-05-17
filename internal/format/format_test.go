package format_test

import (
	"strings"
	"testing"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/format"
)

func TestSafeFileName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"My Book", "My Book"},
		{"foo/bar", "foobar"},
		{`bad<>:"|?*chars`, "badchars"},
		{"trailing.. ", "trailing"},
		{"", "untitled"},
		{"   ", "untitled"},
		{"x" + strings.Repeat("a", 500), "x" + strings.Repeat("a", 179)},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := format.SafeFileName(c.in)
			if got != c.want {
				t.Errorf("SafeFileName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
