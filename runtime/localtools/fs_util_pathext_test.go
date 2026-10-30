package localtools

import "testing"

func TestUnit_HasExecutableExtension_UsesPATHEXT(t *testing.T) {
	cases := []struct {
		name, pathext string
		want          bool
	}{
		{"tool.EXE", ".COM;.EXE;.BAT;.CMD;.VBS;.JS", true},
		{"script.vbs", ".COM;.EXE;.BAT;.CMD;.VBS;.JS", true},
		{"script.js", ".COM;.EXE;.BAT;.CMD;.VBS;.JS", true},
		{"run.ps1", ".COM;.EXE;.BAT;.CMD", false}, // not in PATHEXT, so the shell would not run it by name
		{"tool.exe", ".com;.bat", false},
		{"tool.py", ".PY;.PYW", true},
		{"tool.py", "py;pyw ; ", true}, // missing dots and blanks tolerated
		{"noext", ".EXE", false},
	}
	for _, c := range cases {
		if got := hasExecutableExtension(c.name, c.pathext); got != c.want {
			t.Errorf("hasExecutableExtension(%q, %q) = %v, want %v", c.name, c.pathext, got, c.want)
		}
	}
}

func TestUnit_HasExecutableExtension_FallsBackWhenPATHEXTEmpty(t *testing.T) {
	for _, pathext := range []string{"", " ; ;"} {
		for _, name := range []string{"a.exe", "a.COM", "a.bat", "a.cmd", "a.ps1"} {
			if !hasExecutableExtension(name, pathext) {
				t.Errorf("%q should be executable with PATHEXT %q", name, pathext)
			}
		}
		if hasExecutableExtension("a.txt", pathext) {
			t.Errorf("a.txt must not be executable with PATHEXT %q", pathext)
		}
	}
}
