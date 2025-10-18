package main

import (
	"fmt"
	"os/user"
	"strconv"
)

// resolveUser is a helper function to resolve a user on the operating system.
// It returns the user's home directory, their UID, their GID, and any errors.
func resolveUser(spec string) (string, uint32, uint32, error) {
	var resolvedUser *user.User

	_, err := strconv.ParseUint(spec, 10, 32)
	if err == nil { // UID
		u, err := user.LookupId(spec)
		if err != nil {
			return "", 0, 0, fmt.Errorf("lookup user %q failed: %w", spec, err)
		}
		resolvedUser = u
	} else { // Username
		u, err := user.Lookup(spec)
		if err != nil {
			return "", 0, 0, fmt.Errorf("lookup user %q failed: %w", spec, err)
		}
		resolvedUser = u
	}

	uid, err := strconv.ParseUint(resolvedUser.Uid, 10, 32)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid uid %q: %w", resolvedUser.Uid, err)
	}

	gid, err := strconv.ParseUint(resolvedUser.Gid, 10, 32)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid gid %q: %w", resolvedUser.Gid, err)
	}

	return resolvedUser.HomeDir, uint32(uid), uint32(gid), nil
}
