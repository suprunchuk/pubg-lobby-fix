package update

import "golang.org/x/sys/windows"

func openURL(raw string) error {
	ptr, err := windows.UTF16PtrFromString(raw)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, ptr, nil, nil, windows.SW_SHOWNORMAL)
}
