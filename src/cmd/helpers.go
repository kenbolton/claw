// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"github.com/kenbolton/claw/driver"
)

func locateDriver(arch string, sourceDir ...string) (*driver.Driver, error) {
	return driver.Locate(arch, sourceDir...)
}
