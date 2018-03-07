// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package vsphereclient

import (
	"fmt"
	"strconv"
	"strings"
)

// Version tracks the 1.2.3 style versions for API versioning in vsphere
type Version []int

// ParseVersion maps the string version "1.2.3" to a Version tuple [1, 2, 3]
func ParseVersion(s string) (Version, error) {
	v := make(Version, 0)
	ps := strings.Split(s, ".")
	for _, p := range ps {
		i, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid version: invalid integer: %q", s, p)
		}
		if i < 0 {
			return nil, fmt.Errorf("%q is not a valid version: negative sections are not allowed", s)
		}
		v = append(v, i)
	}

	return v, nil
}

func min(i, j int) int {
	if i < j {
		return i
	}
	return j
}

// Compare returns whether this version is less than, equal to, or greater than the other version.
func (v Version) Compare(u Version) int {
	minLen := min(len(v), len(u))
	for i := 0; i < minLen; i++ {
		if v[i] < u[i] {
			return -1
		} else if v[i] > u[i] {
			return 1
		}
	}
	// If we got this far, all of 'minLen' matched, everything else is compared as though the other version
	// was full of value '0'
	if len(v) > minLen {
		// All the rest get compared with '0'
		for i := minLen; i < len(v); i++ {
			if v[i] != 0 {
				return 1
			}
		}
	} else if len(u) > minLen {
		for i := minLen; i < len(u); i++ {
			if u[i] != 0 {
				return -1
			}
		}
	}
	return 0
}
