// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

//go:build notest

package testing

import _ "unsafe"

//go:linkname do_not_import_test_code_into_juju
func do_not_import_test_code_into_juju()

func init() {
	do_not_import_test_code_into_juju()
}
