package daemon_test

import (
	"testing"

	"project/src/common/daemon"
)

func TestFilterArgs_RemovesDaemonFlag(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{
			in:   []string{"--daemon", "start"},
			want: []string{"start"},
		},
		{
			in:   []string{"-d", "start"},
			want: []string{"start"},
		},
		{
			in:   []string{"--conf-file", "a.yaml", "--daemon", "start"},
			want: []string{"--conf-file", "a.yaml", "start"},
		},
		{
			in:   []string{"start"},
			want: []string{"start"},
		},
		{
			in:   []string{},
			want: []string{},
		},
		{
			in:   []string{"--daemon=true", "start"},
			want: []string{"start"},
		},
		{
			in:   []string{"--daemon=1", "start"},
			want: []string{"start"},
		},
	}
	for _, c := range cases {
		got := daemon.FilterArgs(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("in=%v: got %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("in=%v: got[%d]=%q, want[%d]=%q", c.in, i, got[i], i, c.want[i])
			}
		}
	}
}
