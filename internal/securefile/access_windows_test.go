package securefile

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWriteAtomicRestrictsWindowsACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "config.yaml")
	if err := WriteAtomic(path, []byte("token: secret\n")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "token: secret\n" {
		t.Fatalf("private file data=%q err=%v", data, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Dir(path), path} {
		sd, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := sd.DACL()
		if err != nil || dacl == nil || dacl.AceCount != 1 {
			t.Fatalf("%s DACL: ace count=%v err=%v", target, dacl, err)
		}
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("%s inherited DACL: control=%v err=%v", target, control, err)
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		if !user.User.Sid.Equals((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			t.Fatalf("%s DACL does not name current user: %s", target, sd.String())
		}
	}
}
