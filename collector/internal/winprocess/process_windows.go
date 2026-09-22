//go:build windows

package winprocess

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func CurrentUserExecutable(pid uint32) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)

	var processToken windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &processToken); err != nil {
		return "", err
	}
	defer processToken.Close()
	processUser, err := processToken.GetTokenUser()
	if err != nil {
		return "", err
	}
	currentToken, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer currentToken.Close()
	currentUser, err := currentToken.GetTokenUser()
	if err != nil {
		return "", err
	}
	if !processUser.User.Sid.Equals(currentUser.User.Sid) {
		return "", fmt.Errorf("process is owned by another user")
	}

	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}
