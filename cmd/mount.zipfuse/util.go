package main

import (
	"fmt"
	"os/user"
	"strconv"
)

func resolveUser(spec string) (uint32, uint32, error) {
	uidNum, err := strconv.ParseUint(spec, 10, 32)
	if err == nil {
		uid := uint32(uidNum)

		return uid, uid, nil
	}

	u, err := user.Lookup(spec)
	if err != nil {
		return 0, 0, fmt.Errorf("lookup user %q failed: %w", spec, err)
	}

	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid uid %q: %w", u.Uid, err)
	}

	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid gid %q: %w", u.Gid, err)
	}

	return uint32(uid), uint32(gid), nil
}
