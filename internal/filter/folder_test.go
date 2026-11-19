package filter

import (
	"errors"
	"testing"
)

func TestCheckPath(t *testing.T) {
	win := Env{Home: `C:\Users\Anna`, WinDir: `C:\Windows`, ProgramFiles: `C:\Program Files`, ProgramData: `C:\ProgramData`}
	unix := Env{Home: "/home/anna"}
	cases := []struct {
		name string
		path string
		env  Env
		bad  bool
	}{
		{"drive root C", `C:\`, win, true},
		{"drive root D lower", `d:/`, win, true},
		{"drive without slash", `D:`, win, true},
		{"drive relative", `D:docs`, win, true},
		{"unc share root", `\\server\share`, win, true},
		{"unc server only", `\\server`, win, true},
		{"device path", `\\?\C:\Data`, win, true},
		{"windows dir", `C:\Windows`, win, true},
		{"windows system32", `C:\WINDOWS\System32`, win, true},
		{"windows on other drive", `D:\Windows\Temp`, Env{}, true},
		{"program files", `C:\Program Files`, win, true},
		{"program files x86", `C:\Program Files (x86)\Foo`, win, true},
		{"programdata", `C:\ProgramData\x`, win, true},
		{"profile itself", `C:\Users\Anna`, win, true},
		{"profile other user, no env", `C:\Users\Bob`, Env{}, true},
		{"users folder", `C:\Users`, win, true},
		{"appdata", `C:\Users\Anna\AppData`, win, true},
		{"appdata roaming", `C:\Users\Anna\AppData\Roaming\X`, win, true},
		{"dotdot to root", `C:\Users\Anna\..\..`, win, true},
		{"forward slashes", `C:/Windows/System32`, win, true},
		{"custom profile location", `D:\profiles\anna`, Env{Home: `D:\Profiles\Anna`}, true},
		{"relative", `docs`, win, true},
		{"documents folder", `C:\Users\Anna\Documents`, win, false},
		{"folder on D", `D:\Belege`, win, false},
		{"unc subfolder", `\\server\share\Belege`, win, false},
		{"unix root", `/`, unix, true},
		{"unix etc", `/etc`, unix, true},
		{"unix etc sub", `/etc/ssl`, unix, true},
		{"unix usr", `/usr/local`, unix, true},
		{"unix bin", `/bin`, unix, true},
		{"macos system", `/System/Library`, unix, true},
		{"home itself", `/home/anna`, unix, true},
		{"home parent", `/home`, unix, true},
		{"home dotdot", `/home/anna/docs/../..`, unix, true},
		{"home subfolder", `/home/anna/Belege`, unix, false},
		{"srv data", `/srv/belege`, unix, false},
	}
	for _, c := range cases {
		err := CheckPath(c.path, c.env)
		if c.bad && !errors.Is(err, ErrForbiddenFolder) {
			t.Errorf("%s: %q should be rejected, got %v", c.name, c.path, err)
		}
		if !c.bad && err != nil {
			t.Errorf("%s: %q should be allowed, got %v", c.name, c.path, err)
		}
	}
}
