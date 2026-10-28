package workspacegrants

import (
	"runtime"
	"testing"
)

func TestUnit_IsBroadParent_TopLevelDirsIgnoreVolumeName(t *testing.T) {
	type tc struct {
		path  string
		broad bool
	}
	var cases []tc
	if runtime.GOOS == "windows" {
		cases = []tc{
			{`C:\`, true},
			{`C:\Windows`, true},
			{`C:\Users`, true},
			{`D:\Projects`, true},
			{`\\server\share\top`, true},
			{`C:\Users\someone\project`, false},
			{`D:\Projects\app`, false},
		}
	} else {
		cases = []tc{
			{"/", true},
			{"/etc", true},
			{"/usr", true},
			{"/srv/project", false},
			{"/opt/app/sub", false},
		}
	}
	for _, c := range cases {
		got, why := isBroadParent(c.path)
		if got != c.broad {
			t.Errorf("isBroadParent(%q) = %v (%q), want %v", c.path, got, why, c.broad)
		}
	}
}
