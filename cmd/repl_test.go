package cmd

import (
	"reflect"
	"testing"
)

func TestSplitLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"send hello", []string{"send", "hello"}},
		{`send "hello world"`, []string{"send", "hello world"}},
		{"send 'quoted phrase' trailing", []string{"send", "quoted phrase", "trailing"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
	}
	for _, tc := range cases {
		got := splitLine(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitLine(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestParseInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"42", 42},
		{"0", 0},
		{"", 0},
		{"12x", 0},
		{"general", 0},
	}
	for _, tc := range cases {
		if got := parseInt(tc.in); got != tc.want {
			t.Errorf("parseInt(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
