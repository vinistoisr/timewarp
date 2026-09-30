package install

import "golang.org/x/sys/windows"

// CurrentUser returns DOMAIN\user for the account that owns this process token.
func CurrentUser() (string, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	account, domain, _, err := tu.User.Sid.LookupAccount("")
	if err != nil {
		return "", err
	}
	return domain + `\` + account, nil
}
