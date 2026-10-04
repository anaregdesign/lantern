//go:build windows

package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsHandleOwnerDACL(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	for _, test := range []struct {
		name, dacl         string
		protected, allowed bool
	}{
		{"owner_full", "D:P(A;;FA;;;" + sid + ")", true, true},
		{"owner_read", "D:P(A;;FR;;;" + sid + ")", true, true},
		{"world", "D:P(A;;FA;;;WD)", true, false},
		{"extra_grant", "D:P(A;;FA;;;" + sid + ")(A;;FR;;;WD)", true, false},
		{"unprotected", "D:(A;;FA;;;" + sid + ")", false, false},
		{"deny_ace", "D:P(D;;FW;;;WD)(A;;FA;;;" + sid + ")", true, false},
		{"unknown_mask", "D:P(A;;0x02000000;;;" + sid + ")", true, false},
		{"empty", "D:P", true, false},
		{"null", "", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := Create(filepath.Join(t.TempDir(), "private"), os.O_RDWR)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			name, err := windows.UTF16PtrFromString(file.Name())
			if err != nil {
				t.Fatal(err)
			}
			// Production handles deliberately lack WRITE_DAC. Only this test
			// handle may install adversarial ACLs on the owned fixture.
			control, err := windows.CreateFile(name, windows.WRITE_DAC|windows.READ_CONTROL,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
				nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.CloseHandle(control) }()
			original, err := windows.GetSecurityInfo(control, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			originalACL, _, err := original.DACL()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := windows.SetSecurityInfo(control, windows.SE_FILE_OBJECT,
					windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
					nil, nil, originalACL, nil); err != nil {
					t.Error("restore owned fixture DACL", err)
				}
				runtime.KeepAlive(original)
			}()
			var acl *windows.ACL
			var descriptor *windows.SECURITY_DESCRIPTOR
			if test.dacl != "" {
				descriptor, err = windows.SecurityDescriptorFromString("O:" + sid + test.dacl)
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err = descriptor.DACL()
				if err != nil {
					t.Fatal(err)
				}
			}
			information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
			if !test.protected {
				information = windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
			}
			if err := windows.SetSecurityInfo(control, windows.SE_FILE_OBJECT, information, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			runtime.KeepAlive(descriptor)
			err = Check(file)
			if test.allowed && err != nil || !test.allowed && !errors.Is(err, ErrUnsafe) {
				t.Fatal("handle-bound DACL contract", err)
			}
		})
	}
}
