//go:build windows

package clientruntime

import (
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

func writePrivateFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := restrictCurrentUser(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows does not replace an existing destination with Rename. Status
		// files and active receipts are rewritten during retries and updates.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			_ = os.Remove(tmp)
			return err
		}
		if renameErr := os.Rename(tmp, path); renameErr != nil {
			_ = os.Remove(tmp)
			return renameErr
		}
	}
	if err := restrictCurrentUser(path); err != nil {
		return err
	}
	_ = restrictCurrentUser(filepath.Dir(path))
	return nil
}

func restrictCurrentUser(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid := user.User.Sid
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	// Directories must grant inheritably. Restricting a directory with an
	// object-only ACE makes Windows' automatic propagation strip the children's
	// inherited ACEs (they mirror the old inheritable grants) without anything
	// to re-inherit, leaving subdirectories with an empty deny-all DACL.
	inheritance := uint32(0)
	if st, statErr := os.Stat(path); statErr == nil && st.IsDir() {
		inheritance = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}
