// Package sshkey finds the user's SSH keys under ~/.ssh, including keys with custom names
// (id_ed25519_homelab…) that ssh only uses when told to.
package sshkey

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Private returns the private keys that have a matching id_*.pub, standard names first.
func Private() []string {
	var keys []string
	for _, pub := range publicFiles() {
		if key := strings.TrimSuffix(pub, ".pub"); exists(key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// LocalPublic returns the user's first public key, or "".
func LocalPublic() string {
	for _, pub := range publicFiles() {
		if data, err := os.ReadFile(pub); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// ForAuthorized returns the private key whose public half is one of authorized, so the
// nodes created with those keys can be reached.
func ForAuthorized(authorized []string) (string, bool) {
	for _, key := range Private() {
		data, err := os.ReadFile(key + ".pub")
		if err != nil {
			continue
		}
		for _, a := range authorized {
			if sameKey(string(data), a) {
				return key, true
			}
		}
	}
	return "", false
}

// sameKey compares type and key material, ignoring the comment.
func sameKey(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	return len(fa) >= 2 && len(fb) >= 2 && fa[0] == fb[0] && fa[1] == fb[1]
}

func publicFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".ssh")
	files := []string{}
	for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		if exists(filepath.Join(dir, name)) {
			files = append(files, filepath.Join(dir, name))
		}
	}
	others, _ := filepath.Glob(filepath.Join(dir, "id_*.pub"))
	for _, o := range others {
		if !slices.Contains(files, o) {
			files = append(files, o)
		}
	}
	return files
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
