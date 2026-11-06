// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// PoolEnvVar names the environment variable holding the path of a file that
// lists PostgreSQL URLs, one per line, each for a separate database. When it
// is set it takes precedence over URLEnvVar: each test process leases one
// slot and uses the URL at slot modulo the number of URLs, so parallel test
// packages are spread over the databases.
//
// A slot is an exclusive flock on <path>.slot-<n>.lock, taken without
// waiting and held until the process exits. A process takes the lowest free
// slot, so with go test -p P and N URLs each database serves about P/N
// processes at a time.
const PoolEnvVar = "JUJU_POSTGRESQL_TEST_URL_POOL"

// maxPoolSlots bounds the slots a process tries before giving up.
const maxPoolSlots = 256

var (
	poolOnce sync.Once
	poolURL  string
	poolErr  error
	// poolSlot keeps the slot's lock file open, and so locked, for the life
	// of the process.
	poolSlot *os.File
)

// baseURL returns the leased pool URL when PoolEnvVar is set, otherwise the
// value of URLEnvVar.
func baseURL() string {
	path := os.Getenv(PoolEnvVar)
	if path == "" {
		return os.Getenv(URLEnvVar)
	}
	poolOnce.Do(func() {
		poolURL, poolSlot, poolErr = leasePoolURL(path)
	})
	if poolErr != nil {
		panic(fmt.Sprintf("leasing a PostgreSQL test database from %s: %v",
			PoolEnvVar, poolErr))
	}
	return poolURL
}

// leasePoolURL locks the lowest free slot of the pool file and returns the
// URL for it with the open, locked slot file.
func leasePoolURL(path string) (string, *os.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var urls []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			urls = append(urls, line)
		}
	}
	if len(urls) == 0 {
		return "", nil, errors.New("pool file lists no URLs")
	}

	for slot := range maxPoolSlots {
		f, err := tryLockSlot(fmt.Sprintf("%s.slot-%d.lock", path, slot))
		if err != nil {
			return "", nil, err
		}
		if f != nil {
			return urls[slot%len(urls)], f, nil
		}
	}
	return "", nil, fmt.Errorf("all %d slots are held", maxPoolSlots)
}
