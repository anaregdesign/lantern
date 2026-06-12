package mcp

import "testing"

func TestParseLanternAddrs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "http://a:6380", []string{"http://a:6380"}},
		{"two", "http://a:6380,http://b:6380", []string{"http://a:6380", "http://b:6380"}},
		{"trim-and-drop-empties", " http://a:6380 , , http://b:6380 ,", []string{"http://a:6380", "http://b:6380"}},
		{"empty", "", nil},
		{"only-separators", " , ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLanternAddrs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("parseLanternAddrs(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseLanternAddrs(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}
