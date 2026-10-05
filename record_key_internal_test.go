package aviato

import "testing"

func TestCompositeRecordKeyEncoding(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"1", "7", "1|7"},
		{"x|y", "z", "~787c79.7a"},
		{"x", "y|z", "~78.797c7a"},
		{"é|", "", "~c3a97c."},
	}
	for _, test := range cases {
		got := encodeID(Record{"a": test.a, "b": test.b}, []string{"a", "b"})
		if got != test.want {
			t.Errorf("encodeID(%q,%q) = %q, want %q", test.a, test.b, got, test.want)
		}
	}
}
