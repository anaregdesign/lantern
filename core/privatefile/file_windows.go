//go:build windows

package privatefile

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FILE_ALL_ACCESS from winnt.h; x/sys exposes the constituent rights.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func ownerDescriptor() (*windows.SECURITY_DESCRIPTOR, *windows.Tokenuser, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	return descriptor, user, err
}

func checkNative(file *os.File, _ os.FileInfo) error {
	handle := windows.Handle(file.Fd())
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		return ErrUnsafe
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil || information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrUnsafe
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || !descriptor.IsValid() {
		return ErrUnsafe
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return ErrUnsafe
	}
	owner, _, err := descriptor.Owner()
	user, userErr := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || userErr != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return ErrUnsafe
	}
	acl, defaulted, err := descriptor.DACL()
	if err != nil || defaulted || acl == nil || acl.AceCount != 1 {
		return ErrUnsafe
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil || ace == nil ||
		ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 ||
		ace.Mask & ^windows.ACCESS_MASK(fileAllAccess) != 0 || ace.Mask&windows.FILE_READ_DATA == 0 {
		return ErrUnsafe
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.IsValid() || int(ace.Header.AceSize) < int(unsafe.Offsetof(ace.SidStart))+sid.Len() || !sid.Equals(owner) {
		return ErrUnsafe
	}
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(user)
	return nil
}

func createNative(path string, flags int) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	descriptor, user, err := ownerDescriptor()
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	access := uint32(windows.READ_CONTROL)
	switch flags & (os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		access |= windows.GENERIC_WRITE
	case os.O_RDWR:
		access |= windows.GENERIC_READ | windows.GENERIC_WRITE
	default:
		access |= windows.GENERIC_READ
	}
	if flags&os.O_APPEND != 0 {
		access &^= windows.GENERIC_WRITE
		access |= windows.FILE_APPEND_DATA
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, &attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(user)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	if err := Check(file); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return file, nil
}

func syncDirectoryNative(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.Join(ErrUnsafe, windows.CloseHandle(handle))
	}
	return errors.Join(windows.FlushFileBuffers(handle), windows.CloseHandle(handle))
}
