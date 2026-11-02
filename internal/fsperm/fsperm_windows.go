//go:build windows

package fsperm

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// restrictDir replaces dir's access control list with one that grants full control to the
// current user and SYSTEM only, inherited by everything inside (OICI), and protected (P) from
// the permissions of the folders above it.
func restrictDir(dir string) error {
	sid, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)", sid))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func restrictFile(string) error { return nil }

// Owner reports whether dir's access control list is the one restrictDir sets: the current user
// and SYSTEM, nobody else. dotlane doctor uses it in place of a mode check.
func Owner(dir string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false, err
	}
	sid, err := currentUser()
	if err != nil {
		return false, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false, err
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, err
		}
		who := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !who.Equals(sid) && !who.Equals(system) {
			return false, nil
		}
	}
	return true, nil
}

func currentUser() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}
